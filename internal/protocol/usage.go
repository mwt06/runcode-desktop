package protocol

// 用量统计（USAGE-001）的桌面 wire 契约，字段对应本机 SQLite 快照与前端展示。三条约定贯穿全部类型：
//
//   - 计数与 Token 聚合一律是十进制整数字符串：64 位整数在 JS 里超过 2^53 就丢精度，
//     前端用 BigInt 显示。字段为指针（TS 里 `string | null`）时，null 表示「未知 /
//     不可统计」，与字符串 "0" 含义不同，任何一层都不得把 null 补成 0。
//   - 比例是 0—1 的可空小数；分母为 0 或无可信样本时为 null，界面显示「—」。
//   - 不含任何成本、价格、额度、正文、Details 或密钥字段；身份与 scope 由桌面端根据当前登录账号、Bridge 和租户冻结，这里没有 userId / dependentId 字段。
//
// 失败经 protocol.Error 返回，Code 取下面的 ErrCodeUsage* 或通用的 not_logged_in /
// invalid_argument；界面按 code 区分未登录、无权、不支持、超时与暂不可用，绝不把
// 失败显示成零用量。

// 用量查询专属的错误码（与引擎的通用 ErrCode* 同属 ErrCodes 联合）。
const (
	// ErrCodeUsageForbidden：当前账号无权查看所选租户的用量（本机权限）。
	ErrCodeUsageForbidden = "usage_forbidden"
	// ErrCodeUsageInvalidRange：时间范围或筛选不符合本机查询契约。
	ErrCodeUsageInvalidRange = "usage_invalid_range"
	// ErrCodeUsageInvalidCursor：分页游标失效（快照过期或筛选变化），应回到第一页。
	ErrCodeUsageInvalidCursor = "usage_invalid_cursor"
	// ErrCodeUsageUnsupported：当前本机数据源 / 维度无法给出准确统计，不是零用量。
	ErrCodeUsageUnsupported = "usage_unsupported"
	// ErrCodeUsageTimeout：统计在时限内没有完成（本机命令时限内没有完成）。
	ErrCodeUsageTimeout = "usage_timeout"
	// ErrCodeUsageUnavailable：功能关闭、并发已满或本机库不可读。
	ErrCodeUsageUnavailable = "usage_unavailable"
	// ErrCodeUsageStorage：本机用量库损坏、版本不兼容或无法打开，需要用户确认重建。
	ErrCodeUsageStorage = "usage_storage"
	// ErrCodeUsageInvalidResponse：本机记录不符合契约，整份丢弃不展示。
	ErrCodeUsageInvalidResponse = "usage_invalid_response"
	// ErrCodeUsageIdentityChanged：请求期间登录账号已变化，旧结果作废。
	ErrCodeUsageIdentityChanged = "usage_identity_changed"
)

// UsageQuery 是总览查询。range 取 today / last24h / last7d / last14d / last30d /
// all / custom；custom 时 startAt 必填，endAt 与 followNow 二选一（followNow 只是
// 页面偏好：每次刷新由桌面端固定成读取时刻）。timezone 固定 Asia/Shanghai。
type UsageQuery struct {
	TenantID   string `json:"tenantId"`
	Range      string `json:"range"`
	StartAt    string `json:"startAt,omitempty"`
	EndAt      string `json:"endAt,omitempty"`
	FollowNow  bool   `json:"followNow"`
	Timezone   string `json:"timezone"`
	ProviderID string `json:"providerId,omitempty"`
	Model      string `json:"model,omitempty"`
}

// UsageEffectiveRange 是桌面端为一次查询固定下来的半开区间 [startAt, endAt)。
type UsageEffectiveRange struct {
	StartAt  string `json:"startAt"`
	EndAt    string `json:"endAt"`
	Timezone string `json:"timezone"`
}

// ResolvedUsageQuery 是总览返回范围之后，日志与分组共用的查询条件；翻页沿用它，
// 不再各自解析相对时间。
type ResolvedUsageQuery struct {
	TenantID       string              `json:"tenantId"`
	SnapshotSeq    string              `json:"snapshotSeq,omitempty"`
	EffectiveRange UsageEffectiveRange `json:"effectiveRange"`
	ProviderID     string              `json:"providerId,omitempty"`
	Model          string              `json:"model,omitempty"`
}

// UsageRequestsQuery 读取一页请求日志。第一页不带 cursor；pageSize 只能是 20/50/100。
type UsageRequestsQuery struct {
	Query    ResolvedUsageQuery `json:"query"`
	Cursor   string             `json:"cursor,omitempty"`
	PageSize int                `json:"pageSize"`
}

