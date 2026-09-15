package desktop

// OA 的自动切换编排:闸门拦下一次调用之后,把这条会话整个换到本地模型上再重跑。
//
// # 时序
//
//	用户提问
//	  └─ 云端模型 → 调 oa_todo
//	       └─ 闸门拦下(**没有发出任何 OA 请求**),排一次待切换,回一句"马上切,你别说话"
//	  └─ 回合自然结束(onTurnEnd)
//	       └─ 必要时先压缩(此刻历史里还没有 OA 数据,走云端摘要是安全的)
//	       └─ 按本地模型重建会话(恢复历史)
//	       └─ 替用户重发原消息
//	  └─ 本地模型 → 调 oa_todo → 这次才真的打 OA
//
// # 为什么是"重建会话"而不是 SetModel
//
// 引擎里带模型的请求路径有六条,其中三条读的是**构建时**的 cfg.Model,运行时的
// SetModel 改不动:harm 判定(resolveHarmModel 在非 anthropic provider 下恒等于
// cfg.Model)、子代理(subagent.NewLauncher(Model: cfg.Model))、MCP sampling
// (NewMCPSampler(provider, cfg.Model, …))。换掉 cfg.Model 再重建,这三条才一起跟着走。
//
// # 为什么等回合结束,而不是当场打断
//
// 重建会话会把正在跑的回合连根拔掉,中途打断还要处理半条工具结果的提交。等它自然
// 结束的代价只是模型多说一句话(闸门的措辞已经在要求它闭嘴),换来的是不必碰引擎
// 里最微妙的那段状态机。
//
// # 只对聚焦会话做
//
// 重建走的是"关掉再按同一个 id 恢复"那条路,它同时会切聚焦与工作区。对一条后台
// 会话这么干,等于在用户正看着别处时把界面抽走。后台会话的 OA 调用照样被闸门拦住
// (数据依然不出内网),只是不自动切——用户切到那条会话再问一次即可。

import (
	"fmt"
	"strings"

	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/host"
)

// oaCompactHeadroom 是切换前判断"历史放不放得下"的余量系数。
//
// 本地模型的窗口通常远小于云端模型,直接把一段云端会话的历史搬过去,第一次请求就
// 可能超限——而那时 OA 数据已经在路上了,没有退路。留 20% 余量是因为估算本身有误差,
// 而且切过去之后还要放得下这一轮的工具结果。
const oaCompactHeadroom = 0.8

// lockSessionToLocalModel 记下"这条会话读过 OA",内存与磁盘各一份。
//
// 磁盘那份是给 Resume 用的(见 oalock.go 的说明);写失败只记日志不打断——锁的即时
// 效力来自内存与闸门,落盘失败最坏是"重启后忘了锁",而不是"这次就漏了"。
func (a *App) lockSessionToLocalModel(sessionID, localModel string) {
	a.mu.Lock()
	e := a.entryLocked(sessionID)
	if e == nil || e.oaLocalModel != "" {
		a.mu.Unlock()
		return // 没这条会话,或者已经锁过了
	}
	e.oaLocalModel = localModel
	ws := e.workspace
	a.mu.Unlock()
	if err := writeOALock(ws, sessionID, localModel); err != nil {
		debugLog("oa: 锁落盘失败 session=%s: %v", sessionID, err)
	}
}

// oaLockedModel 返回一条会话被锁定到的本地模型("" = 未锁)。
func (a *App) oaLockedModel(sessionID string) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if e := a.entryLocked(strings.TrimSpace(sessionID)); e != nil {
		return e.oaLocalModel
	}
	return ""
}

// requestOASwitch 在闸门拦下一次调用时排一次自动切换,并返回给模型的说明。
//
// 三种情况不排、只回普通拒绝:会话不在表里、不是当前聚焦的那条(理由见文件头)、
// 或者本会话已经自动切过一次(防重入——切完还过不了闸说明有别的问题,再切一次就是
// 死循环,而每一轮都真的跑一次模型)。
func (a *App) requestOASwitch(sessionID, current, localModel, toolUseID string) error {
	a.mu.Lock()
	e := a.entryLocked(sessionID)
	switch {
	case e == nil, a.focused != sessionID, e.oaSwitchDone:
		a.mu.Unlock()
		return oaGateError(current, localModel)
	}
	e.oaSwitchPending = localModel
	e.oaResendText = e.lastUserText
	emit := e.emit
	a.mu.Unlock()
	// 告诉界面"这一次调用是被策略拦下的,不是真失败"。
	//
	// 工具结果本身必须是 IsError:调用确实没跑成,模型要据此停下来,把它伪装成成功
	// 既骗了模型也骗了用户。但界面上一个红色的"执行失败"同样不对——系统正在自动
	// 恢复,而且这一轮随后会被整个重跑。所以让界面按 toolUseID 精确改写那一张卡片。
	//
	// 用 id 而不是按文案匹配:措辞改一次,匹配就会静默失效,而失效的表现是界面退回
	// 报错——没人会注意到,直到有人问"怎么又报错了"。
	if emit != nil {
		emit(EventOABlocked, OABlocked{ToolUseID: toolUseID, LocalModel: localModel})
	}
	return oaSwitchingError(localModel)
}

