// Package ui is runcode 的终端交互界面(TUI):一个 bubbletea 模型,把会话服务
// (Service)的流式输出、工具事件与授权请求画成可滚动的对话记录。
//
// 文件分工:
//
//	model.go         Model 与 bubbletea 生命周期(Init/Update/按键/布局)
//	tool_events.go   工具事件 → ToolProgress 的归并与净化
//	tea_commands.go  异步动作的 tea.Cmd 工厂(发起回合、重置、压缩)
//	messages.go      模型/视图之间传递的消息类型与展示数据结构
//	slash_commands.go 斜杠命令注册表与内置命令
//	menu.go          输入斜杠命令时的候选菜单
//	approval.go      权限审批的状态机(Approver 与待决队列)
//	render.go        视图组装:对话区 + 底部固定行
//	render_approval.go 审批弹窗的渲染
//	render_tools.go  工具进度的汇总与渲染
//	markdown.go      助手回复的 Markdown 渲染与记忆化
//	format.go        状态行用到的短格式化函数
//	picker.go        独立的会话选择器(/resume)
//	history.go       输入历史
//	width.go         终端显示宽度(CJK 双宽)
//
// 本包不直接接触引擎:一切都经 Service 接口进出,因此可以用假实现完整测试。
package ui
