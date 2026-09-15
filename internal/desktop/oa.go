package desktop

// OA 办公工具的接线:端点推导、注册判据、以及那道"OA 数据只进本地模型"的闸门。
//
// 工具本身在 internal/oatool(与引擎无关的纯客户端);本文件回答的是三个只有外壳
// 知道答案的问题:
//
//  1. **往哪儿发** —— 由会话的 Bridge 基地址推出,选定的租户自动跟着(与联网搜索
//     同一套推导,见 websearch.go)。
//  2. **装不装** —— 三个条件缺一不装:通行证连接、该租户有可用的本地模型、该账号在
//     Passport 里绑了 OA。不装比装一批注定失败的工具好:15 条工具定义要占上下文,
//     还会让模型在不该用它的场合去试。
//  3. **这次让不让调** —— 闸门。OA 数据是保密数据,只允许进内网部署的模型。
//
// # 闸门为什么在这里而不在引擎里
//
// "哪个模型的推理跑在内网"是**平台事实**,由基座在模型清单里下发(protocol
// .PassportModel.Local);"这条工具的数据算不算保密"是**产品判断**。两者引擎都
// 不知道也不该知道。所以闸门是外壳造工具时随手扣上的那把锁,而不是引擎的一个开关。
//
// # 为什么不能靠"切模型"而要靠"不让调"
//
// 引擎里带模型的请求路径有六条,其中三条读的是**构建时**的 cfg.Model,运行时的
// SetModel 改不动它们:
//
//   - harm 判定 —— resolveHarmModel(cfg) 在非 anthropic provider 下恒等于 cfg.Model,
//     于是 Session.harmJudgeModel() 永远不会回落到 currentModel();
//   - 子代理(Task)—— subagent.NewLauncher(Model: cfg.Model);
//   - MCP sampling —— NewMCPSampler(provider, cfg.Model, …)。
//
// 也就是说"边跑边把模型切成本地"根本切不干净。真正换掉这三条的唯一办法是**换掉
// cfg.Model 并重建会话**,而重建不能在回合中途做。于是顺序反过来:先把这次调用
// 拦下(数据一个字节都没取),再由宿主在回合之外重建会话、重发消息。
//
// 安全性来自"没产生",不是"产生了但没交给云端"——数据一旦进了工具结果就会进会话
// 历史,而历史是要发给模型的。

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"

	"github.com/wt68/runcode/internal/oatool"
)

// localModelTTL 是本地模型解析结果的缓存时长。模型清单由基座的 ConfigMap 下发,
// 变更频率以天计;缓存短到没意义只会给每次开会话加一趟网络。
const localModelTTL = 5 * time.Minute

// localModelColdTimeout 是缓存冷启动时那一趟同步查询的上限。
//
// 原先是 2s,理由是"会话创建是延迟敏感路径"。那个取舍搞反了代价:省下的那 2 秒
// 换来的是**功能整个不可用**,而且是静默的——用户看到的是 OA 工具凭空消失,没有
// 任何提示。真实实测:公网 Bridge 冷启动 2.07s / 1.52s / 0.24s,2s 正好压在边界上,
// 于是时灵时不灵。建会话多等一两秒,远比"今天有 OA、明天没有"好。
const localModelColdTimeout = 8 * time.Second

// OA 绑定探测的缓存时长与超时。
//
// 这条链路更长:客户端 → Bridge → OA REST 面 → Passport.API → OA 人员库,所以给得
// 比模型清单还宽。绑定关系变更以人为单位、频率极低,成功结果缓存久一点没有代价。
const (
	oaBoundTTL      = 10 * time.Minute
	oaStatusTimeout = 10 * time.Second
	// oaBoundFailTTL 是**探测失败**的缓存时长,远短于成功结果。
	//
	// 失败与"确实没绑"是两回事:前者是网络抖动,下一次就可能好;后者要人去 Passport
	// 绑工号,十分钟内不会变。原先两者共用 10 分钟,于是一次超时会让 OA 工具消失
	// 十分钟——而用户完全不知道发生了什么,只会反复重开应用。
	oaBoundFailTTL = 30 * time.Second
)

// oaBindingEntry 是一条绑定探测结果。**false 也要缓存**:没绑 OA 的用户占多数,
// 不缓存否定结果等于让他们每次开会话都白问一趟。
//
// 但"没绑"与"没问到"要分开记(failed):后者只缓存很短一段(oaBoundFailTTL),
// 网络抖一下不该让 OA 工具消失十分钟。
type oaBindingEntry struct {
	bound  bool
	failed bool
	at     time.Time
}

// ttl 返回这条记录该被信任多久。
func (e oaBindingEntry) ttl() time.Duration {
	if e.failed {
		return oaBoundFailTTL
	}
	return oaBoundTTL
}