// takeOASwitch 取走并清掉一条会话的待切换请求,同时立上防重入标记。
func (a *App) takeOASwitch(sessionID string) (localModel, resendText string, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e := a.entryLocked(sessionID)
	if e == nil || e.oaSwitchPending == "" {
		return "", "", false
	}
	localModel, resendText = e.oaSwitchPending, e.oaResendText
	e.oaSwitchPending, e.oaResendText = "", ""
	// 标记在**发起**切换时就立,不等切换成功:切换失败同样不该重来一遍。
	e.oaSwitchDone = true
	return localModel, resendText, true
}

// maybeSwitchToLocalModel 是 onTurnEnd 上的那一半:有待切换就在回合之外执行它。
// 由 onTurnEnd 在自己的 goroutine 里调用,所以这里可以阻塞。
func (a *App) maybeSwitchToLocalModel(sctx host.SessionContext) {
	localModel, resendText, ok := a.takeOASwitch(sctx.ID)
	if !ok {
		return
	}
	if err := a.switchToLocalModel(sctx, localModel, resendText); err != nil {
		// 切换失败没有退路:OA 数据不能退回云端模型去处理,所以只能如实说明。
		// 说清是"切换失败"而不是"OA 查不到",否则用户会去查 OA 那边的问题。
		sctx.Emit(EventWarning, Warning{Message: fmt.Sprintf(
			"切换到本地模型 %s 失败：%v。OA 查询无法继续——OA 数据只能由本地模型处理，不会退回云端模型。",
			localModel, err)})
	}
}

