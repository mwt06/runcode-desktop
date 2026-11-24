package desktop

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/mailtool"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/tool"
)

// mailTools 只进主会话 ExtraTools；子代理结构上拿不到。
func (a *App) mailTools(cfg *engine.Config, sessionID string) []tool.Tool {
	_ = cfg
	host := mailDraftHost{app: a, sessionID: sessionID}
	searcher := mailContactSearcher{app: a}
	saver := mailContactSaver{app: a}
	return []tool.Tool{
		mailtool.SearchContactsTool{Searcher: searcher},
		mailtool.AddMailContactTool{Saver: saver},
		mailtool.SendEmailTool{Host: host},
	}
}

type mailContactSearcher struct{ app *App }

type mailContactSaver struct{ app *App }

func (s mailContactSaver) SaveContact(ctx context.Context, name, email string) (mailtool.ContactHit, error) {
	if err := mailRefuseIfExiting(s.app); err != nil {
		return mailtool.ContactHit{}, err
	}
	st, err := s.app.mailStore()
	if err != nil {
		return mailtool.ContactHit{}, err
	}
	saved, err := st.SaveContact(ctx, mail.Contact{
		OwnerScope: mailOwnerLocal,
		Name:       name,
		Emails:     []string{email},
	}, 0)
	if err != nil {
		if errors.Is(err, mail.ErrInvalid) && strings.Contains(err.Error(), "email already used") {
			return mailtool.ContactHit{}, fmt.Errorf("该邮箱已在通信录中，未重复添加")
		}
		return mailtool.ContactHit{}, err
	}
	emails := saved.Emails
	if emails == nil {
		emails = []string{}
	}
	return mailtool.ContactHit{ID: saved.ID, Name: saved.Name, Emails: emails}, nil
}

func (s mailContactSearcher) SearchContacts(ctx context.Context, query string, limit int) ([]mailtool.ContactHit, error) {
	if err := mailRefuseIfExiting(s.app); err != nil {
		return nil, err
	}
	st, err := s.app.mailStore()
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 20 {
		limit = 20
	}
	list, err := st.ListContacts(ctx, mailOwners(), query, limit, 0)
	if err != nil {
		return nil, err
	}
	out := make([]mailtool.ContactHit, 0, len(list))
	for _, c := range list {
		emails := c.Emails
		if emails == nil {
			emails = []string{}
		}
		out = append(out, mailtool.ContactHit{ID: c.ID, Name: c.Name, Emails: emails})
	}
	return out, nil
}

type mailDraftHost struct {
	app       *App
	sessionID string
}

