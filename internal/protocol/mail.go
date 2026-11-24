package protocol

// 邮箱账号设置的公共契约（接入向导 / 设置小节）。密文只经请求进入，
// 响应与 SettingsView 永不携带密码或 PasswordProtected。

// MailAccountVersion 是阿里企业邮箱服务预设。
const (
	MailVersionStandard = "standard" // smtp.qiye.aliyun.com
	MailVersionXC       = "xc"       // smtp.xc.aliyun.com（国产化）
	MailVersionHK       = "hk"       // smtphk.qiye.aliyun.com
)

// MailOnboardingStatus 是接入进度的事实状态，不是单一 verified 布尔值。
const (
	MailStatusNotConfigured        = "not_configured"
	MailStatusGuiding              = "guiding"
	MailStatusConnectionTestPassed = "connection_test_passed"
	MailStatusSaved                = "saved"
	MailStatusTestMailAccepted     = "test_mail_accepted"
	MailStatusReceiptConfirmed     = "receipt_confirmed"
	MailStatusNeedsAttention       = "needs_attention"
)

// MailSettings 是设置页加载的脱敏视图。
type MailSettings struct {
	Account *MailAccountView `json:"account,omitempty"`
	// SecretStorageOK 复用系统钥匙串探测结论，前端据此禁用“安全保存”。
	SecretStorageOK bool `json:"secretStorageOk"`
}

// MailAccountView 是已保存账号的公开视图；无密码字段。
type MailAccountView struct {
	ID                   string `json:"id"`
	Address              string `json:"address"`
	DisplayName          string `json:"displayName"`
	Version              string `json:"version"`
	Host                 string `json:"host"`
	Port                 int    `json:"port"`
	Enabled              bool   `json:"enabled"`
	HasPassword          bool   `json:"hasPassword"`
	Revision             int64  `json:"revision"`
	CredentialRevision   int64  `json:"credentialRevision"`
	OnboardingStatus     string `json:"onboardingStatus"`
	LastConnectionTestAt string `json:"lastConnectionTestAt,omitempty"`
	TestMailAcceptedAt   string `json:"testMailAcceptedAt,omitempty"`
	ReceiptConfirmedAt   string `json:"receiptConfirmedAt,omitempty"`
	LastTestShortID      string `json:"lastTestShortId,omitempty"`
	LastTestSubmissionID string `json:"lastTestSubmissionId,omitempty"`
}

// SaveMailAccountRequest 保存当前表单。Password 留空保留旧值；ClearPassword 才清空。
// ConfigFingerprint 必须来自同配置的 TestMailConnection 成功结果。
type SaveMailAccountRequest struct {
	Address           string `json:"address"`
	DisplayName       string `json:"displayName"`
	Version           string `json:"version"`
	Password          string `json:"password,omitempty"`
	ClearPassword     bool   `json:"clearPassword,omitempty"`
	ConfigFingerprint string `json:"configFingerprint"`
	ExpectedRevision  int64  `json:"expectedRevision"`
	Enabled           bool   `json:"enabled"`
}

// DisableMailAccountRequest 禁用已保存账号（保留配置，停止发信）。
type DisableMailAccountRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

// RemoveMailAccountRequest 删除已保存账号与密文。
type RemoveMailAccountRequest struct {
	ExpectedRevision int64 `json:"expectedRevision"`
}

// TestMailConnectionRequest 使用当前表单值探测 SMTP，不落盘、不发信。
type TestMailConnectionRequest struct {
	Address  string `json:"address"`
	Password string `json:"password"`
	Version  string `json:"version"`
}

// MailConnectionTestResult 是连接测试结果；成功时带 ConfigFingerprint 供保存校验。
type MailConnectionTestResult struct {
	OK                bool   `json:"ok"`
	Phase             string `json:"phase,omitempty"` // connecting|tls|auth|done
	TestedAtUTC       string `json:"testedAtUtc,omitempty"`
	ConfigFingerprint string `json:"configFingerprint,omitempty"`
	ErrorCategory     string `json:"errorCategory,omitempty"`
	Message           string `json:"message"`
}

// SendMailTestToSelfRequest 向已保存账号自身发固定测试信；无收件人字段。
type SendMailTestToSelfRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	ClientRequestID  string `json:"clientRequestId,omitempty"` // 防双击幂等
}