// UsageDimensionsQuery 读取按模型或供应商分组的汇总（groupBy = model / provider）。
// pageSize 只能是 50/100，page 从 1 开始。
type UsageDimensionsQuery struct {
	Query    ResolvedUsageQuery `json:"query"`
	GroupBy  string             `json:"groupBy"`
	Search   string             `json:"search,omitempty"`
	Page     int                `json:"page"`
	PageSize int                `json:"pageSize"`
}

// UsageQuality 描述一组统计的口径覆盖：tokenCoverage / cacheCoverage 取
// complete / partial / none；containsEstimates 表示合计含本地近似缓存（非供应商精确命中）；
// legacy 标记说明合计里含无法确认来源的历史口径。
type UsageQuality struct {
	TokenCoverage               string `json:"tokenCoverage"`
	CacheCoverage               string `json:"cacheCoverage"`
	ContainsEstimates           bool   `json:"containsEstimates"`
	ContainsLegacyUnknown       bool   `json:"containsLegacyUnknown"`
	ContainsLegacyInferredCache bool   `json:"containsLegacyInferredCache"`
	InvalidRecordCount          string `json:"invalidRecordCount"`
}

// UsageTotals 是一组记录的聚合。tokenRequestCount 是全部已结束的 LLM/识图调用数，
// sampleCount 是其中有可信数值的样本数；totalTokens 汇总所有已知输入与输出。
// totalTokens = inputTokens + outputTokens（缓存是输入
// 子集、思考是输出子集，都不再相加）；缓存三项只统计可信缓存子集，命中率是
// SUM(命中)/SUM(可信输入)。
type UsageTotals struct {
	RequestCount              string       `json:"requestCount"`
	TokenRequestCount         string       `json:"tokenRequestCount"`
	InputTokenSampleCount     string       `json:"inputTokenSampleCount,omitempty"`
	OutputTokenSampleCount    string       `json:"outputTokenSampleCount,omitempty"`
	TotalTokenSampleCount     string       `json:"totalTokenSampleCount,omitempty"`
	InputTokens               *string      `json:"inputTokens"`
	OutputTokens              *string      `json:"outputTokens"`
	TotalTokens               *string      `json:"totalTokens"`
	CacheHitTokens            *string      `json:"cacheHitTokens"`
	CacheMissTokens           *string      `json:"cacheMissTokens"`
	CacheEligibleInputTokens  *string      `json:"cacheEligibleInputTokens"`
	CacheHitRate              *float64     `json:"cacheHitRate"`
	CacheKnownRequestCount    string       `json:"cacheKnownRequestCount"`
	CacheEligibleRequestCount string       `json:"cacheEligibleRequestCount"`
	ReasoningTokens           *string      `json:"reasoningTokens"`
	DurationSampleCount       string       `json:"durationSampleCount"`
	AverageDurationMs         *float64     `json:"averageDurationMs"`
	Quality                   UsageQuality `json:"quality"`
}

// UsageBucket 是趋势里的一个时间桶（首尾可能是被裁剪的部分桶）。coverage 取
// known（已覆盖，没有记录就是真 0）/ gap（有证据的采集缺口）/ unknown（覆盖未知）。
type UsageBucket struct {
	StartAt                  string  `json:"startAt"`
	EndAt                    string  `json:"endAt"`
	RequestCount             string  `json:"requestCount"`
	InputTokens              *string `json:"inputTokens"`
	OutputTokens             *string `json:"outputTokens"`
	TotalTokens              *string `json:"totalTokens"`
	CacheHitTokens           *string `json:"cacheHitTokens"`
	CacheEligibleInputTokens *string `json:"cacheEligibleInputTokens"`
	Coverage                 string  `json:"coverage"`
}

// UsageCapabilities 声明本次数据能可靠支持哪些维度；本机记录支持请求状态，TTFT 未采集。
type UsageCapabilities struct {
	ProviderBreakdown bool `json:"providerBreakdown"`
	CacheMetrics      bool `json:"cacheMetrics"`
	RequestStatus     bool `json:"requestStatus"`
	TTFT              bool `json:"ttft"`
}

// UsageCoverageGap 是一段有证据的采集缺口。
type UsageCoverageGap struct {
	StartAt string `json:"startAt"`
	EndAt   string `json:"endAt"`
	Reason  string `json:"reason,omitempty"`
}

// UsageStatusCounts 保留在途与五种终态的独立计数，pending 不进入已完成请求数。
type UsageStatusCounts struct {
	Pending    string `json:"pending"`
	Succeeded  string `json:"succeeded"`
	Failed     string `json:"failed"`
	Cancelled  string `json:"cancelled"`
	Unknown    string `json:"unknown"`
	SaveFailed string `json:"saveFailed"`
}

