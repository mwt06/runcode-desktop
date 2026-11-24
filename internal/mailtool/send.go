package mailtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// CallMeta 标识一次工具调用，供提案幂等键使用。
type CallMeta struct {
	SessionID string
	TurnID    string
	ToolUseID string
}

// AddressIn 是模型传入的收件人。
type AddressIn struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// AttachmentIn 是来源会话内已授权附件引用；字节在用户确认时才冻结。
type AttachmentIn struct {
	Path string `json:"path"`
	Name string `json:"name,omitempty"`
}

// SendInput 是 send_email 参数；不含 confirmed——模型不能用 confirmed 绕过本人确认。
type SendInput struct {
	AccountID      string         `json:"accountId,omitempty"`
	Subject        string         `json:"subject"`
	BodyMarkdown   string         `json:"bodyMarkdown"`
	To             []AddressIn    `json:"to"`
	Cc             []AddressIn    `json:"cc,omitempty"`
	Bcc            []AddressIn    `json:"bcc,omitempty"`
	Attachments    []AttachmentIn `json:"attachments,omitempty"`
	SendMode       string         `json:"sendMode,omitempty"`       // together | individual
	DispatchMode   string         `json:"dispatchMode,omitempty"`   // immediate | scheduled_at
	ScheduledLocal string         `json:"scheduledLocal,omitempty"` // YYYY-MM-DDTHH:mm:ss
	TimeZone       string         `json:"timeZone,omitempty"`
}

// DraftReady 是工具成功结果：只表示草稿已创建，不是已发送。
type DraftReady struct {
	Status         string `json:"status"` // draft_ready
	DraftID        string `json:"draftId"`
	Revision       int64  `json:"revision"`
	Subject        string `json:"subject,omitempty"`
	NeedsMailSetup bool   `json:"needsMailSetup,omitempty"`
}

// DraftHost 由 desktop 注入：校验 OA/账号边界后幂等写入提案草稿。
type DraftHost interface {
	CreateProposal(ctx context.Context, in SendInput, meta CallMeta) (DraftReady, error)
}

type sendRawProbe struct {
	Confirmed *bool `json:"confirmed"`
}

// SendEmailTool 准备可编辑草稿；不 SMTP。
type SendEmailTool struct {
	Host DraftHost
}

// Name 返回 send_email。
func (SendEmailTool) Name() string { return SendEmailName }

// InputPresentation 预览主题。
func (SendEmailTool) InputPresentation() tool.InputPresentation {
	return tool.InputPresentation{PrimaryField: "subject"}
}

// Description 明确：只准备草稿，用户编辑并确认后才发；不可声称已发出。
func (SendEmailTool) Description() string {
	return "Prepare an editable email draft for the user. " +
		"Persists recipients, subject, Markdown body, attachments references, and optional schedule, " +
		"then returns {status:\"draft_ready\", draftId, revision} immediately. " +
		"This does NOT send mail over SMTP and does NOT wait for the user to finish editing. " +
		"Tell the user the draft is ready and waiting for their review/confirmation; never claim the message was sent. " +
		"If needsMailSetup is true, tell the user they must first configure mailbox address and password in Settings → Mail, " +
		"and that the app shows a direct link to that settings page; do not invent a URL. " +
		"There is no confirmed parameter — human confirmation happens in the app UI after editing."
}

// InputSchema 声明提案字段；故意不暴露 confirmed。
func (SendEmailTool) InputSchema() tool.Schema {
	addr := tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"name":    {Type: tool.SchemaTypeString},
			"address": {Type: tool.SchemaTypeString, Description: "Email address."},
		},
		Required:             []string{"address"},
		AdditionalProperties: false,
	}
	att := tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"path": {Type: tool.SchemaTypeString, Description: "Workspace-relative or authorized absolute path."},
			"name": {Type: tool.SchemaTypeString},
		},
		Required:             []string{"path"},
		AdditionalProperties: false,
	}
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"accountId":      {Type: tool.SchemaTypeString, Description: "Saved mailbox account id; omit to use the default enabled account."},
			"subject":        {Type: tool.SchemaTypeString},
			"bodyMarkdown":   {Type: tool.SchemaTypeString, Description: "Markdown body only; no native HTML."},
			"to":             {Type: tool.SchemaTypeArray, Items: &addr},
			"cc":             {Type: tool.SchemaTypeArray, Items: &addr},
			"bcc":            {Type: tool.SchemaTypeArray, Items: &addr},
			"attachments":    {Type: tool.SchemaTypeArray, Items: &att},
			"sendMode":       {Type: tool.SchemaTypeString, Description: "together (default) or individual"},
			"dispatchMode":   {Type: tool.SchemaTypeString, Description: "immediate (default) or scheduled_at"},
			"scheduledLocal": {Type: tool.SchemaTypeString, Description: "Local wall time YYYY-MM-DDTHH:mm:ss when scheduled_at"},
			"timeZone":       {Type: tool.SchemaTypeString, Description: "IANA timezone when scheduled_at"},
		},
		Required:             []string{"subject", "bodyMarkdown", "to"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe 报告 false：写草稿库。
func (SendEmailTool) IsConcurrencySafe() bool { return false }

// Run 拒绝 confirmed 绕过，创建草稿并回传 draft_ready。
func (t SendEmailTool) Run(ctx context.Context, raw json.RawMessage, tc *tool.Context, out chan<- tool.Event) (tool.Result, error) {
	var probe sendRawProbe
	_ = json.Unmarshal(raw, &probe)
	if probe.Confirmed != nil {
		return tool.Result{}, fmt.Errorf("confirmed is not accepted; the user must confirm in the app after editing the draft")
	}
	var in SendInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse send_email input: %w", err)
	}
	in.Subject = strings.TrimSpace(in.Subject)
	in.BodyMarkdown = strings.TrimSpace(in.BodyMarkdown)
	if in.Subject == "" || in.BodyMarkdown == "" || len(in.To) == 0 {
		return tool.Result{}, fmt.Errorf("subject, bodyMarkdown and to are required")
	}
	if t.Host == nil {
		return tool.Result{}, fmt.Errorf("mail draft host is unavailable")
	}
	meta := CallMeta{}
	if tc != nil {
		// MessageID 充当回合键：同一次助手回合内同 ToolUseID 幂等；无 TurnID 字段。
		meta.SessionID = tc.SessionID
		meta.TurnID = tc.MessageID
		meta.ToolUseID = tc.ToolUseID
	}
	ready, err := t.Host.CreateProposal(ctx, in, meta)
	if err != nil {
		return tool.Result{}, err
	}
	if ready.Status == "" {
		ready.Status = "draft_ready"
	}
	if out != nil && tc != nil {
		select {
		case out <- tool.Event{Type: tool.EventTypeProgress, ToolName: SendEmailName, ToolUseID: tc.ToolUseID, Message: "草稿已就绪", Data: ready}:
		default:
		}
	}
	payload, err := json.Marshal(ready)
	if err != nil {
		return tool.Result{}, err
	}
	text := string(payload) + "\n草稿已创建，等待用户在「邮箱与通信录」中编辑并确认后才会发送。不要声称邮件已发出。"
	if ready.NeedsMailSetup {
		text += "\nneedsMailSetup=true：本机尚未接入可用邮箱（地址/密码等）。请明确告诉用户先到「设置 → 邮箱」填写邮箱地址与密码；应用界面会提供「前往邮箱设置」入口，不要编造链接。"
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: text}}}, nil
}
