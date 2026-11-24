package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/protocol"
)

const maxMailAttachmentBytes = 20 << 20 // 20 MiB 合计上限（产品初始限制）

// PrepareMailSend 校验草稿修订、冻结附件字节、签发一次性 confirmationId。
// 不 SMTP；OA 锁在此与 Confirm 双检。
func (a *App) PrepareMailSend(req protocol.PrepareMailSendRequest) (protocol.PrepareMailSendResult, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	st, err := a.mailStore()
	if err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	ctx := context.Background()
	d, err := st.GetDraft(ctx, req.DraftID, mailOwners())
	if err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	if d.Revision != req.ExpectedRevision {
		return protocol.PrepareMailSendResult{}, wireError(fmt.Errorf("%w: 草稿已被其它位置更新，请刷新后重试", mail.ErrConflict))
	}
	if err := mailLiveGate(a, d); err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	frozen, err := freezeMailAttachments(d)
	if err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	now := time.Now().UTC()
	snap, err := mail.BuildPreparedSnapshot(d, frozen, now)
	if err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	if err := st.SavePreparedSnapshot(ctx, snap); err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	conf := mail.NewConfirmation(snap, now, mail.ConfirmationTTL)
	if err := st.SaveConfirmation(ctx, conf); err != nil {
		return protocol.PrepareMailSendResult{}, wireError(err)
	}
	return protocol.PrepareMailSendResult{
		ConfirmationID: conf.ID,
		ExpiresAtUtc:   conf.ExpiresAtUTC.Format(time.RFC3339Nano),
		Preview:        publicMailSendPreview(d, snap),
	}, nil
}

// ConfirmMailSend 校验令牌后写入 queued/scheduled 投递意图；SMTP 由独立 dispatcher 执行。
func (a *App) ConfirmMailSend(req protocol.ConfirmMailSendRequest) (protocol.ConfirmMailSendResult, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	id := strings.TrimSpace(req.ConfirmationID)
	if id == "" {
		return protocol.ConfirmMailSendResult{}, wireError(fmt.Errorf("%w: confirmationId", mail.ErrInvalid))
	}
	st, err := a.mailStore()
	if err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	// 入队前必须有派发器：否则确认成功、SMTP 永不发生（自测同步发信会掩盖此缝）。
	if err := a.ensureMailDispatcher(); err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	ctx := context.Background()
	now := time.Now().UTC()
	conf, err := st.GetConfirmation(ctx, id, mailOwners())
	if err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	d, err := st.GetDraft(ctx, conf.DraftID, mailOwners())
	if err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	if err := mail.ConfirmationStillValid(conf, d, now); err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	if err := mailLiveGate(a, d); err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	snap, err := st.GetPreparedSnapshot(ctx, conf.SnapshotID, mailOwners())
	if err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	if snap.Digest != conf.Digest || snap.DraftRevision != d.Revision {
		return protocol.ConfirmMailSendResult{}, wireError(fmt.Errorf("%w: snapshot mismatch", mail.ErrConfirmationExpired))
	}
	if err := st.MarkConfirmationUsed(ctx, conf.ID, mailOwnerLocal, now); err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	status := mail.StatusQueued
	if snap.DispatchMode == mail.ModeScheduledAt {
		status = mail.StatusScheduled
	}
	subID := "sub-" + mailNewID(12)
	items := buildSubmissionItems(snap, subID, status, now)
	sub := mail.Submission{
		ID: subID, OwnerScope: mailOwnerLocal, DraftID: d.ID, DraftRevision: d.Revision,
		SnapshotID: snap.ID, ConfirmationID: conf.ID, Status: status,
		CreatedAtUTC: now, UpdatedAtUTC: now,
	}
	if _, err := st.CreateSubmission(ctx, sub, items); err != nil {
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}
	d.Status = status
	d.UpdatedAtUTC = now
	if _, err := st.SaveDraft(ctx, d, d.Revision); err != nil && !errors.Is(err, mail.ErrConflict) {
		// 投递意图已落库；草稿状态更新失败不回滚确认，返回事实。
		return protocol.ConfirmMailSendResult{}, wireError(err)
	}

	result := protocol.ConfirmMailSendResult{
		SubmissionID: sub.ID,
		Status:       status,
		DraftID:      d.ID,
		Revision:     d.Revision,
	}
	// immediate：与「确认发送测试邮件」同路径同步 SMTP，避免只入队、派发器未跑导致「点了确认却收不到」。
	// scheduled：仍只入队，由 dispatcher 到期投递。
	if snap.DispatchMode != mail.ModeScheduledAt {
		delivStatus, detail := a.deliverSubmissionSync(st, sub, snap, items)
		result.Status = delivStatus
		result.Detail = detail
		return result, nil
	}
	a.wakeMailDispatcher()
	return result, nil
}