// UsageOverview 是同一次本机快照读取到的汇总、趋势与质量。dataAsOf 是本机读取时刻；
// local_platform 来源不使用 replicaLagSeconds。
type UsageOverview struct {
	ScopeID             string              `json:"scopeId"`
	SnapshotSeq         string              `json:"snapshotSeq,omitempty"`
	CollectionStartedAt *string             `json:"collectionStartedAt,omitempty"`
	StorageStatus       string              `json:"storageStatus,omitempty"`
	StatusCounts        *UsageStatusCounts  `json:"statusCounts,omitempty"`
	QueryKey            string              `json:"queryKey"`
	EffectiveRange      UsageEffectiveRange `json:"effectiveRange"`
	DataAsOf            string              `json:"dataAsOf"`
	Summary             UsageTotals         `json:"summary"`
	Trend               []UsageBucket       `json:"trend"`
	Capabilities        UsageCapabilities   `json:"capabilities"`
	Empty               bool                `json:"empty"`
	CoverageStart       *string             `json:"coverageStart"`
	CoverageGaps        []UsageCoverageGap  `json:"coverageGaps"`
	CoverageStatus      string              `json:"coverageStatus"`
	CoverageMessage     string              `json:"coverageMessage,omitempty"`
	// SourceKind 首期固定为 local_platform；兼容字段仍允许其他只读来源。
	SourceKind        string `json:"sourceKind,omitempty"`
	ReplicaLagSeconds *int64 `json:"replicaLagSeconds"`
	Consistency       string `json:"consistency,omitempty"`
	MappingVersion    string `json:"mappingVersion,omitempty"`
}

// UsageRowQuality 是单条记录各项用量的来源口径（reported / estimated / derived /
// legacyUnknown / legacyInferred / unavailable）。
type UsageRowQuality struct {
	TokenSource     string `json:"tokenSource"`
	CacheSource     string `json:"cacheSource"`
	ReasoningSource string `json:"reasoningSource"`
}

// UsageRequestRow 是一条已入账的消费记录，只有计量与口径，没有正文或成本。
// meterKind 不是 tokens 时 Token 各列为 null，用量见 usageQuantity + usageUnit。
type UsageRequestRow struct {
	ID              string          `json:"id"`
	Status          string          `json:"status,omitempty"`
	RecordedAt      string          `json:"recordedAt"`
	StartedAt       *string         `json:"startedAt"`
	Model           string          `json:"model"`
	ProviderID      *string         `json:"providerId"`
	ProviderName    *string         `json:"providerName"`
	MeterKind       string          `json:"meterKind"`
	UsageQuantity   *string         `json:"usageQuantity"`
	UsageUnit       *string         `json:"usageUnit"`
	InputTokens     *string         `json:"inputTokens"`
	OutputTokens    *string         `json:"outputTokens"`
	CacheHitTokens  *string         `json:"cacheHitTokens"`
	CacheMissTokens *string         `json:"cacheMissTokens"`
	ReasoningTokens *string         `json:"reasoningTokens"`
	DurationMs      *int64          `json:"durationMs"`
	Quality         UsageRowQuality `json:"quality"`
}

// UsageRequestsPage 是一页请求日志；totalCount 与 nextCursor 绑定同一个本机快照水位、范围与筛选。
type UsageRequestsPage struct {
	Items          []UsageRequestRow   `json:"items"`
	TotalCount     string              `json:"totalCount"`
	PageSize       int                 `json:"pageSize"`
	NextCursor     string              `json:"nextCursor,omitempty"`
	HasMore        bool                `json:"hasMore"`
	EffectiveRange UsageEffectiveRange `json:"effectiveRange"`
	QueryKey       string              `json:"queryKey"`
}

// UsageDimensionRow 是一个模型或供应商的汇总；供应商无法确定时归入 unknown 分组。
type UsageDimensionRow struct {
	ID     string      `json:"id"`
	Name   string      `json:"name"`
	Totals UsageTotals `json:"totals"`
}

// UsageDimensionsPage 是一页分组汇总，按请求数降序、ID 稳定排序。
type UsageDimensionsPage struct {
	Items          []UsageDimensionRow `json:"items"`
	GroupBy        string              `json:"groupBy"`
	Page           int                 `json:"page"`
	PageSize       int                 `json:"pageSize"`
	TotalCount     string              `json:"totalCount"`
	HasMore        bool                `json:"hasMore"`
	EffectiveRange UsageEffectiveRange `json:"effectiveRange"`
	QueryKey       string              `json:"queryKey"`
}