// localModelEntry 是一条缓存记录。**空值是有意义的**:它表示"问过了,这个租户没有
// 本地模型",与"还没问过"不同——前者不必反复重试。
type localModelEntry struct {
	model PassportModel
	found bool
	at    time.Time
}

// oaEndpoint 由会话的 Bridge 基地址推出 OA 接口基地址。BaseURL 形如
// <bridge>[/t/<租户>]/v1,所以选定的租户自动跟着——OA 的用量与对话记在同一个租户
// 名下,不必在这里再解析一遍租户。与 webSearchEndpoint 同一套推导。
func oaEndpoint(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if base == "" {
		return ""
	}
	return base + "/oa"
}

// resolveLocalModel 从模型清单里挑出这次要用的本地模型。
//
// 三条规则,按优先级:
//
//  1. 显式标了 localDefault 的那个胜出。**只标 local 是不够的**——清单顺序一变就
//     换了模型,而"OA 用哪个模型"必须是一句显式声明,不能是排序的副作用。
//  2. 没有默认、但本地模型只有一个时,用它。这是绝大多数部署的实际形态,为它多写
//     一行配置没有意义。
//  3. 多个本地模型却一个都没标默认 —— **判定为没有**,OA 工具不注册。这是有意
//     失败关闭:在"随便挑一个"和"暂时用不了"之间,前者会让保密数据流向一个没人
//     指定过的模型。
func resolveLocalModel(models []PassportModel) (PassportModel, bool) {
	var locals []PassportModel
	for _, m := range models {
		if !m.Local {
			continue
		}
		if m.LocalDefault {
			return m, true
		}
		locals = append(locals, m)
	}
	if len(locals) == 1 {
		return locals[0], true
	}
	return PassportModel{}, false
}

// localModel 返回该租户可用的本地模型(带缓存)。
//
// 拿不到一律报"没有":Bridge 不可达、令牌过期、清单里没有本地模型,这几种情况的
// 结果都该是"OA 工具这次不装",而不是猜一个模型出来。
func (a *App) localModel(tenantID string) (PassportModel, bool) {
	tenantID = strings.TrimSpace(tenantID)
	a.mu.Lock()
	entry, ok := a.localModels[tenantID]
	a.mu.Unlock()
	if ok && time.Since(entry.at) < localModelTTL {
		return entry.model, entry.found
	}
	// 拉清单这一步顺手就把缓存刷了(见 passportModelsTimeout),所以这里只关心成败。
	//
	// 失败与"拉到了但没有本地模型"必须分开记:前者是网络/令牌问题(改 Bridge 地址、
	// 重新登录),后者是基座配置或租户授权问题(改 catalog、去 AI.Core 授权)。
	// 混成一句话的代价是排查时朝完全错误的方向找——这一条是真实排查里踩过的。
	models, err := a.passportModelsTimeout(tenantID, localModelColdTimeout)
	if err != nil {
		debugLog("oa: 拉租户 %q 的模型清单失败(不是没有本地模型,是没问到): %v", tenantID, err)
		// 查不到就沿用上一次的已知结果(如果有):Bridge 抖一下不该让正在用的 OA
		// 工具凭空消失。彻底没有过结果时才是"没有"。
		if ok {
			return entry.model, entry.found
		}
		return PassportModel{}, false
	}
	a.mu.Lock()
	fresh := a.localModels[tenantID]
	a.mu.Unlock()
	if !fresh.found {
		ids := make([]string, 0, len(models))
		for _, m := range models {
			ids = append(ids, m.ID)
		}
		debugLog("oa: 租户 %q 的清单里没有 local 模型;该租户可用的是 %v", tenantID, ids)
	}
	return fresh.model, fresh.found
}

// rememberLocalModel 记下一份模型清单的解析结果。PassportModels 每次成功都会调它,
// 所以正常使用下(登录、开模型选择器)缓存总是热的,新建会话几乎不必等网络。
func (a *App) rememberLocalModel(tenantID string, models []PassportModel) {
	model, found := resolveLocalModel(models)
	a.mu.Lock()
	if a.localModels == nil {
		a.localModels = map[string]localModelEntry{}
	}
	a.localModels[strings.TrimSpace(tenantID)] = localModelEntry{model: model, found: found, at: time.Now()}
	a.mu.Unlock()
}