// MailTestSendResult 是本人测试发信结果；Accepted 只表示 SMTP 接受，不是收信核实。
type MailTestSendResult struct {
	Status        string `json:"status"` // accepted|failed|unknown
	ShortID       string `json:"shortId,omitempty"`
	SubmissionID  string `json:"submissionId,omitempty"`
	AcceptedAt    string `json:"acceptedAt,omitempty"`
	Message       string `json:"message"`
	ErrorCategory string `json:"errorCategory,omitempty"`
}

// ConfirmMailTestReceiptRequest 记录本人在网页核实收到测试信。
type ConfirmMailTestReceiptRequest struct {
	ExpectedRevision int64  `json:"expectedRevision"`
	ShortID          string `json:"shortId"`
}

// OpenMailOfficialURLRequest 只打开固定官方 URL，不接受任意地址。
type OpenMailOfficialURLRequest struct {
	Page string `json:"page"` // webmail_standard|webmail_xc|help_security_password
}

// ---- 通信录 / 草稿业务页（更多 → 邮箱与通信录）----

// MailAddressView 是可编辑地址芯片。
type MailAddressView struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// MailContactView 是通信录条目。
type MailContactView struct {
	ID           string   `json:"id"`
	Revision     int64    `json:"revision"`
	Name         string   `json:"name"`
	Emails       []string `json:"emails"`
	CreatedAtUTC string   `json:"createdAtUtc"`
	UpdatedAtUTC string   `json:"updatedAtUtc"`
}

// MailDraftView 是草稿编辑视图；含模型提案原文供对照，预览用用户修订字段。
type MailDraftView struct {
	ID             string            `json:"id"`
	Revision       int64             `json:"revision"`
	Status         string            `json:"status"`
	AccountID      string            `json:"accountId"`
	Subject        string            `json:"subject"`
	BodyMarkdown   string            `json:"bodyMarkdown"`
	To             []MailAddressView `json:"to"`
	Cc             []MailAddressView `json:"cc"`
	Bcc            []MailAddressView `json:"bcc"`
	SendMode       string            `json:"sendMode"`
	DispatchMode   string            `json:"dispatchMode"`
	ScheduledLocal string            `json:"scheduledLocal,omitempty"`
	TimeZone       string            `json:"timeZone,omitempty"`
	ProposalMD     string            `json:"proposalMd,omitempty"`
	UpdatedAtUTC   string            `json:"updatedAtUtc"`
	CreatedAtUTC   string            `json:"createdAtUtc"`
}

// ListMailContactsRequest 搜索/分页通信录。
type ListMailContactsRequest struct {
	Query  string `json:"query,omitempty"`
	Limit  int    `json:"limit,omitempty"`
	Offset int    `json:"offset,omitempty"`
}

// SaveMailContactRequest 新建或 CAS 更新联系人。
type SaveMailContactRequest struct {
	ID               string   `json:"id,omitempty"`
	Name             string   `json:"name"`
	Emails           []string `json:"emails"`
	ExpectedRevision int64    `json:"expectedRevision"`
}