// deliverSubmissionSync 在确认线程内认领并投递（与自测信同一 SMTP 预算）；挡住 dispatcher 以免双投。
func (a *App) deliverSubmissionSync(st *mail.Store, sub mail.Submission, snap mail.PreparedSnapshot, items []mail.SubmissionItem) (status, detail string) {
	release := a.holdMailAdmission("confirm-immediate")
	defer release()

	ctx, cancel := context.WithTimeout(context.Background(), mailTestSendBudget)
	defer cancel()

	final := mail.StatusAccepted
	var lastDetail string
	for _, seed := range items {
		now := time.Now().UTC()
		claimed, err := st.ClaimItem(ctx, seed.ID, now)
		if err != nil {
			if errors.Is(err, mail.ErrConflict) {
				if cur, loadErr := st.LoadItem(ctx, seed.ID, mailOwners()); loadErr == nil {
					switch cur.Status {
					case mail.StatusAccepted:
						continue
					case mail.StatusFailed, mail.StatusUnknown:
						final = cur.Status
						lastDetail = cur.BlockedReason
					}
				}
				continue
			}
			final = mail.StatusFailed
			lastDetail = err.Error()
			_ = st.ReconcileSubmission(ctx, sub.ID, now)
			continue
		}
		_ = st.ReconcileSubmission(ctx, sub.ID, now)

		if ok, reason := a.mailAccountGate(ctx, snap.AccountID); !ok {
			if reason == "" {
				reason = "account_unavailable"
			}
			_ = st.FinishItem(ctx, claimed.ID, mail.StatusFailed, reason, now)
			_ = st.ReconcileSubmission(ctx, sub.ID, now)
			final = mail.StatusFailed
			lastDetail = reason
			continue
		}
		dayKey := time.Now().In(mailQuotaLocation()).Format("2006-01-02")
		if err := st.ReserveQuota(ctx, claimed.OwnerScope, snap.AccountID, dayKey, claimed.ID, 1, mail.DailyQuotaDefault); err != nil {
			reason := "quota_reserve_failed"
			if errors.Is(err, mail.ErrQuotaExceeded) {
				reason = "quota_exceeded"
			}
			_ = st.FinishItem(ctx, claimed.ID, mail.StatusFailed, reason, now)
			_ = st.ReconcileSubmission(ctx, sub.ID, now)
			final = mail.StatusFailed
			lastDetail = reason
			continue
		}

		delivStatus, reason, delivErr := a.mailDeliver(ctx, claimed, snap, sub)
		fin := time.Now().UTC()
		if reason == "" && delivErr != nil {
			reason = delivErr.Error()
		}
		switch delivStatus {
		case mail.StatusAccepted, mail.StatusUnknown:
			_ = st.ConsumeQuota(ctx, claimed.OwnerScope, claimed.ID)
			_ = st.FinishItem(ctx, claimed.ID, delivStatus, reason, fin)
			if delivStatus == mail.StatusUnknown {
				final = mail.StatusUnknown
				lastDetail = reason
			}
		case mail.StatusFailed:
			_ = st.ReleaseQuota(ctx, claimed.OwnerScope, claimed.ID)
			_ = st.FinishItem(ctx, claimed.ID, mail.StatusFailed, reason, fin)
			final = mail.StatusFailed
			lastDetail = reason
		default:
			_ = st.ConsumeQuota(ctx, claimed.OwnerScope, claimed.ID)
			_ = st.FinishItem(ctx, claimed.ID, mail.StatusUnknown, "invalid_delivery_status", fin)
			final = mail.StatusUnknown
			lastDetail = "invalid_delivery_status"
		}
		_ = st.ReconcileSubmission(ctx, sub.ID, fin)
	}
	if final == mail.StatusAccepted {
		lastDetail = ""
	}
	return final, lastDetail
}

func mailQuotaLocation() *time.Location {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return loc
}

// mailLiveGate 在 Prepare/Confirm 再过一遍账号与 OA 边界。
func mailLiveGate(a *App, d mail.Draft) error {
	if d.Source.SessionID != "" && a.oaLockedModel(d.Source.SessionID) != "" {
		return fmt.Errorf("来源会话已锁定 OA 内容，不能外发")
	}
	cfg := loadRawConfig().MailAccount
	if cfg == nil || !cfg.Enabled || strings.TrimSpace(cfg.PasswordProtected) == "" {
		return fmt.Errorf("%w: 请先在设置中接入可用邮箱账号", mail.ErrInvalid)
	}
	if strings.TrimSpace(d.AccountID) != "" && d.AccountID != cfg.ID {
		return fmt.Errorf("%w: 草稿账号与当前已保存账号不一致", mail.ErrInvalid)
	}
	if strings.TrimSpace(d.AccountID) == "" {
		return fmt.Errorf("%w: account required", mail.ErrInvalid)
	}
	return nil
}