// switchToLocalModel 压缩(如有必要)→ 重建 → 重发。
func (a *App) switchToLocalModel(sctx host.SessionContext, localModel, resendText string) error {
	a.mu.Lock()
	cfg := a.liveConfig
	passport, tenant := a.livePassport, a.livePassportTenant
	a.mu.Unlock()
	if !passport {
		// 闸门只在通行证连接上才会装,走到这里说明连接在本回合里被换掉了。
		return fmt.Errorf("当前连接不是平台连接")
	}

	local, found := a.localModel(tenant)
	if !found || !strings.EqualFold(local.ID, localModel) {
		// 模型清单在这个窗口里变了(改了 ConfigMap、换了租户)。宁可失败也不切到
		// 一个此刻已经不再被声明为"本地"的模型上去。
		return fmt.Errorf("本地模型 %s 已不在当前租户的可用清单里", localModel)
	}

	// 已经在目标模型上就什么都不做。
	//
	// **这是防循环的最后一道,也是唯一一道不依赖任何标记的**:重建会话会新建
	// sessionEntry(registerSessionLocked 每次都 &sessionEntry{}),于是 oaSwitchDone
	// 那个防重入标记随之归零;只要闸门再判一次失败,就会"切换→重发→再切"无限循环,
	// 每一轮都真的跑一次模型。真实事故就是这么发生的。
	//
	// 判据读引擎自己的状态,不读任何本地镜像——镜像正是上面那条链里会过期的东西。
	if s, err := a.mgr.Session(sctx.ID); err == nil {
		if cur := strings.TrimSpace(s.Status().Model); strings.EqualFold(cur, local.ID) {
			debugLog("oa: 已经在本地模型 %s 上,跳过切换(闸门误判?)", cur)
			a.lockSessionToLocalModel(sctx.ID, local.ID)
			return nil
		}
	}

	a.shrinkHistoryForLocalModel(sctx.ID, local)

	sctx.Emit(EventWarning, Warning{Message: fmt.Sprintf(
		"检测到 OA 查询：OA 数据属于内部保密数据，已将本会话切换到本地模型 %s 并重新处理你的请求。"+
			"本会话之后将一直使用该模型。", local.ID)})

	a.startMu.Lock()
	cfg = applyLocalModelConfig(cfg, local, a.foreignModelAgents(local.ID))
	_, err := a.rebuildResumingWithConnectionHeld(cfg, sctx.ID, true, tenant)
	a.startMu.Unlock()
	if err != nil {
		return err
	}
	persistConnectionChoice("passport", local.ID, "")

	// **把会话 sidecar 里记的模型也改掉**，否则这次切换根本不生效。
	//
	// 重建走的是 cfg.Resume，而引擎恢复会话时会拿 sidecar 里存的运行时设置回灌
	// （host/manager.go 的 applyMeta → SetModel），把我们刚设的 cfg.Model 覆盖回
	// 用户上次手动选的那个。表现极具迷惑性：桌面侧的 liveConfig 显示已经是本地模型
	// （日志里的 turn submit 也这么写），引擎里却还是旧模型，于是闸门每轮都判失败
	// ——"切换→重发→再切"的无限循环就是这么来的，而且看上去像闸门误判。
	//
	// Manager.SetModel 正好一并做两件事：设引擎模型 + 写 sidecar，两处从此一致。
	if id := a.focusedSessionID(); id != "" {
		if err := a.mgr.SetModel(id, local.ID); err != nil {
			debugLog("oa: 重建后同步模型到会话 meta 失败: %v", err)
		}
	}

	// 告诉前端"模型已经换了"。
	//
	// 界面上的模型、计划模式这些开关平时都是"前端调后端 → 拿返回值更新自己",
	// 后端自作主张改掉的东西前端不会知道。少了这一步的表现是:模型确实切了、请求
	// 也发给新模型了,右下角却还显示旧模型——用户合理地以为切换没生效。
	if info, err := a.Status(""); err == nil {
		sctx.Emit(EventSessionStatus, info)
	}

	// 把 OA 状态搬到重建出来的那条会话上。
	//
	// 重建 = 新建 sessionEntry,三个字段全部归零:oaSwitchDone(防重入)、oaLocalModel
	// (出网工具封锁的依据)、以及落盘锁的内存镜像。落盘那份按会话 id 存,而重建后
	// id 未必相同,所以不能指望它自己恢复——显式搬,才与 id 无关。
	a.adoptOAStateAfterRebuild(local.ID)

	// 重建之后聚焦的就是这条会话(buildSessionHeld 登记时顺带切聚焦),所以按""重发。
	if strings.TrimSpace(resendText) == "" {
		return nil // 没有可重发的原文(理论上不该发生),切换本身已经完成
	}
	return a.sendUserTurn("", resendText, nil, false)
}

// adoptOAStateAfterRebuild 把 OA 状态过继给重建出来的那条会话(此时它已是聚焦的那条)。
//
// 少了这一步有两个后果,都不会报错:防重入标记归零 → 下一次闸门误判就无限重建;
// OA 锁归零 → 出网工具(联网搜索/抓网页)重新放行,而历史里已经有 OA 数据了。
func (a *App) adoptOAStateAfterRebuild(localModel string) {
	a.mu.Lock()
	e := a.entryLocked(a.focused)
	if e == nil {
		a.mu.Unlock()
		return
	}
	e.oaSwitchDone = true
	id, ws := e.id, e.workspace
	already := e.oaLocalModel != ""
	if !already {
		e.oaLocalModel = localModel
	}
	a.mu.Unlock()
	if !already {
		// 落盘那份也按新 id 补一份,这样关掉重开仍然锁着。
		if err := writeOALock(ws, id, localModel); err != nil {
			debugLog("oa: 重建后补写锁失败 session=%s: %v", id, err)
		}
	}
}

// applyLocalModelConfig 把一份会话配置改造成"跑在本地模型上"。
//
// 四处一起改,少一处就漏一条:
//
//   - Model —— 主循环、自动标题、压缩摘要都读它;
//   - MaxContextTokens —— 本地模型窗口小得多,不跟着改的话第一次请求就可能超限;
//   - HarmJudgeModel —— 用户可以在设置里单独指定判定模型,那是**会话级固定**的
//     (引擎没有运行时 setter),清掉它才会回落到 resolveHarmModel(cfg) = 新的
//     cfg.Model。留着的话,智能模式下每次审批都会把动作描述发给云端判定模型;
//   - DisabledAgents —— 子代理定义里的 model: 会覆盖 launcher 的默认值,那是唯一
//     一条能绕开本地模型的子代理路径(见 foreignModelAgents)。
func applyLocalModelConfig(cfg engine.Config, local PassportModel, foreignAgents []string) engine.Config {
	cfg.Model = local.ID
	if local.ContextTokens > 0 {
		cfg.MaxContextTokens = local.ContextTokens
	}
	cfg.HarmJudgeModel = ""
	cfg.DisabledAgents = mergeDisabled(cfg.DisabledAgents, foreignAgents)
	return cfg
}

