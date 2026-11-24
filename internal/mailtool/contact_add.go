package mailtool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/wt68/runcode/internal/mail"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// ContactSaver 由 desktop 注入：校验通过后写入本地通信录。
type ContactSaver interface {
	SaveContact(ctx context.Context, name, email string) (ContactHit, error)
}

type addContactInput struct {
	Name  string `json:"name"`
	Email string `json:"email"`
}

// AddMailContactTool 按姓名+邮箱写入通信录；格式错误时拒绝落盘。
type AddMailContactTool struct {
	Saver ContactSaver
}

// Name 返回 add_mail_contact。
func (AddMailContactTool) Name() string { return AddMailContactName }

// InputPresentation 预览邮箱。
func (AddMailContactTool) InputPresentation() tool.InputPresentation {
	return tool.InputPresentation{PrimaryField: "email"}
}

// Description 告诉模型：本地写入、须审批、坏地址要让用户改。
func (AddMailContactTool) Description() string {
	return "Add one person to the user's local mail contacts (通信录). " +
		"Call when the user asks to save someone like \"把马文涛 mawentao@ouc-online.com.cn 加入通信录\". " +
		"Requires name and a valid email. If the email format is invalid, this tool returns an error and does NOT save — " +
		"tell the user to correct the address and try again. " +
		"One contact per call; repeat for multiple people. Does not send mail and does not access OA or the network."
}

// InputSchema 声明 name + email。
func (AddMailContactTool) InputSchema() tool.Schema {
	return tool.Schema{
		Type: tool.SchemaTypeObject,
		Properties: map[string]tool.Schema{
			"name":  {Type: tool.SchemaTypeString, Description: "Display name for the contact."},
			"email": {Type: tool.SchemaTypeString, Description: "Email address to store."},
		},
		Required:             []string{"name", "email"},
		AdditionalProperties: false,
	}
}

// IsConcurrencySafe 报告 false：写通信录。
func (AddMailContactTool) IsConcurrencySafe() bool { return false }

// Run 先规范化地址，失败则中文报错且不写库。
func (t AddMailContactTool) Run(ctx context.Context, raw json.RawMessage, _ *tool.Context, _ chan<- tool.Event) (tool.Result, error) {
	var in addContactInput
	if err := json.Unmarshal(raw, &in); err != nil {
		return tool.Result{}, fmt.Errorf("parse add_mail_contact input: %w", err)
	}
	name := strings.TrimSpace(in.Name)
	email := strings.TrimSpace(in.Email)
	if name == "" {
		return tool.Result{}, fmt.Errorf("请提供联系人姓名后再添加到通信录")
	}
	if email == "" {
		return tool.Result{}, fmt.Errorf("请提供邮箱地址后再添加到通信录")
	}
	normalized, err := mail.NormalizeAddress(name, email)
	if err != nil {
		return tool.Result{}, fmt.Errorf("邮箱格式不正确：%s。请修改后再添加到通信录", email)
	}
	if t.Saver == nil {
		return tool.Result{}, fmt.Errorf("通信录写入不可用")
	}
	hit, err := t.Saver.SaveContact(ctx, normalized.Name, normalized.Address)
	if err != nil {
		return tool.Result{}, err
	}
	payload, err := json.Marshal(map[string]any{
		"status":  "saved",
		"contact": hit,
		"note":    "Contact saved to the local address book. Not an email send.",
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: []tool.ResultContent{{Type: tool.ResultContentTypeText, Text: string(payload)}}}, nil
}
