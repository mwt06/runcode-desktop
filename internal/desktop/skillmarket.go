package desktop

// 技能市场：把 lea-config 上架的 skill 列出来、装到本地。
//
// 链路：桌面 → lea-gateway(:18093) → lea-config。**永远打网关**——lea-config 是
// ClusterIP，不对外。网关按 /api/user 前缀转发（转发时会把这段前缀剥掉），并要求
// 两个头，缺一不可：
//
//	Authorization:        Bearer <通行证 access_token>
//	X-Selected-Tenant-ID: <租户 id>
//
// 装到哪：由安装时选的作用域决定，落到与「导入技能文件夹」同一个地方——全局
// （userResourceDir）或本项目（<workspace>/.runcode/skills）。装完就出现在插件页的
// 「技能」栏里，可停用、可删除、可编辑——市场只是多一条获取途径，不是另一套并行的
// 技能体系。
//
// 与 MCP 市场（mcpmarket.go）的区别：那个是 Bridge 上的一份配置清单，装的是「连接
// 方式」；这个是真的要把文件拉到本地磁盘上，所以多了校验、解压与目录归一。

import (
	"archive/zip"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

const (
	// skillMarketSize 是每页条数。上限 200，**参数名必须是 size**——上游是 FastAPI，
	// 传错名字（比如 page_size）不会报错，会被静默忽略然后按默认 50 返回第一页。
	skillMarketSize = 200
	// skillMarketMaxPages 给翻页封个顶。按 total 翻页本身是对的，但 total 来自服务端，
	// 一旦它算错就是个不会停的循环。
	skillMarketMaxPages = 10
	// skillMarketTTL 是清单的缓存时长。市场按它自己的节奏变，不必每次开页面都跑一趟网络。
	skillMarketTTL = 5 * time.Minute
	// skillBundleMaxBytes / skillBundleMaxFiles 是解压的两道闸：一个压缩炸弹能把磁盘写满，
	// 而这些包是从网络来的。
	skillBundleMaxBytes = 200 << 20
	skillBundleMaxFiles = 4000
	// skillDownloadTTL 是预签名下载链接的有效期，够慢速网络下完一个大包。
	skillDownloadTTL = 900
	// skillDownloadTimeout 是下载一个技能包的时长上限。
	skillDownloadTimeout = 10 * time.Minute
)

// skillMarketBaseURL 是网关地址。默认值是当前部署，环境变量可覆盖——换环境
// （测试网关、内网直连）不该重新打包。
func skillMarketBaseURL() string {
	return strings.TrimRight(envOr("RUNCODE_SKILL_MARKET_BASE_URL", "http://123.249.111.75:18093"), "/")
}

// marketSkillWire 是上游 /skills 返回的一行。字段是 snake_case，只解析用得上的。
type marketSkillWire struct {
	ID           int      `json:"id"`
	Name         string   `json:"name"`
	DisplayName  string   `json:"display_name"`
	Description  string   `json:"description"`
	Category     string   `json:"category"`
	Version      string   `json:"version"`
	AllowedTools []string `json:"allowed_tools"`
	HasBundle    bool     `json:"has_bundle"`
	// Enabled 与可见性是两回事：一个 global 的技能完全可以是停用的。停用的不进列表——
	// 装了也不会被下发。
	Enabled bool `json:"enabled"`
	// SystemPrompt 是 SKILL.md 的正文全文。列表里也带，但桌面只在**详情**里用它
	// （纯提示词型技能靠它生成本地 SKILL.md）。
	SystemPrompt string `json:"system_prompt"`
}

// marketListWire 是列表响应。**它是对象不是裸数组**——按数组解析会直接失败。
type marketListWire struct {
	Skills []marketSkillWire `json:"skills"`
	Total  int               `json:"total"`
	Page   int               `json:"page"`
	Size   int               `json:"size"`
}

// marketDownloadWire 是 /skills/{id}/download 的响应：一个带时效的对象存储直链。
type marketDownloadWire struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"`
	Filename  string `json:"filename"`
	SHA256    string `json:"sha256"`
}