// DeleteMailContactRequest 删除联系人。
type DeleteMailContactRequest struct {
	ID               string `json:"id"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

// MailContactImportRow 是 CSV 预览/提交的一行。
type MailContactImportRow struct {
	Name   string   `json:"name"`
	Emails []string `json:"emails"`
}

// PreviewMailContactImportRequest / Result。
type PreviewMailContactImportRequest struct {
	Rows []MailContactImportRow `json:"rows"`
}

// MailContactImportPreview 是通信录 CSV 导入预检结果。
type MailContactImportPreview struct {
	Accept []MailContactImportRow `json:"accept"`
	Skip   []string               `json:"skip"`
}

// CommitMailContactImportRequest 提交预览可接受行。
type CommitMailContactImportRequest struct {
	Rows []MailContactImportRow `json:"rows"`
}

// ListMailDraftsRequest 分页草稿。
type ListMailDraftsRequest struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// GetMailDraftRequest 读取单草稿。
type GetMailDraftRequest struct {
	ID string `json:"id"`
}

// DeleteMailDraftRequest 删除草稿箱中的草稿。
type DeleteMailDraftRequest struct {
	ID string `json:"id"`
}

// SaveMailDraftRequest 保存用户修订（CAS）。
type SaveMailDraftRequest struct {
	ID               string            `json:"id"`
	ExpectedRevision int64             `json:"expectedRevision"`
	AccountID        string            `json:"accountId"`
	Subject          string            `json:"subject"`
	BodyMarkdown     string            `json:"bodyMarkdown"`
	To               []MailAddressView `json:"to"`
	Cc               []MailAddressView `json:"cc"`
	Bcc              []MailAddressView `json:"bcc"`
	SendMode         string            `json:"sendMode"`
	DispatchMode     string            `json:"dispatchMode"`
	ScheduledLocal   string            `json:"scheduledLocal,omitempty"`
	TimeZone         string            `json:"timeZone,omitempty"`
}

// CreateMailDraftRequest 新建空白用户草稿。
type CreateMailDraftRequest struct {
	AccountID string `json:"accountId,omitempty"`
}

// PrepareMailSendRequest 校验并冻结当前修订，返回一次性确认令牌与只读预览。
type PrepareMailSendRequest struct {
	DraftID          string `json:"draftId"`
	ExpectedRevision int64  `json:"expectedRevision"`
}

// MailAttachmentPreview 是确认框里展示的已冻结附件。
type MailAttachmentPreview struct {
	Name   string `json:"name"`
	Bytes  int64  `json:"bytes"`
	SHA256 string `json:"sha256"`
}

// MailSendPreview 是本人确认前的只读定稿摘要。
type MailSendPreview struct {
	DraftID        string                  `json:"draftId"`
	Revision       int64                   `json:"revision"`
	AccountID      string                  `json:"accountId"`
	Subject        string                  `json:"subject"`
	BodyMarkdown   string                  `json:"bodyMarkdown"`
	To             []MailAddressView       `json:"to"`
	Cc             []MailAddressView       `json:"cc"`
	Bcc            []MailAddressView       `json:"bcc"`
	Attachments    []MailAttachmentPreview `json:"attachments"`
	SendMode       string                  `json:"sendMode"`
	DispatchMode   string                  `json:"dispatchMode"`
	ScheduledLocal string                  `json:"scheduledLocal,omitempty"`
	TimeZone       string                  `json:"timeZone,omitempty"`
	ScheduledAtUTC string                  `json:"scheduledAtUtc,omitempty"`
	Digest         string                  `json:"digest"`
}

// PrepareMailSendResult 含 confirmationId 与只读预览；不发信。
type PrepareMailSendResult struct {
	ConfirmationID string          `json:"confirmationId"`
	ExpiresAtUtc   string          `json:"expiresAtUtc"`
	Preview        MailSendPreview `json:"preview"`
}

// ConfirmMailSendRequest 消耗 confirmationId，入队 queued/scheduled。
type ConfirmMailSendRequest struct {
	ConfirmationID string `json:"confirmationId"`
}

// ConfirmMailSendResult 报告确认结果。
// immediate：尽量同步 SMTP，status 为 accepted/failed/unknown；scheduled：仅入队为 scheduled。
type ConfirmMailSendResult struct {
	SubmissionID string `json:"submissionId"`
	Status       string `json:"status"` // accepted | failed | unknown | queued | scheduled
	Detail       string `json:"detail,omitempty"`
	DraftID      string `json:"draftId"`
	Revision     int64  `json:"revision"`
}

// ListMailOutboxRequest 分页发件箱（投递项）。
type ListMailOutboxRequest struct {
	Limit  int `json:"limit,omitempty"`
	Offset int `json:"offset,omitempty"`
}

// MailOutboxEntryView 是发件箱一行；状态来自投递项，失败原因不含凭据；正文为定稿快照只读。
type MailOutboxEntryView struct {
	ItemID         string `json:"itemId"`
	SubmissionID   string `json:"submissionId"`
	DraftID        string `json:"draftId"`
	DraftRevision  int64  `json:"draftRevision"`
	Subject        string `json:"subject"`
	BodyMarkdown   string `json:"bodyMarkdown,omitempty"`
	Recipient      string `json:"recipient"`
	Status         string `json:"status"`
	BlockedReason  string `json:"blockedReason,omitempty"`
	ScheduledAtUTC string `json:"scheduledAtUtc,omitempty"`
	FinishedAtUTC  string `json:"finishedAtUtc,omitempty"`
	CreatedAtUTC   string `json:"createdAtUtc"`
	AttemptCount   int    `json:"attemptCount"`
}
