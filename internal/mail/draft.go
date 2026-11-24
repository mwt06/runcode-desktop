package mail

import (
	"fmt"
	"strings"
	"time"
)

// ApplyDraftEdit 将用户编辑应用到草稿：修订号 +1，规范化地址，保留模型 Source 原文。
// 旧确认令牌由调用方根据返回的新 Revision 失效，本函数不改写提案历史。
func ApplyDraftEdit(d Draft, edit DraftEdit, now time.Time) (Draft, error) {
	if d.Status != StatusDraft && d.Status != StatusScheduled && d.Status != StatusCancelled && d.Status != StatusMissed && d.Status != StatusFailed {
		// 已进入投递链路的草稿不可改规则字段；调用方也可另做拦截。
		if d.Status == StatusQueued || d.Status == StatusSending || d.Status == StatusAccepted || d.Status == StatusUnknown {
			return Draft{}, fmt.Errorf("%w: draft not editable in status %s", ErrInvalid, d.Status)
		}
	}
	if err := ValidateDraftEdit(edit); err != nil {
		return Draft{}, err
	}
	to, err := normalizeList(edit.To)
	if err != nil {
		return Draft{}, err
	}
	cc, err := normalizeList(edit.Cc)
	if err != nil {
		return Draft{}, err
	}
	bcc, err := normalizeList(edit.Bcc)
	if err != nil {
		return Draft{}, err
	}

	out := d
	out.Revision = d.Revision + 1
	out.AccountID = strings.TrimSpace(edit.AccountID)
	out.Subject = strings.TrimSpace(edit.Subject)
	out.BodyMarkdown = edit.BodyMarkdown // 保留用户空白意图；校验已要求 trim 非空
	if strings.TrimSpace(out.BodyMarkdown) == "" {
		return Draft{}, fmt.Errorf("%w: 请填写正文内容", ErrInvalid)
	}
	out.To, out.Cc, out.Bcc = to, cc, bcc
	out.Attachments = append([]AttachmentRef(nil), edit.Attachments...)
	out.SendMode = edit.SendMode
	out.DispatchMode = edit.DispatchMode
	out.ScheduledLocal = strings.TrimSpace(edit.ScheduledLocal)
	out.TimeZone = strings.TrimSpace(edit.TimeZone)
	out.Status = StatusDraft
	out.BlockedReason = ""
	out.UpdatedAtUTC = now.UTC()

	if edit.DispatchMode == ModeScheduledAt {
		at, err := ParseLocalSchedule(edit.ScheduledLocal, edit.TimeZone)
		if err != nil {
			return Draft{}, err
		}
		out.ScheduledAtUTC = &at
	} else {
		out.ScheduledAtUTC = nil
		out.ScheduledLocal = ""
	}
	// Source.ProposalMD 永不被用户正文覆盖——模型提案是审计历史。
	return out, nil
}

// ConfirmationStillValid 检查确认是否仍绑定当前草稿修订且未使用、未过期。
func ConfirmationStillValid(c Confirmation, draft Draft, now time.Time) error {
	if c.DraftID != draft.ID || c.OwnerScope != draft.OwnerScope {
		return ErrNotFound
	}
	if c.DraftRevision != draft.Revision {
		return fmt.Errorf("%w: draft revised", ErrConfirmationExpired)
	}
	if c.UsedAtUTC != nil {
		return fmt.Errorf("%w: already used", ErrConfirmationExpired)
	}
	// 过期：now 严格晚于 ExpiresAt（到期瞬间仍可用）。
	if now.UTC().After(c.ExpiresAtUTC) {
		return fmt.Errorf("%w: expired", ErrConfirmationExpired)
	}
	return nil
}

// ToolResultDraftReady 强调工具成功只表示草稿已创建。
// needsMailSetup 为真时，前端应提示用户先接入邮箱并提供设置入口。
func ToolResultDraftReady(draftID string, revision int64, needsMailSetup bool) map[string]any {
	m := map[string]any{
		"status":   "draft_ready",
		"draftId":  draftID,
		"revision": revision,
	}
	if needsMailSetup {
		m["needsMailSetup"] = true
	}
	return m
}