// SkillMarket 返回市场页要画的全部内容（全平台可见的那批技能）。
//
// refresh 为 false 时命中缓存就直接返回；「已安装」标记每次都重算——它是本地事实，
// 与清单的新鲜度无关，装完一个技能不该还要等缓存过期才变。
func (a *App) SkillMarket(refresh bool) (protocol.SkillMarketPage, error) {
	a.mu.Lock()
	cached, at := a.skillMarket, a.skillMarketAt
	a.mu.Unlock()

	if refresh || cached == nil || time.Since(at) > skillMarketTTL {
		fresh, err := a.fetchSkillMarket()
		if err != nil {
			// 有旧清单就先用着：市场暂时打不开不该让整页变成一条错误。
			if cached == nil {
				return protocol.SkillMarketPage{}, wireError(err)
			}
		} else {
			cached, at = fresh, time.Now()
			a.mu.Lock()
			a.skillMarket, a.skillMarketAt = fresh, at
			a.mu.Unlock()
		}
	}
	return a.skillMarketPage(cached, at), nil
}

// skillMarketPage 把缓存的原始行组装成一页：标注已安装、汇总分类。
func (a *App) skillMarketPage(rows []marketSkillWire, at time.Time) protocol.SkillMarketPage {
	// 两个作用域各算各的：装到哪儿由用户选，卸载也得删对地方。用户级与项目级可以
	// 同时装着同名技能（用户级遮住项目级），所以这里不能合成一个布尔。
	//
	// 按目录看，不走 ListSkills——那份清单是模型看到的视图，被遮住的项目级技能根本
	// 不在里面，会显示成"没装"、连卸载入口都没有。
	installedUser := installedSkillNames(userResourceDir(kindSkills))
	installedProject := installedSkillNames(a.projectResourceDir(kindSkills))
	page := protocol.SkillMarketPage{
		Skills:     make([]protocol.MarketSkill, 0, len(rows)),
		Categories: []string{},
		Total:      len(rows),
		FetchedAt:  at.Format(time.RFC3339),
	}
	seen := map[string]bool{}
	for _, r := range rows {
		cat := strings.TrimSpace(r.Category)
		if cat == "" {
			cat = "general"
		}
		if !seen[cat] {
			seen[cat] = true
			page.Categories = append(page.Categories, cat)
		}
		page.Skills = append(page.Skills, protocol.MarketSkill{
			ID:           r.ID,
			Name:         r.Name,
			DisplayName:  strings.TrimSpace(firstNonEmpty(r.DisplayName, r.Name)),
			Description:  strings.TrimSpace(r.Description),
			Category:     cat,
			Version:      r.Version,
			AllowedTools: r.AllowedTools,
			HasBundle:    r.HasBundle,

			InstalledUser:    installedUser[r.Name],
			InstalledProject: installedProject[r.Name],
		})
	}
	sort.Strings(page.Categories)
	return page
}

// fetchSkillMarket 翻页拉全量。
//
// 按响应里的 total 判断结束，不是「拿到不满一页就停」：上游可能因为过滤而返回
// 少于 size 条却仍有下一页。
func (a *App) fetchSkillMarket() ([]marketSkillWire, error) {
	var out []marketSkillWire
	for page := 1; page <= skillMarketMaxPages; page++ {
		q := url.Values{}
		q.Set("visibility", "global")
		q.Set("size", strconv.Itoa(skillMarketSize))
		q.Set("page", strconv.Itoa(page))
		body, err := a.marketGet("/skills?"+q.Encode(), 20*time.Second)
		if err != nil {
			return nil, err
		}
		var res marketListWire
		if err := json.Unmarshal(body, &res); err != nil {
			return nil, fmt.Errorf("解析技能市场返回失败: %w", err)
		}
		for _, s := range res.Skills {
			// 停用的不上架给用户：装了也不会被下发，摆在那里只会让人以为坏了。
			// 名字不合法的同样跳过——它落不到本地目录里。
			if s.Enabled && validResourceName(s.Name) {
				out = append(out, s)
			}
		}
		if len(res.Skills) == 0 || len(out) >= res.Total || res.Total == 0 {
			break
		}
	}
	return out, nil
}

