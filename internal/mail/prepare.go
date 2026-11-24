package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ConfirmationTTL 是本人确认令牌默认有效期。
const ConfirmationTTL = 15 * time.Minute

// BuildPreparedSnapshot 从当前草稿与已冻结附件构造定稿；校验地址/主题/正文。
// frozen 必须已带 ContentSHA256（空附件列表允许）。
func BuildPreparedSnapshot(d Draft, frozen []AttachmentRef, now time.Time) (PreparedSnapshot, error) {
	edit := DraftEdit{
		AccountID: d.AccountID, Subject: d.Subject, BodyMarkdown: d.BodyMarkdown,
		To: d.To, Cc: d.Cc, Bcc: d.Bcc, Attachments: frozen,
		SendMode: d.SendMode, DispatchMode: d.DispatchMode,
		ScheduledLocal: d.ScheduledLocal, TimeZone: d.TimeZone,
	}
	if err := ValidateDraftEdit(edit); err != nil {
		return PreparedSnapshot{}, err
	}
	for _, a := range frozen {
		if strings.TrimSpace(a.RelPath) == "" {
			return PreparedSnapshot{}, fmt.Errorf("%w: attachment path", ErrInvalid)
		}
		if strings.TrimSpace(a.ContentSHA256) == "" {
			return PreparedSnapshot{}, fmt.Errorf("%w: attachment not frozen", ErrInvalid)
		}
	}
	to, err := normalizeList(d.To)
	if err != nil {
		return PreparedSnapshot{}, err
	}
	cc, err := normalizeList(d.Cc)
	if err != nil {
		return PreparedSnapshot{}, err
	}
	bcc, err := normalizeList(d.Bcc)
	if err != nil {
		return PreparedSnapshot{}, err
	}
	snap := PreparedSnapshot{
		ID:            "snap-" + shortIDPublic(12),
		DraftID:       d.ID,
		DraftRevision: d.Revision,
		OwnerScope:    d.OwnerScope,
		AccountID:     strings.TrimSpace(d.AccountID),
		Subject:       strings.TrimSpace(d.Subject),
		BodyMarkdown:  d.BodyMarkdown,
		ResolvedTo:    to,
		ResolvedCc:    cc,
		ResolvedBcc:   bcc,
		Attachments:   append([]AttachmentRef(nil), frozen...),
		SendMode:      d.SendMode,
		DispatchMode:  d.DispatchMode,
		CreatedAtUTC:  now.UTC(),
	}
	if d.DispatchMode == ModeScheduledAt && d.ScheduledAtUTC != nil {
		at := d.ScheduledAtUTC.UTC()
		snap.ScheduledAtUTC = &at
	}
	snap.Digest = SnapshotDigest(snap)
	return snap, nil
}

// SnapshotDigest 对定稿关键字段做稳定摘要，改任意字段即失效旧确认。
func SnapshotDigest(s PreparedSnapshot) string {
	type wire struct {
		DraftID, DraftRevision, Owner, Account, Subject, Body string
		To, Cc, Bcc                                           []MailAddress
		Atts                                                  []AttachmentRef
		SendMode, DispatchMode                                string
		Scheduled                                             string
	}
	sched := ""
	if s.ScheduledAtUTC != nil {
		sched = s.ScheduledAtUTC.UTC().Format(time.RFC3339Nano)
	}
	w := wire{
		DraftID: s.DraftID, DraftRevision: fmt.Sprintf("%d", s.DraftRevision),
		Owner: s.OwnerScope, Account: s.AccountID, Subject: s.Subject, Body: s.BodyMarkdown,
		To: s.ResolvedTo, Cc: s.ResolvedCc, Bcc: s.ResolvedBcc, Atts: s.Attachments,
		SendMode: s.SendMode, DispatchMode: s.DispatchMode, Scheduled: sched,
	}
	raw, _ := json.Marshal(w)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// NewConfirmation 绑定定稿摘要的一次性令牌。
func NewConfirmation(snap PreparedSnapshot, now time.Time, ttl time.Duration) Confirmation {
	if ttl <= 0 {
		ttl = ConfirmationTTL
	}
	return Confirmation{
		ID:            "conf-" + shortIDPublic(12),
		OwnerScope:    snap.OwnerScope,
		DraftID:       snap.DraftID,
		DraftRevision: snap.DraftRevision,
		SnapshotID:    snap.ID,
		Digest:        snap.Digest,
		ExpiresAtUTC:  now.UTC().Add(ttl),
		CreatedAtUTC:  now.UTC(),
	}
}

// shortIDPublic 供 prepare 使用；与 store shortID 同算法。
func shortIDPublic(n int) string {
	return shortID(n)
}