// CreateProposal 幂等写入模型提案草稿；OA 锁拒绝；忽略模型 confirmed。
func (h mailDraftHost) CreateProposal(ctx context.Context, in mailtool.SendInput, meta mailtool.CallMeta) (mailtool.DraftReady, error) {
	if err := mailRefuseIfExiting(h.app); err != nil {
		return mailtool.DraftReady{}, err
	}
	sid := meta.SessionID
	if sid == "" {
		sid = h.sessionID
	}
	if h.app.oaLockedModel(sid) != "" {
		return mailtool.DraftReady{}, fmt.Errorf("当前会话已读取 OA 内容，不能准备外发邮件草稿")
	}
	st, err := h.app.mailStore()
	if err != nil {
		return mailtool.DraftReady{}, err
	}
	accountID := strings.TrimSpace(in.AccountID)
	if accountID == "" {
		if cfg := loadRawConfig().MailAccount; cfg != nil && cfg.Enabled {
			accountID = cfg.ID
		}
	}
	to, err := mailAddrsFromTool(in.To)
	if err != nil {
		return mailtool.DraftReady{}, err
	}
	cc, err := mailAddrsFromTool(in.Cc)
	if err != nil {
		return mailtool.DraftReady{}, err
	}
	bcc, err := mailAddrsFromTool(in.Bcc)
	if err != nil {
		return mailtool.DraftReady{}, err
	}
	sendMode := strings.TrimSpace(in.SendMode)
	if sendMode == "" {
		sendMode = mail.ModeTogether
	}
	dispatch := strings.TrimSpace(in.DispatchMode)
	if dispatch == "" {
		dispatch = mail.ModeImmediate
	}
	atts := make([]mail.AttachmentRef, 0, len(in.Attachments))
	for _, a := range in.Attachments {
		path := strings.TrimSpace(a.Path)
		if path == "" {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			name = filepath.Base(path)
		}
		atts = append(atts, mail.AttachmentRef{
			SourceSessionID: sid,
			RelPath:         path,
			Name:            name,
		})
	}
	proposalMD := in.BodyMarkdown
	now := time.Now().UTC()
	d := mail.Draft{
		ID:         "draft-" + mailNewID(10),
		OwnerScope: mailOwnerLocal,
		AccountID:  accountID,
		Revision:   1,
		Status:     mail.StatusDraft,
		Source: mail.DraftSource{
			SessionID: sid, TurnID: meta.TurnID, ToolUseID: meta.ToolUseID,
			ProposalMD: proposalMD, CreatedAtUTC: now,
		},
		Subject: in.Subject, BodyMarkdown: in.BodyMarkdown,
		To: to, Cc: cc, Bcc: bcc, Attachments: atts,
		SendMode: sendMode, DispatchMode: dispatch,
		ScheduledLocal: strings.TrimSpace(in.ScheduledLocal),
		TimeZone:       strings.TrimSpace(in.TimeZone),
		CreatedAtUTC:   now, UpdatedAtUTC: now,
	}
	if dispatch == mail.ModeScheduledAt {
		at, err := mail.ParseLocalSchedule(d.ScheduledLocal, d.TimeZone)
		if err != nil {
			return mailtool.DraftReady{}, err
		}
		d.ScheduledAtUTC = &at
	}
	// 提案阶段允许缺账号：用户可稍后在邮件页补；保存用户编辑时才强制账号。
	saved, err := st.CreateDraftFromProposal(ctx, d)
	if err != nil {
		return mailtool.DraftReady{}, err
	}
	needsSetup := !mailAccountConfiguredForSend(accountID)
	ready := mail.ToolResultDraftReady(saved.ID, saved.Revision, needsSetup)
	return mailtool.DraftReady{
		Status:         fmt.Sprint(ready["status"]),
		DraftID:        fmt.Sprint(ready["draftId"]),
		Revision:       saved.Revision,
		Subject:        saved.Subject,
		NeedsMailSetup: needsSetup,
	}, nil
}

// mailAccountConfiguredForSend 判断当前是否已接入可发信的邮箱账号。
// 与 mailLiveGate / mailAccountGate 同口径：启用 + 有密文；草稿指定了账号时还须与已保存 ID 一致。
func mailAccountConfiguredForSend(draftAccountID string) bool {
	cfg := loadRawConfig().MailAccount
	if cfg == nil || !cfg.Enabled {
		return false
	}
	if strings.TrimSpace(cfg.Address) == "" || strings.TrimSpace(cfg.PasswordProtected) == "" {
		return false
	}
	if id := strings.TrimSpace(draftAccountID); id != "" && id != cfg.ID {
		return false
	}
	return true
}

func mailAddrsFromTool(in []mailtool.AddressIn) ([]mail.MailAddress, error) {
	out := make([]mail.MailAddress, 0, len(in))
	for _, a := range in {
		n, err := mail.NormalizeAddress(a.Name, a.Address)
		if err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return mail.DedupeAddresses(out), nil
}

// mailNewID 生成短随机 id（测试可替换）。
var mailNewID = func(n int) string {
	if n <= 0 {
		n = 8
	}
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	s := hex.EncodeToString(b)
	if len(s) > n {
		return s[:n]
	}
	return s
}