// marketGet 打网关取一个 JSON。
//
// 路径按文档里的裸形式传（/skills、/skills/1/download），这里补 /api/user 前缀——
// 网关只放行这个前缀，转发给 lea-config 时又会把它剥掉。两边都写一遍容易漏，收在这里。
//
// 租户就用**用户在设置里选的那个**，不猜、不换。曾经在这里放过一条"候选链"：选的
// 那个被拒就依次试账号名下的其它租户、最后试 default。它建立在一个错的判断上——
// 实测网关对「没带这个头」和「带了但不认」返回的是**同一句** 403 selected tenant
// is invalid，所以那条 403 根本不能证明"值不对"。拿它当依据去换租户，等于用一串
// 猜测掩盖一个还没查清的问题：真出错时用户看到的是"试过 A、B、default 都不行"，
// 而不是"你选的 A 不行"——后者才是他能拿去问平台的信息。
func (a *App) marketGet(pathAndQuery string, timeout time.Duration) ([]byte, error) {
	if a.tokens == nil || !a.tokens.LoggedIn() {
		return nil, errors.New("技能市场需要先登录平台账号")
	}
	tok, err := a.tokens.Token()
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	tenant := strings.TrimSpace(a.passportTenant)
	a.mu.Unlock()
	// 令牌里没有 manageapi 就别白跑一趟：网关会用它去查租户成员关系，缺了它任何租户
	// 都是 selected tenant is invalid——而那句话同时也是"没带租户头"和"租户不存在"的
	// 说法，指不出真正的原因。这里提前认出来，直接说该怎么办。
	//
	// 为什么会缺：授权范围只在登录时申请，刷新令牌不补。所以老的登录态升级到新版本
	// 之后仍然是旧的一串，必须退出重登一次。
	if !tokenHasScope(tok, marketScope) {
		return nil, errors.New("当前登录缺少技能市场需要的 " + marketScope + " 授权。请到「设置 → 平台账号」退出登录后重新登录一次（授权范围只在登录时申请，刷新令牌不会补上）")
	}
	if tenant == "" {
		// 网关拿这个头去查成员关系并算出角色，没有它一律拒。说清楚去哪儿选，
		// 比丢一个 403 给用户强——尤其因为网关对「没带」和「带了但不认」返回的是
		// 同一句 403，真漏了这个头，报错也不会告诉你是漏了。
		return nil, errors.New("技能市场需要先选择租户：设置 → 平台账号 里选一个")
	}
	return a.marketRequest(tok, tenant, pathAndQuery, timeout)
}

