package protocol

// MarketSkill 是技能市场（lea-config / lea-gateway）里的一条。
//
// 与本包里其它 DTO 一样，它是**桌面自己的线上形状**，不是上游 API 的原样转发：
// 上游返回 snake_case 且带一堆界面用不到的列（is_builtin / can_edit /
// pending_publish / system_prompt 全文 …），照抄进前端只会让市场页跟着上游的表结构
// 走。这里只留画得出来、装得上的那几个字段，上游加列不必动前端。
type MarketSkill struct {
	// ID 是市场里的主键。安装按 id 取详情与下载链接——上游明确说绑定/寻址按 id，
	// 不按 name（同名可以跨租户存在）。
	ID int `json:"id"`
	// Name 是 kebab-case 的真实身份，来自 SKILL.md 的 frontmatter，也是安装到本地
	// 之后的目录名。它决定「装没装过」怎么判定。
	Name string `json:"name"`
	// DisplayName 是展示名（这个市场里是中文）。空则退回 Name。
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	// Category 是市场页顶部那排分类页签的来源，缺省 general。
	Category string `json:"category"`
	Version  string `json:"version"`
	// AllowedTools 是这个技能声明需要的工具，装之前让人看一眼。
	AllowedTools []string `json:"allowedTools"`
	// HasBundle 为 false 表示纯提示词型：没有 zip 可下，正文就是 system_prompt。
	// 两条安装路径由它分流。
	HasBundle bool `json:"hasBundle"`
	// InstalledUser / InstalledProject 表示本机的哪个作用域下已经躺着同名技能。
	//
	// 分两栏而不是一个布尔：安装要选装到哪儿（全局 or 本项目），卸载也得知道删的是
	// 哪一份——两个作用域可以同时装着同名技能（用户级遮住项目级），一个布尔说不清。
	// 它们由本地目录算出来，不是上游的字段：上游不知道这台机器上装了什么。
	InstalledUser    bool `json:"installedUser"`
	InstalledProject bool `json:"installedProject"`
}

// 安装技能的几个阶段。名字进 wire，前端按它显示"正在做什么"，所以是稳定字符串。
//
// 分阶段而不是只给一个百分比：这条链路里真正会卡住的地方各不相同——取详情卡是
// 网关慢，下载卡是对象存储慢或包大，解压卡是文件多。只给一根进度条的话，卡住时
// 没人知道该去查哪一头。
const (
	SkillInstallDetail   = "detail"   // 取技能详情
	SkillInstallDownload = "download" // 下载技能包（带字节进度）
	SkillInstallVerify   = "verify"   // 校验 sha256
	SkillInstallExtract  = "extract"  // 解压并归位
	SkillInstallDone     = "done"     // 装完
)

// EventSkillInstall carries a SkillInstallProgress while InstallMarketSkill runs.
const EventSkillInstall = "skill:install"

// SkillInstallProgress 是一次安装的实时进度。
//
// 它报的是**真的发生了什么**，不是按定时器推的假进度：Stage 由各步骤自己发，
// Received/Total 来自 HTTP 响应体的实际字节数与 Content-Length。假进度条在快的
// 时候显得慢、在卡住的时候还在动，等于把唯一能判断"是不是死了"的信号也抹掉。
type SkillInstallProgress struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Stage 是上面那五个常量之一。
	Stage string `json:"stage"`
	// Received / Total 只在 download 阶段有意义，单位字节。
	// Total 为 0 表示服务端没给 Content-Length——这时进度条只能画成不确定态。
	Received int64 `json:"received"`
	Total    int64 `json:"total"`
}

// SkillMarketPage 是市场页一次要画的全部东西。
//
// Categories 由这一批技能的 category 去重得来，而不是另开一个接口：上游没有分类
// 接口，分类本来就只是技能表里的一列，从数据里长出来的页签永远和内容对得上。
type SkillMarketPage struct {
	Skills     []MarketSkill `json:"skills"`
	Categories []string      `json:"categories"`
	Total      int           `json:"total"`
	// FetchedAt 是这份清单的抓取时刻（RFC3339），界面据此显示「刚刚更新」。
	FetchedAt string `json:"fetchedAt"`
}
