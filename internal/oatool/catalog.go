package oatool

import "gitlab.ouc-online.com.cn/aibase/agentloop/tool"

// OA 只读工具目录。
//
// 一条 descriptor 同时决定三件事:模型看到的工具名与说明、发给服务端的 OA 工具名、
// 以及参数 schema。加一条能力就加一条记录,不必再写一个 Go 类型。
//
// # 为什么工具名要带 oa_ 前缀
//
// 服务端那套名字是 my_todo / search_docs / doc_content —— 太通用。会话内工具名
// **唯一**,撞名不是覆盖而是**装配直接失败**,而这些名字很容易和技能、MCP 服务器、
// 将来的内置工具撞上。前缀是本仓自己加的,remote 字段保留服务端原名,所以服务端
// 一行都不用改。
//
// # 为什么说明文案是中文、且逐字沿用服务端那份
//
// 工具说明是**模型行为的直接输入**,不是文档。这批文案已经在 OA MCP 版上调过
// (例如 oa_colleague_contact 特意写明"只填姓名本身,不要带'的座机'等后缀词",
// 那是真实的调用错误换来的)。重写等于把调好的提示词丢掉重来。
type descriptor struct {
	// name 是模型看到的工具名,也是权限归类与关闭清单的寻址键。
	name string
	// remote 是服务端 OA 工具名(POST /v1/oa/invoke 的 tool 字段)。
	remote string
	desc   string
	args   []argSpec
}

type argSpec struct {
	name     string
	desc     string
	required bool
}

// catalog 是全部 15 条只读能力。
//
// required 的判断与服务端略有出入,是有意的:服务端把参数全设成可选、在 handler 里
// 回一句"请提供流程ID"。那等于用一整个工具往返换一条本可以写在 schema 里的约束。
// 确实非填不可的(流程/文档 id、要查的人名)在这里标成 required,留空有意义的
// (搜索关键词、文档栏目、通讯录范围)保持可选。
var catalog = []descriptor{
	{name: "oa_todo", remote: "my_todo", desc: "查询【我】的待办流程"},
	{name: "oa_done", remote: "my_done", desc: "查询【我】的已办流程"},
	{name: "oa_created", remote: "my_created", desc: "查询【我】发起的流程"},
	{name: "oa_toread", remote: "my_toread", desc: "查询【我】的待阅流程"},
	{name: "oa_processed", remote: "my_processed", desc: "查询【我】的办结流程"},
	{
		name: "oa_request_detail", remote: "request_detail",
		desc: "查看某流程的发起人/审批过程/当前节点(谁审的、走到哪)",
		args: []argSpec{{name: "requestId", required: true,
			desc: "流程ID（来自你的待办/已办/发起等列表，或用户直接给出的流程号）"}},
	},
	{
		name: "oa_request_content", remote: "request_content",
		desc: "查看【某一个】流程的表单实际填写内容(任务名称/任务描述/所属项目/联系人等)",
		args: []argSpec{{name: "requestId", required: true,
			desc: "流程ID（来自你的待办/已办/发起等列表，或用户直接给出的流程号）"}},
	},
	{name: "oa_profile", remote: "my_profile", desc: "查询【我】的名片(姓名/部门/座机/手机/邮箱)"},
	{
		name: "oa_colleague_contact", remote: "colleague_contact",
		desc: "按姓名查【同事】的联系方式(座机/手机/办公室/邮箱/部门)",
		args: []argSpec{{name: "name", required: true,
			desc: "同事的姓名，只填姓名本身（如“杨亚菲”），不要带“的座机/的电话/联系方式”等后缀词"}},
	},
	{
		name: "oa_search_docs", remote: "search_docs",
		desc: "按关键词搜索文档/规章制度/通知公告，返回文档列表",
		args: []argSpec{{name: "keyword", desc: "搜索关键词（如“制度”“通知”），可留空搜最近文档"}},
	},
	{
		name: "oa_doc_content", remote: "doc_content",
		desc: "读取某个文档的正文内容（扫描件公文会用视觉模型识别）",
		args: []argSpec{{name: "docId", required: true,
			desc: "文档ID（来自 oa_search_docs / oa_browse_docs 的结果，或用户直接给出的）"}},
	},
	{
		name: "oa_browse_docs", remote: "browse_docs",
		desc: "按栏目浏览文档(通知公告/规章制度/科级干部选任/党群园地/督办任务等);留空列出所有栏目",
		args: []argSpec{{name: "category", desc: "文档栏目名(如“党群园地”“科级干部选任”“通知公告”)，留空则列出所有栏目"}},
	},
	{
		name: "oa_team_contacts", remote: "team_contacts",
		desc: "列出【同部门同事】或【我的下属】的联系方式",
		args: []argSpec{{name: "scope", desc: "范围：填“同部门”或“下属”，默认同部门"}},
	},
	{name: "oa_messages", remote: "my_messages", desc: "查询【我】消息中心的待阅提醒(待阅文档/通知公告等，含标题/创建人/时间)"},
	{
		name: "oa_search_people", remote: "search_people",
		desc: "按姓氏或姓名片段【搜索全校人员】名单(列出姓名+部门+工号；这是允许的通讯录查询)。查某人的座机/手机等联系方式仍用 oa_colleague_contact",
		args: []argSpec{{name: "keyword", required: true, desc: "姓氏或姓名片段，如“王”“张伟”"}},
	},
}

// Names 返回全部 OA 工具名。宿主用它做权限归类(hostToolClasses)与界面目录,
// 单独维护一份名单必然漂移。
func Names() []string {
	out := make([]string, 0, len(catalog))
	for _, d := range catalog {
		out = append(out, d.name)
	}
	return out
}

// schema 把 args 拼成工具的入参 schema。
//
// **身份不在这里**:没有 userid 之类的参数,一个都没有。调用者是谁只由请求头里的
// 令牌决定,模型既改不了也看不见——这是整条链路的安全基础,不是实现细节。
func (d descriptor) schema() tool.Schema {
	props := make(map[string]tool.Schema, len(d.args))
	var required []string
	for _, a := range d.args {
		props[a.name] = tool.Schema{Type: tool.SchemaTypeString, Description: a.desc}
		if a.required {
			required = append(required, a.name)
		}
	}
	return tool.Schema{
		Type:                 tool.SchemaTypeObject,
		Properties:           props,
		Required:             required,
		AdditionalProperties: false,
	}
}