func freezeMailAttachments(d mail.Draft) ([]mail.AttachmentRef, error) {
	if len(d.Attachments) == 0 {
		return nil, nil
	}
	out := make([]mail.AttachmentRef, 0, len(d.Attachments))
	var total int64
	for _, a := range d.Attachments {
		path := strings.TrimSpace(a.RelPath)
		if path == "" {
			return nil, fmt.Errorf("%w: empty attachment path", mail.ErrInvalid)
		}
		// 首版：仅接受已带哈希的引用，或可读的绝对/相对路径；无授权来源时明确失败。
		if a.ContentSHA256 != "" && a.ByteSize > 0 {
			out = append(out, a)
			total += a.ByteSize
			continue
		}
		abs := path
		if !filepath.IsAbs(abs) {
			return nil, fmt.Errorf("%w: 附件缺少来源会话授权快照，请回来源会话授权后重试（不猜当前工作区）", mail.ErrInvalid)
		}
		fi, err := os.Stat(abs)
		if err != nil || fi.IsDir() {
			return nil, fmt.Errorf("%w: attachment missing: %s", mail.ErrInvalid, a.Name)
		}
		total += fi.Size()
		if total > maxMailAttachmentBytes {
			return nil, fmt.Errorf("%w: attachments too large", mail.ErrInvalid)
		}
		sum, err := hashFileSHA256(abs)
		if err != nil {
			return nil, err
		}
		name := a.Name
		if name == "" {
			name = filepath.Base(abs)
		}
		out = append(out, mail.AttachmentRef{
			SourceSessionID: a.SourceSessionID,
			RelPath:         a.RelPath,
			Name:            name,
			ByteSize:        fi.Size(),
			ContentSHA256:   sum,
		})
	}
	return out, nil
}

func hashFileSHA256(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // 路径来自已授权附件引用
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, io.LimitReader(f, maxMailAttachmentBytes+1)); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func buildSubmissionItems(snap mail.PreparedSnapshot, subID, status string, _ time.Time) []mail.SubmissionItem {
	recipients := append([]mail.MailAddress(nil), snap.ResolvedTo...)
	if snap.SendMode == mail.ModeTogether {
		// together：一封多人，单 item 用第一个收件人代表；其余在 SMTP 步展开。
		if len(recipients) == 0 {
			return nil
		}
		it := mail.SubmissionItem{
			ID: "item-" + mailNewID(10), SubmissionID: subID, OwnerScope: mailOwnerLocal,
			Recipient: recipients[0], Status: status, ScheduledAtUTC: snap.ScheduledAtUTC,
		}
		return []mail.SubmissionItem{it}
	}
	out := make([]mail.SubmissionItem, 0, len(recipients))
	for _, r := range recipients {
		out = append(out, mail.SubmissionItem{
			ID: "item-" + mailNewID(10), SubmissionID: subID, OwnerScope: mailOwnerLocal,
			Recipient: r, Status: status, ScheduledAtUTC: snap.ScheduledAtUTC,
		})
	}
	return out
}

func publicMailSendPreview(d mail.Draft, snap mail.PreparedSnapshot) protocol.MailSendPreview {
	atts := make([]protocol.MailAttachmentPreview, 0, len(snap.Attachments))
	for _, a := range snap.Attachments {
		atts = append(atts, protocol.MailAttachmentPreview{Name: a.Name, Bytes: a.ByteSize, SHA256: a.ContentSHA256})
	}
	p := protocol.MailSendPreview{
		DraftID: d.ID, Revision: d.Revision, AccountID: snap.AccountID,
		Subject: snap.Subject, BodyMarkdown: snap.BodyMarkdown,
		To: toMailAddressViews(snap.ResolvedTo), Cc: toMailAddressViews(snap.ResolvedCc), Bcc: toMailAddressViews(snap.ResolvedBcc),
		Attachments: atts, SendMode: snap.SendMode, DispatchMode: snap.DispatchMode,
		ScheduledLocal: d.ScheduledLocal, TimeZone: d.TimeZone, Digest: snap.Digest,
	}
	if snap.ScheduledAtUTC != nil {
		p.ScheduledAtUTC = snap.ScheduledAtUTC.Format(time.RFC3339Nano)
	}
	return p
}
