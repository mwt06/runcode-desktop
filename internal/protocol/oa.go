package protocol

// OABlocked 说明某一次工具调用被"OA 数据只进本地模型"的闸门拦下了，而且外壳已经
// 排好自动切换。
//
// 它只为界面呈现而生：那次调用在模型眼里是失败的（工具结果带 IsError，模型要据此
// 停下来），但对用户不是——系统正在自动恢复，这一轮随后会被本地模型整个重跑。
// 界面据此把那张工具卡从红色的"执行失败"改写成中性的"已阻止 · 正在切换"。
type OABlocked struct {
	// ToolUseID 定位是哪一次调用。用 id 而不是让界面去匹配错误文案：措辞改一次，
	// 匹配就静默失效，而失效的表现只是界面退回报错，没人会注意到。
	ToolUseID string `json:"toolUseId"`
	// LocalModel 是即将切过去的本地模型，界面可以直接说出它的名字。
	LocalModel string `json:"localModel"`
}
