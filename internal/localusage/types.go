package localusage

import (
	"context"
	"time"
)

// SchemaVersion 是本机用量库的当前 schema；打开时若库版本更新则拒绝，不得静默删库。
const SchemaVersion = 3

// SourceKindLocalPlatform 是本机采集的固定来源标记（与 Bridge 读库口径区分）。
const SourceKindLocalPlatform = "local_platform"

// 调用种类：一次底层平台调用对应一条 start／至多一条 finish。
const (
	CallKindLLM    = "llm"
	CallKindVision = "vision"
	CallKindImage  = "image"
	CallKindSearch = "search"
)

// 结果状态六类互斥；pending 仅表示当前进程在途。
const (
	StatusPending    = "pending"
	StatusSucceeded  = "succeeded"
	StatusFailed     = "failed"
	StatusCancelled  = "cancelled"
	StatusUnknown    = "unknown"
	StatusSaveFailed = "save_failed"
)

// Scope 是一条调用的冻结归属。稳定账号摘要与 Bridge 摘要不含凭据；
// 不用 loginEpoch／access token 作永久主键。
type Scope struct {
	ScopeID       string // 稳定哈希：issuer+subject+bridge+tenant
	AccountDigest string // issuer+subject 摘要
	BridgeDigest  string // 规范化 Bridge 基址摘要
	TenantID      string
}

// StartRecord 是调用开始事件；callId 全局唯一，同一 callId 的 scope 不可更换。
type StartRecord struct {
	CallID       string
	Scope        Scope
	Model        string
	CallKind     string
	SessionID    string // 可选安全会话标识，可空
	TurnID       string // 可空
	GenerationID string // 生图等复用既有 generationID
	StartedAt    time.Time
}

// FinishRecord 是调用终态。不得覆盖已落盘的先前终态；矛盾终态记诊断。
// FrozenStart 供 finish 事务必要时补写缺失的 start（同一 callId）。
type FinishRecord struct {
	CallID               string
	Status               string
	FinishedAt           time.Time
	DurationMs           *int64
	InputTokens          *int64
	InputTokensKnown     bool
	OutputTokens         *int64
	OutputTokensKnown    bool
	CacheHitTokens       *int64
	CacheHitTokensKnown  bool
	CacheMissTokens      *int64
	CacheMissTokensKnown bool
	GeneratedImages      *int64
	TokenSource          string // reported / unavailable
	ErrorCode            string // 仅安全短码，无原文异常
	FrozenStart          StartRecord
}

// Recorder 是采集边界的窄接口；Store 实现它，测试可注入 fake。
type Recorder interface {
	Begin(ctx context.Context, start StartRecord) error
	Finish(ctx context.Context, finish FinishRecord) error
	Close() error
}