// mergeDisabled 并集,保持稳定顺序且不重复。
func mergeDisabled(existing, extra []string) []string {
	seen := make(map[string]bool, len(existing)+len(extra))
	out := make([]string, 0, len(existing)+len(extra))
	for _, group := range [][]string{existing, extra} {
		for _, name := range group {
			name = strings.TrimSpace(name)
			if name == "" || seen[name] {
				continue
			}
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

// foreignModelAgents 列出那些在定义里把模型钉死成**别的**模型的子代理。
//
// 重建之后子代理默认继承 cfg.Model(也就是本地模型),但 agent 定义里的 model: 字段
// 会覆盖这个默认值(引擎的 subagent launcher 明确如此)。于是一个写着
// `model: glm-5.1` 的子代理,就是主模型把 OA 内容塞进 Task prompt 之后的一条出口。
//
// 处理办法是把这类子代理停掉,而不是把 Task 整个禁掉:禁 Task 会连带废掉一个正常
// 能力,而真正有问题的只是"自己指定了别的模型"的那几个。没写 model: 的子代理不受
// 影响,它们跟着本地模型走。
func (a *App) foreignModelAgents(localModel string) []string {
	var out []string
	for _, ag := range a.ListAgents().Agents {
		m := strings.TrimSpace(ag.Model)
		if m != "" && !strings.EqualFold(m, localModel) {
			out = append(out, ag.Name)
		}
	}
	return out
}

// shrinkHistoryForLocalModel 在切换前把历史压到本地模型放得下的程度。
//
// **顺序很重要**:压缩要在切换之前做。压缩摘要是一次模型调用,此刻会话还在云端
// 模型上——而此刻历史里还没有任何 OA 数据(闸门把那次调用挡掉了),所以这次云端调用
// 是安全的。反过来先切再压,摘要就要由刚切过去的本地模型做,历史却可能已经超出它的
// 窗口,那正是我们要避免的那次失败。
//
// 尽力而为:压不动就照常切,让"上下文太长"以一个明确的模型错误暴露出来,而不是在
// 这里把整条切换取消掉。
func (a *App) shrinkHistoryForLocalModel(sessionID string, local PassportModel) {
	if local.ContextTokens <= 0 {
		return // 基座没声明窗口大小,无从判断
	}
	s, err := a.mgr.Session(sessionID)
	if err != nil {
		return
	}
	budget := int(float64(local.ContextTokens) * oaCompactHeadroom)
	if s.EstimateContextTokens() <= budget {
		return
	}
	debugLog("oa: 历史超出本地模型预算(%d > %d),切换前先压缩", s.EstimateContextTokens(), budget)
	if _, err := a.Compact(sessionID); err != nil {
		debugLog("oa: 切换前压缩失败(继续切换): %v", err)
	}
}

// restoreOALockHeld 在开会话时把磁盘上的锁读回来并钉住模型。
//
// 这是"关掉重开就能绕过锁"那个洞的补丁:恢复出来的历史仍然带着 OA 数据,而内存里
// 的锁随上一个进程一起没了。调用方须持 startMu(它在 buildSessionHeld 里,那是每条
// 会话的唯一入口)。
//
// 只对 Resume 生效:新会话没有历史,自然也没有 OA 数据。
func (a *App) restoreOALockHeld(cfg engine.Config) engine.Config {
	resume := strings.TrimSpace(cfg.Resume)
	if resume == "" {
		return cfg
	}
	lock, ok := readOALock(cfg.CWD, resume)
	if !ok {
		return cfg
	}
	if strings.EqualFold(strings.TrimSpace(cfg.Model), lock.LocalModel) {
		return cfg // 已经是锁定的那个模型
	}
	debugLog("oa: 恢复会话 %s 的 OA 锁,模型钉回 %s", resume, lock.LocalModel)
	cfg.Model = lock.LocalModel
	cfg.HarmJudgeModel = ""
	cfg.DisabledAgents = mergeDisabled(cfg.DisabledAgents, a.foreignModelAgents(lock.LocalModel))
	return cfg
}
