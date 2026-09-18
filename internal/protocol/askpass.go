package protocol

// 提权密码框（sudo 的 askpass）——桌面外壳自己的功能。
//
// 模型要跑 sudo 时有两道人工关：先在应用里批准那条命令，再在应用弹出的密码框里
// 输入系统密码。密码只经过"用户的键盘 → 本应用 → sudo"这一条路，**模型永远看不到**
// ——它进不了工具输出、事件流或任何日志。
//
// 密码框里显示的命令取自 sudo 进程自己的命令行（/proc），而不是审批时那段文本：
// 两者本该一致，但万一有一条 sudo 绕开了审批，用户在输密码之前看到的仍是它**真正**
// 要执行的东西。

// EventAskpassRequest asks the user for their password on behalf of a sudo run.
const EventAskpassRequest = "askpass:request"

// EventAskpassDone retracts a pending password request (answered, cancelled, or
// timed out), so every open dialog for it closes.
const EventAskpassDone = "askpass:done"

// AskpassRequest 是一次"sudo 要密码"。
type AskpassRequest struct {
	// ID 是这一次请求的标识，回答时原样带回。
	ID string `json:"id"`
	// SessionID 是发起它的会话。
	SessionID string `json:"sessionId"`
	// Command 是 sudo **实际要执行**的命令行（取自 sudo 进程本身），弹框里原样显示。
	Command string `json:"command"`
	// Prompt 是 sudo 给的提示语（"[sudo] password for jybzd:"），仅作参考。
	Prompt string `json:"prompt"`
	// Purpose 只在**应用自己**发起的提权里有值（比如给刚装的运行时加白名单），是一句
	// 给人看的用途说明。模型发起的 sudo 没有它：那种情况下命令本身就是全部信息，
	// 替模型编一句"用途"反而会让用户少看一眼命令。
	Purpose string `json:"purpose,omitempty"`
}

// AskpassDone 撤回一次请求。
type AskpassDone struct {
	ID string `json:"id"`
}