// marketRequest 发一次请求。从 marketGet 里拆出来是为了能在测试里直接给定令牌与
// 租户——否则要验「两个头发出去了没有」就得先伪造一个登录态。
func (a *App) marketRequest(token, tenant, pathAndQuery string, timeout time.Duration) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, skillMarketBaseURL()+"/api/user"+pathAndQuery, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Selected-Tenant-ID", tenant)

	resp, err := passportHTTP().Do(req)
	if err != nil {
		return nil, fmt.Errorf("访问技能市场失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusForbidden {
		// 403 在这里是**身份这一侧**的问题，不是租户名写错——但具体是哪一种，客户端
		// 分不出来，所以别替它下结论。已知的事实：
		//   · 同一份代码、同一个租户 wjtest，拿平台 Web 控制台的令牌（客户端
		//     aimanage、账号 Administrator）打是 200，拿桌面自己的令牌（客户端
		//     runcode-desktop、账号 wt123456）打是 403；
		//   · 而通行证那边（Bridge /api/tenants）确认 wt123456 属于这两个租户。
		// 两个变量同时不同（账号、令牌的受众 passportapi vs manageapi），网关又对
		// 「没带这个头」和「带了但不认」返回同一句话，所以三种可能都还开着。
		//
		// 报错要做的是把**判据**交出去（账号 + 租户 + 原话），让人拿着去问平台，
		// 而不是断言一个我们没验证过的原因。
		return nil, fmt.Errorf("技能市场拒绝了这个身份：账号 %s + 租户 %s。可能是这个账号在市场那边不属于该租户，也可能是桌面的登录令牌少了市场要求的授权范围——把这两个值发给平台侧确认。（网关原话：%s）",
			a.marketWho(), tenant, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		// 报错带上租户。上一轮排查全程在猜"到底发出去的是哪个租户"，而这个值本来
		// 就在手里，写出来就不用猜。
		// （404 在这套接口里既是「不存在」也是「你看不见」——跨租户不该泄露 id 是否
		// 存在，服务端刻意不分，所以照实说、别替它解释。）
		return nil, fmt.Errorf("技能市场返回 %d（租户 %s）: %s", resp.StatusCode, tenant, strings.TrimSpace(string(body)))
	}
	return body, nil
}

// marketScope 是这条链路要求的授权范围，见 passportConfig 的 Scopes。
const marketScope = "manageapi"

// tokenHasScope 看 JWT 里有没有某个授权范围。
//
// 只解 payload、不验签：这里不是安全判断（真正的验签在网关那边），只是想在发请求
// 之前认出"这份令牌肯定不行"，好把一句看不懂的 403 换成一句能照做的话。解不开就
// 当作有——宁可白跑一趟让服务端说话，也不要因为自己看不懂令牌就把功能挡死。
func tokenHasScope(token, scope string) bool {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return true
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return true
	}
	// scope 这一栏各家 IdP 不一样：有的是空格分隔的字符串，有的是数组。两种都认。
	var claims struct {
		Scope any `json:"scope"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		return true
	}
	switch v := claims.Scope.(type) {
	case string:
		for _, f := range strings.Fields(v) {
			if f == scope {
				return true
			}
		}
	case []any:
		for _, item := range v {
			if s, ok := item.(string); ok && s == scope {
				return true
			}
		}
	default:
		return true // 没有这一栏或者形状不认识，交给服务端判
	}
	return false
}

// marketWho 是当前登录账号的可读名字，只用于报错。取不到就回退到"当前账号"——
// 一句读得通的话，好过一个空括号。
func (a *App) marketWho() string {
	a.mu.Lock()
	u := a.passportUser
	a.mu.Unlock()
	if u == nil {
		return "当前账号"
	}
	if n := strings.TrimSpace(firstNonEmpty(u.UserName, u.Name, u.Nickname)); n != "" {
		return n
	}
	return "当前账号"
}

// InstallMarketSkill 把市场里的一个技能装到 scope 指定的技能目录（"user" 全局 /
// "project" 本工作区），并返回更新后的技能表。
//
// 装到哪由调用方给，不再一律装全局：同一个市场技能，有人想全项目通用，有人只想给
// 手头这个项目配上（还要能随项目一起提交进 .runcode/）。两种都是正当用法，客户端
// 替用户决定只会逼他装完再手工搬目录。
//
// 两条路：有 bundle 的下 zip 校验解压；纯提示词型（has_bundle=false）按详情里的
// system_prompt 就地生成 SKILL.md。重复安装等于**更新**：先解到临时目录，成了再
// 整体替换，中途失败不会留下一个半截的技能目录。
func (a *App) InstallMarketSkill(id int, scope string) (SkillList, error) {
	if id <= 0 {
		return SkillList{}, wireError(errors.New("无效的技能 id"))
	}
	// 进度发在**每一步真的开始时**，不是按定时器推的：这条链路会卡住的地方各不
	// 相同（取详情是网关、下载是对象存储、解压是文件数），假进度会把"卡在哪一步"
	// 这个唯一有用的信号也抹掉。
	step := func(stage string, name string, received, total int64) {
		a.sink.Emit(protocol.EventSkillInstall, protocol.SkillInstallProgress{
			ID: id, Name: name, Stage: stage, Received: received, Total: total,
		})
	}
	step(protocol.SkillInstallDetail, "", 0, 0)

	body, err := a.marketGet("/skills/"+strconv.Itoa(id), 20*time.Second)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	var detail marketSkillWire
	if err := json.Unmarshal(body, &detail); err != nil {
		return SkillList{}, wireError(fmt.Errorf("解析技能详情失败: %w", err))
	}
	// 本地目录名用 frontmatter 里的 name，不是展示名——上游也是这么定身份的，
	// 而且展示名是中文，落不进目录。
	if !validResourceName(detail.Name) {
		return SkillList{}, wireError(fmt.Errorf("技能名 %q 不能作为本地目录名", detail.Name))
	}
	root, err := a.resourceRoot(kindSkills, scope)
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SkillList{}, wireError(fmt.Errorf("创建技能目录失败: %w", err))
	}

	staging, err := os.MkdirTemp(root, ".installing-")
	if err != nil {
		return SkillList{}, wireError(fmt.Errorf("创建临时目录失败: %w", err))
	}
	defer func() { _ = os.RemoveAll(staging) }()

	if detail.HasBundle {
		err = a.downloadSkillBundle(id, staging, func(stage string, received, total int64) {
			step(stage, detail.Name, received, total)
		})
	} else {
		err = writePromptSkill(staging, detail)
	}
	if err != nil {
		return SkillList{}, wireError(err)
	}
	if !hasSkillManifest(staging) {
		return SkillList{}, wireError(errors.New("这个技能包里没有 SKILL.md，装不了"))
	}
	// 把市场目录里那两句给人看的话（展示名 + 描述）落进 SKILL.md：装完之后这个技能
	// 和市场就断了联系，插件页读的是本地目录，只留在缓存的清单里，缓存一清就没了。
	// 包自己带了对应的键就不动（作者写在文件里的比目录行更权威）。
	//
	// 描述写的是 display-description，**不覆盖** frontmatter 里的 description：后者
	// 是给模型判断何时加载用的，动它会影响技能什么时候被触发。
	if err := setSkillDisplayMeta(staging, detail.DisplayName, detail.Description); err != nil {
		return SkillList{}, wireError(fmt.Errorf("写入技能展示信息失败: %w", err))
	}
	dest := filepath.Join(root, detail.Name)
	if err := os.RemoveAll(dest); err != nil {
		return SkillList{}, wireError(fmt.Errorf("替换已安装的技能失败: %w", err))
	}
	if err := os.Rename(staging, dest); err != nil {
		return SkillList{}, wireError(fmt.Errorf("安装技能失败: %w", err))
	}
	a.reloadSessionSkills()
	step(protocol.SkillInstallDone, detail.Name, 0, 0)
	// 装完清一次缓存的「已安装」视图：下次开页面立刻是对的。
	return a.ListSkills(), nil
}

// writePromptSkill 为纯提示词型技能生成 SKILL.md：frontmatter + system_prompt 正文。
//
// 上游的 system_prompt 就是 SKILL.md 去掉 frontmatter 之后的正文，所以这里把
// frontmatter 补回去即可——本地技能加载器认的是同一份格式。
func writePromptSkill(dir string, s marketSkillWire) error {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: " + s.Name + "\n")
	if d := strings.TrimSpace(collapseLine(s.Description)); d != "" {
		b.WriteString("description: " + d + "\n")
	}
	if v := strings.TrimSpace(s.Version); v != "" {
		b.WriteString("version: " + v + "\n")
	}
	if len(s.AllowedTools) > 0 {
		b.WriteString("allowed-tools: " + strings.Join(s.AllowedTools, ", ") + "\n")
	}
	b.WriteString("---\n\n")
	b.WriteString(strings.TrimSpace(s.SystemPrompt))
	b.WriteString("\n")
	return os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(b.String()), 0o600)
}

// downloadSkillBundle 取预签名直链、下载、校验 sha256、解压进 dir。
//
// 直链不经过服务转发（一个几十 MB 的包走 FastAPI 会把 worker 占满），所以这一趟
// **不带**鉴权头——带上反而可能被对象存储拒。
func (a *App) downloadSkillBundle(id int, dir string, step func(stage string, received, total int64)) error {
	body, err := a.marketGet("/skills/"+strconv.Itoa(id)+"/download?expires="+strconv.Itoa(skillDownloadTTL), 20*time.Second)
	if err != nil {
		// 这一步失败绝大多数是服务端的事，说清楚免得用户在本机反复重试：预签名链接
		// 由 lea-config 现算，它依赖对象存储的对外地址（部署时要配 OBS_PUBLIC_ENDPOINT
		// 覆盖集群内地址）。实测这个环境上 64 个技能的 /download 全是 500。
		return fmt.Errorf("平台的下载接口没能给出链接，装不了这个技能：%w（这是服务端的问题，本机重试没用）", err)
	}
	var link marketDownloadWire
	if err := json.Unmarshal(body, &link); err != nil {
		return fmt.Errorf("解析下载链接失败: %w", err)
	}
	if strings.TrimSpace(link.URL) == "" {
		return errors.New("技能市场没有给出下载链接")
	}

	tmp, err := os.CreateTemp("", "runcode-skill-*.zip")
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	step(protocol.SkillInstallDownload, 0, 0)
	// 时长上限给在这里而不是下载器里：多久算「下不动了」是按下的东西定的，
	// 一个技能包和一个安装包不是一个量级。
	ctx, cancel := context.WithTimeout(context.Background(), skillDownloadTimeout)
	defer cancel()
	sum, size, err := fetchToFile(ctx, link.URL, "技能包", tmp, skillBundleMaxBytes, func(received, total int64) {
		step(protocol.SkillInstallDownload, received, total)
	})
	_ = tmp.Close()
	if err != nil {
		return err
	}
	step(protocol.SkillInstallVerify, 0, 0)
	// sha256 就是对象键里那一段，服务端和包一起给出来。对不上说明下到的不是那个包，
	// 别解压——解压一个来路不明的 zip 比不装糟得多。
	if want := strings.ToLower(strings.TrimSpace(link.SHA256)); want != "" && want != sum {
		return fmt.Errorf("技能包校验失败：期望 %s，实际 %s", want, sum)
	}
	step(protocol.SkillInstallExtract, 0, 0)
	return unzipSkill(tmpName, size, dir)
}

// unzipSkill 把 zip 里的技能解到 dir，并把目录层级**归一**成「SKILL.md 就在 dir 下」。
//
// 归一这一步不能省：上游接受 SKILL.md 在根目录或一级子目录两种打包方式，而本地
// 技能是按 skills/<name>/SKILL.md 加载的。照原样解开的话，套了一层目录的包会变成
// skills/<name>/<name>/SKILL.md——模型读不到，而且上传阶段验不出来、只在运行时显形。
// 所以这里认 SKILL.md 所在的那一层为包根，只解那一层往下的东西。
func unzipSkill(zipPath string, size int64, dir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("技能包不是有效的 zip: %w", err)
	}
	defer func() { _ = zr.Close() }()

	base, ok := skillBundleRoot(zr.File)
	if !ok {
		return errors.New("技能包里没有 SKILL.md（根目录或一级子目录都没有）")
	}

	// written 用 uint64 累加：zip 头里的 UncompressedSize64 是**包自己声明的**，
	// 一个恶意包可以把它填成接近 2^64——先转 int64 会溢出成负数，那道闸就白设了。
	var written uint64
	files := 0
	for _, f := range zr.File {
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if base != "" {
			if !strings.HasPrefix(name, base+"/") {
				continue // 包根之外的东西不要（同级还塞了别的目录时）
			}
			name = strings.TrimPrefix(name, base+"/")
		}
		if name == "." || name == "" {
			continue
		}
		// zip-slip：条目名里带 .. 或绝对路径能把文件写到目录外面去。
		rel := filepath.FromSlash(name)
		if strings.HasPrefix(name, "/") || strings.Contains(name, "..") || filepath.IsAbs(rel) {
			return fmt.Errorf("技能包里有非法路径：%s", f.Name)
		}
		target := filepath.Join(dir, rel)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if !f.Mode().IsRegular() {
			continue // 符号链接与特殊文件一律跳过
		}
		files++
		if files > skillBundleMaxFiles {
			return fmt.Errorf("技能包里的文件超过 %d 个，拒绝安装", skillBundleMaxFiles)
		}
		written += f.UncompressedSize64
		if written > uint64(skillBundleMaxBytes) {
			return fmt.Errorf("技能包解压后超过 %d MiB，拒绝安装", skillBundleMaxBytes>>20)
		}
		if err := extractZipFile(f, target); err != nil {
			return err
		}
	}
	_ = size
	return nil
}

// skillBundleRoot 找出包里 SKILL.md 所在的那一层目录（""=zip 根），只认根目录与
// 一级子目录两种——更深的层级上游本来也不接受。
func skillBundleRoot(files []*zip.File) (string, bool) {
	best, found := "", false
	for _, f := range files {
		if f.FileInfo().IsDir() {
			continue
		}
		name := path.Clean(strings.ReplaceAll(f.Name, "\\", "/"))
		if path.Base(name) != "SKILL.md" {
			continue
		}
		dir := path.Dir(name)
		if dir == "." {
			return "", true // 根目录的优先，不必再看
		}
		if !strings.Contains(dir, "/") && !found {
			best, found = dir, true
		}
	}
	return best, found
}

func extractZipFile(f *zip.File, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	//nolint:gosec // G110: 解压总量与文件数在调用方按 skillBundleMaxBytes/Files 已封顶
	_, err = io.Copy(out, rc)
	return err
}