// oaTools 造这条会话的 OA 工具;不满足注册条件时返回 nil。
//
// 判据一:必须是通行证连接。判据是 TokenSource——只有通行证那条路会装它,自填端点
// 与自定义模型一定把它清成 nil。**这是安全红线**:OA 令牌与 OA 数据都不能出现在
// 一条指向第三方端点的连接上。与 websearch.go 的 usingPassport 同一个判据。
//
// 判据二:该租户得有可用的本地模型。没有本地模型,这批工具就永远过不了闸门,装上
// 只会让模型反复去试、每次撞一堵墙。这也是那条"租户没开通本地模型时绝不退回云端"
// 的落点——不装,而不是装上再退让。
//
// 判据三:该账号在 Passport 里绑了 OA。没绑的人调任何一条都只会拿到同一句"请联系
// 管理员绑定工号",让模型带着 15 条注定失败的工具走完全程没有意义。
// 三个条件每一条都记日志。不装是静默的(界面上就是什么都没有,不报错),而三条
// 判据分别依赖登录态、基座配置、Passport 绑定——没有日志就只能靠猜,一次真实排查
// 为此绕了一大圈。**这几行是这个功能唯一的可诊断性**,别删。
func (a *App) oaTools(cfg *engine.Config, sessionID string) []tool.Tool {
	if !usingPassport(cfg) {
		debugLog("oa: 不装工具——当前不是通行证连接(自填端点/自定义模型不发 OA 令牌)")
		return nil
	}
	tenant := passportTenantOf(cfg.BaseURL)
	local, ok := a.localModel(tenant)
	if !ok {
		debugLog("oa: 不装工具——租户 %q 的模型清单里没有可用的本地模型"+
			"(检查 /v1/models 是否返回带 local:true 的模型;基座标了但租户没被授权该模型时它不会出现)", tenant)
		return nil
	}
	if !a.oaBound(tenant) {
		debugLog("oa: 不装工具——该账号未绑定 OA(/v1/oa/status 报 bound=false 或查询失败)")
		return nil
	}
	debugLog("oa: 已注册 %d 个 OA 工具,本地模型=%s(窗口 %d)", len(oatool.Names()), local.ID, local.ContextTokens)
	return oatool.New(oatool.Config{
		Endpoint:       oaEndpoint(cfg.BaseURL),
		Token:          cfg.TokenSource,
		OnUnauthorized: cfg.OnUnauthorized,
		Gate:           a.oaGate(sessionID, local.ID),
	})
}

// oaGate 造这条会话的闸门:当前会话跑在 localModel 上才放行。
//
// 读的是**引擎自己的**会话状态(Status().Model),不是建会话时那份 cfg.Model 的
// 副本。差别在于:万一将来有哪条路径调了 SetModel,副本会过期、闸门会误开,而误开
// 的表现是保密数据被送去云端且无人知晓。安全判断必须读权威来源。
//
// 会话查不到(已关闭/正在重建)时**拒绝**。失败关闭是这道闸唯一可接受的默认。
//
// 放行的那一刻同时上锁(见 oalock.go):从这里往后,这条会话的历史里就可能有 OA
// 数据了。锁得比"真的取回了数据"早一点是有意的——工具可能失败(没绑 OA、OA 不可达),
// 那时锁是多余的;但反过来,等结果回来再锁就有一个窗口,窗口里用户切走模型就漏了。
// 在"偶尔多锁一条会话"和"偶尔漏一次"之间,没什么可犹豫的。
func (a *App) oaGate(sessionID, localModel string) func(context.Context, string, string) error {
	return func(_ context.Context, _ string, toolUseID string) error {
		s, err := a.mgr.Session(sessionID)
		if err != nil {
			return oaGateError("", localModel)
		}
		current := strings.TrimSpace(s.Status().Model)
		if strings.EqualFold(current, localModel) {
			a.lockSessionToLocalModel(sessionID, localModel)
			return nil
		}
		// 比的是哪两个值要能看见:这道闸判错一次,后果是"切换→重发→再切"的无限
		// 重建(每轮真跑一次模型)。出过这个事故,而当时日志里只有结果、没有依据。
		debugLog("oa: 闸门拦下——当前模型 %q != 本地模型 %q (会话 %s)", current, localModel, sessionID)
		return a.requestOASwitch(sessionID, current, localModel, toolUseID)
	}
}

// oaGateError 是闸门拒绝、且**不会**自动切换时回给模型的那句话。
//
// 它同时要做到三件事,少一件都会变成一次糟糕的对话:说清**为什么**(不是故障,是
// 规则)、告诉模型**不要自己编**(否则它会拿常识胡诌一份待办)、以及给用户一个
// **能执行的下一步**。
func oaGateError(current, localModel string) error {
	where := "当前会话"
	if strings.TrimSpace(current) != "" {
		where = fmt.Sprintf("当前会话使用的是 %s，", current)
	}
	return fmt.Errorf("%s不是本地模型，已阻止本次 OA 访问：OA 数据属于内部保密数据，"+
		"只能由内网部署的模型处理。请不要凭猜测回答 OA 相关问题，"+
		"直接告诉用户需要切换到本地模型 %s 后重试。", where, localModel)
}

