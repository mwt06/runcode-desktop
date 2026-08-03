package protocol

import hostproto "gitlab.ouc-online.com.cn/aibase/agentloop/protocol"

// 阶段化计划模式的 wire 契约。计划模式不再是"模型自由写一段方案"，而是一条有阶段、
// 有可编辑清单、有审批闸门的流水线：需求理解 → 方案设计 → 方案审查 → 用户审批。
// 前三个阶段由模型经桌面专属工具 plan_write 逐个记录（工具本身把关顺序），第四个
// 阶段不发模型——用户在这份文档上增删改排序，确认后才退出计划模式开始执行。
//
// 整份状态只经一个事件通道下发（EventPlanUpdated 携带 PlanRun），工具事件本身在前端
// 被过滤掉不进对话流——两条通道描述同一件事会让"当前计划是什么"变成需要对账的问题。

// PlanStage* 是流水线的三个模型阶段，也是 plan_write 的 stage 入参取值。
// 顺序固定：understanding → design → review，跳阶段由工具直接拒绝。
const (
	// PlanStageUnderstanding 需求理解：复述目标、边界、验收标准与不做什么。
	PlanStageUnderstanding = "understanding"
	// PlanStageDesign 方案设计：给出有序步骤清单（每步落到具体文件/动作）。
	PlanStageDesign = "design"
	// PlanStageReview 方案审查：以审查者视角复核设计稿，补风险、漏项与待确认问题，
	// 并给出修订后的最终清单。
	PlanStageReview = "review"
)

// PlanState* 是一次规划运行的生命周期状态。
const (
	// PlanStateIdle 没有进行中的规划（会话刚开、或上一次已执行/已取消后复位）。
	PlanStateIdle = "idle"
	// PlanStatePlanning 模型正在按阶段产出，Stage 指向最近完成的阶段。
	PlanStatePlanning = "planning"
	// PlanStateAwaitingApproval 三个阶段跑完，等用户编辑与确认。
	PlanStateAwaitingApproval = "awaiting_approval"
	// PlanStateExecuting 用户已确认，计划模式已退出，正在按清单执行。
	PlanStateExecuting = "executing"
	// PlanStateCancelled 用户取消了这次规划；文档保留供回看，下一轮重新开始。
	PlanStateCancelled = "cancelled"
)

// PlanStep 是清单里的一步。ID 由外壳分配并在编辑往返中保持稳定（前端拿它做 key，
// 后端拿它认"这步是不是新加的"）；模型不提供 ID，新步骤留空由后端补。
type PlanStep struct {
	ID     string   `json:"id"`
	Title  string   `json:"title"`
	Detail string   `json:"detail,omitempty"`
	Files  []string `json:"files,omitempty"`
}

// PlanDoc 是规划文档本身：三个阶段各自往里填一部分，用户在审批前还能改。
type PlanDoc struct {
	// Goal 是需求理解阶段的结论（目标与边界的复述）。
	Goal string `json:"goal,omitempty"`
	// NonGoals 是明确不做的部分——规划跑偏最常见的地方就是这里没说清。
	NonGoals []string `json:"nonGoals,omitempty"`
	// Title 是方案标题，设计阶段给出。
	Title string `json:"title,omitempty"`
	// Steps 是有序执行步骤；审查阶段可整体修订。
	Steps []PlanStep `json:"steps,omitempty"`
	// Risks 是已识别的风险与代价。
	Risks []string `json:"risks,omitempty"`
	// Questions 是需要用户拍板的开放问题（审批区会单独列出）。
	Questions []string `json:"questions,omitempty"`
	// ReviewNotes 是审查阶段的发现：漏项、更好的做法、被否掉的选项。
	ReviewNotes []string `json:"reviewNotes,omitempty"`
}

// PlanRun 是一次规划运行的完整对外状态，EventPlanUpdated 与 PlanStatus 都发它。
type PlanRun struct {
	State string `json:"state"`
	// Stage 是最近完成的阶段（State 为 planning 时即进度所在），idle 时为空。
	Stage string `json:"stage,omitempty"`
	// Doc 在模型产出第一段结论前为空。
	Doc *PlanDoc `json:"doc,omitempty"`
	// Edited 标记用户改过清单——审批后下发给模型的是用户这一版，不是模型那一版。
	Edited bool `json:"edited,omitempty"`
	// UpdatedAt 是最后一次变更时间（RFC3339），用于"恢复会话后这份计划有多旧"。
	UpdatedAt string `json:"updatedAt,omitempty"`
}

// PlanApproveRequest 是用户点"确认执行"时提交的东西：编辑后的最终清单，加上执行阶段
// 要切换到的权限模式（沿用原先那张三选一卡片的语义：交互 / 智能）。
type PlanApproveRequest struct {
	Doc            PlanDoc `json:"doc"`
	PermissionMode string  `json:"permissionMode"`
}

// PlanApproveResult 把审批的两件后果一起带回前端：会话状态已变（计划模式关、权限模式
// 切好），以及应当作为下一条消息发出去的执行指令。指令由后端拼是为了让措辞与清单编号
// 只有一个来源（也便于单测），发送仍走前端既有的 send 路径，这样 busy、用户气泡、回合
// 生命周期全部复用原有链路。
type PlanApproveResult struct {
	Info            hostproto.SessionInfo `json:"info"`
	ExecutionPrompt string                `json:"executionPrompt"`
}
