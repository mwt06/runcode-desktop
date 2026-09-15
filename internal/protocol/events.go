package protocol

// Event names the desktop shell emits on top of the ones the engine's host
// package streams during a turn. They are stable strings the UI subscribes to;
// payload types are documented alongside each constant.
const (
	// EventSessionRenamed carries a SessionRenamed when a turn's generated title is
	// ready, so the sidebar can refresh that session's name.
	EventSessionRenamed = "session:renamed"
	// EventHarmAutoAllow carries a HarmAutoAllow whenever judge ("smart") mode's
	// harm gate auto-allows a risky action without a prompt, or trips its
	// per-session breaker — so the user can review what smart mode decided.
	EventHarmAutoAllow = "harm:autoallow"
	// EventPassportChanged carries a PassportStatus whenever login state changes
	// (login success, logout, or refresh-token expiry forcing re-login).
	EventPassportChanged = "passport:changed"
	// EventPlanUpdated carries a PlanRun whenever the staged plan changes: a stage
	// the model just recorded, the user's edits, approval, or cancellation. It is
	// the single channel for plan state — the plan_write tool's own tool events are
	// hidden in the chat, so nothing else describes the current plan.
	EventPlanUpdated = "plan:updated"
	// EventSessionStatus carries a SessionInfo whenever the **backend on its own**
	// changes something the session header shows — today only the OA auto-switch to
	// a local model.
	//
	// 界面上那些开关(模型、计划模式、思考强度)平时都是"前端调后端 → 拿返回值更新
	// 自己",所以后端自作主张改掉的东西前端根本不知道。表现是:模型已经切了、请求
	// 也确实发给新模型了,右下角却还显示旧模型——用户会以为切换没生效。
	EventSessionStatus = "session:status"
	// EventOABlocked carries an OABlocked when the OA local-model gate stops a tool
	// call and the shell has scheduled the automatic switch.
	//
	// 工具结果本身仍是 IsError(调用确实没跑成,模型要据此停下来),但界面上不该是
	// 一个红色的"执行失败"——系统正在自动恢复,这一轮随后会被整个重跑。界面按
	// ToolUseID 精确改写那一张卡片,不靠匹配错误文案(措辞一改匹配就静默失效)。
	EventOABlocked = "oa:blocked"
)