// oaSwitchingError 是闸门拒绝、但宿主已经排好自动切换时回给模型的那句话。
//
// 与上一条的区别只在最后一句:这里要模型**闭嘴等着**,别再解释一遍"你需要切换
// 模型"——用户马上就会看到系统提示,而这条回合的产物随后会被本地模型重跑一遍。
// 模型在这里多说一句,用户就会在切换提示之前先读到一段没用的解释。
func oaSwitchingError(localModel string) error {
	return fmt.Errorf("已阻止本次 OA 访问：OA 数据只能由内网部署的模型处理。"+
		"系统正在把本会话切换到本地模型 %s 并自动重试，"+
		"请立即结束本轮回答，不要解释、不要猜测 OA 内容、不要再调用其它工具。", localModel)
}

// oaToolClasses 是 OA 工具的权限归类。
//
// 全部 ClassReadOnly(免审批)。三条理由:它们物理上只读(服务端没有任何写接口)、
// 只读得到调用者本人的数据(身份来自令牌,模型改不了)、且该不该让你看由 OA 自己
// 判权。让用户为查一次待办点一下确认,只会把审批训练成无意识的点击。
//
// 归类必须由供给工具的一方给出:引擎的解析器按固定工具名分支,不认识的名字会解析
// 成 unknown/高风险,而默认策略对它是**硬拒**——漏了这张表的表现是工具一调就被拒。
var oaToolClasses = func() map[string]permissions.ToolClass {
	out := make(map[string]permissions.ToolClass, len(oatool.Names()))
	for _, name := range oatool.Names() {
		out[name] = permissions.ClassReadOnly
	}
	return out
}()

// oaBound 报告当前账号有没有 OA 访问权(带缓存)。
//
// 与本地模型同一套缓存策略,理由也一样:会话创建是延迟敏感路径。绑定关系变更以人
// 为单位、频率极低,10 分钟的缓存换掉每次开会话的一趟网络是划算的;代价是刚绑完
// 可能要等一会儿才出现 OA 工具,而那是一件本来就要等管理员操作的事。
//
// **拿不到一律报"没绑"**:Bridge 不可达、OA 服务没部署(503)、令牌过期,这几种情况
// 的结果都该是"这次不装 OA 工具"。装上一批必然失败的工具比没有更糟。
func (a *App) oaBound(tenantID string) bool {
	tenantID = strings.TrimSpace(tenantID)
	a.mu.Lock()
	entry, ok := a.oaBindings[tenantID]
	a.mu.Unlock()
	if ok && time.Since(entry.at) < entry.ttl() {
		return entry.bound
	}
	bound, failed := a.fetchOABound(tenantID)
	a.mu.Lock()
	if a.oaBindings == nil {
		a.oaBindings = map[string]oaBindingEntry{}
	}
	a.oaBindings[tenantID] = oaBindingEntry{bound: bound, failed: failed, at: time.Now()}
	a.mu.Unlock()
	return bound
}

// fetchOABound 问一次 Bridge 的 /v1/oa/status。第二个返回值报告"没问到"
// (与"问到了、确实没绑"区分,见 oaBindingEntry.ttl)。
func (a *App) fetchOABound(tenantID string) (bound, failed bool) {
	body, _, err := a.bridgeGetStatusTimeout(tenantPathPrefix(tenantID)+"/v1/oa/status", oaStatusTimeout)
	if err != nil {
		debugLog("oa: 查询 OA 绑定失败(本次不装 OA 工具,%s 后重试): %v", oaBoundFailTTL, err)
		return false, true
	}
	var payload struct {
		State bool `json:"state"`
		Data  struct {
			Bound bool `json:"bound"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		debugLog("oa: 解析 OA 绑定结果失败: %v", err)
		return false, true
	}
	return payload.State && payload.Data.Bound, false
}

// passportTenantOf 从 Bridge 基地址里取回选定的租户。BaseURL 形如
// <bridge>[/t/<租户>]/v1,没有 /t/ 段就是"令牌自带租户",返回空。
//
// 从 URL 反解而不是读 a.passportTenant:那个字段是"下一个会话"的选择,而这里要的
// 是**这条会话实际路由到哪个租户**。两者在"改了设置还没重开会话"的窗口里不同,
// 而模型清单必须与实际路由一致,否则会拿另一个租户的模型去判断闸门。
func passportTenantOf(baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	base = strings.TrimSuffix(base, "/v1")
	if i := strings.LastIndex(base, "/t/"); i >= 0 {
		return base[i+len("/t/"):]
	}
	return ""
}
