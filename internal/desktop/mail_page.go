package desktop

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/mail"
	"github.com/wt68/runcode/internal/protocol"
)

// 本机单用户桌面 owner；后续若接通行证再按账号分区，不把凭据写入 mail 库。
const mailOwnerLocal = "local"

var (
	mailStoreMu   sync.Mutex
	mailStoreInst *mail.Store
	openMailStore = mail.OpenStore
)

func mailDBPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	product := strings.TrimSpace(AppProduct())
	if product == "" || strings.ContainsAny(product, `/\`) || strings.Contains(product, "..") {
		return "", fmt.Errorf("invalid product segment")
	}
	return filepath.Join(dir, "runcode", "mail", product, "mail.sqlite3"), nil
}

func (a *App) mailStore() (*mail.Store, error) {
	mailStoreMu.Lock()
	defer mailStoreMu.Unlock()
	if mailStoreInst != nil {
		return mailStoreInst, nil
	}
	path, err := mailDBPath()
	if err != nil {
		return nil, err
	}
	st, err := openMailStore(path)
	if err != nil {
		return nil, err
	}
	mailStoreInst = st
	return mailStoreInst, nil
}

func resetMailStoreForTest() {
	mailStoreMu.Lock()
	defer mailStoreMu.Unlock()
	if mailStoreInst != nil {
		_ = mailStoreInst.Close()
		mailStoreInst = nil
	}
}

func mailOwners() []string { return []string{mailOwnerLocal} }

func toMailAddressViews(in []mail.MailAddress) []protocol.MailAddressView {
	out := make([]protocol.MailAddressView, 0, len(in))
	for _, a := range in {
		out = append(out, protocol.MailAddressView{Name: a.Name, Address: a.Address})
	}
	return out
}

func fromMailAddressViews(in []protocol.MailAddressView) []mail.MailAddress {
	out := make([]mail.MailAddress, 0, len(in))
	for _, a := range in {
		out = append(out, mail.MailAddress{Name: a.Name, Address: a.Address})
	}
	return out
}

func publicMailContact(c mail.Contact) protocol.MailContactView {
	emails := c.Emails
	if emails == nil {
		emails = []string{}
	}
	return protocol.MailContactView{
		ID: c.ID, Revision: c.Revision, Name: c.Name, Emails: emails,
		CreatedAtUTC: c.CreatedAtUTC.UTC().Format(time.RFC3339Nano),
		UpdatedAtUTC: c.UpdatedAtUTC.UTC().Format(time.RFC3339Nano),
	}
}

func publicMailDraft(d mail.Draft) protocol.MailDraftView {
	return protocol.MailDraftView{
		ID: d.ID, Revision: d.Revision, Status: d.Status, AccountID: d.AccountID,
		Subject: d.Subject, BodyMarkdown: d.BodyMarkdown,
		To: toMailAddressViews(d.To), Cc: toMailAddressViews(d.Cc), Bcc: toMailAddressViews(d.Bcc),
		SendMode: d.SendMode, DispatchMode: d.DispatchMode,
		ScheduledLocal: d.ScheduledLocal, TimeZone: d.TimeZone,
		ProposalMD:   d.Source.ProposalMD,
		UpdatedAtUTC: d.UpdatedAtUTC.UTC().Format(time.RFC3339Nano),
		CreatedAtUTC: d.CreatedAtUTC.UTC().Format(time.RFC3339Nano),
	}
}

// ListMailContacts 列出通信录。
func (a *App) ListMailContacts(req protocol.ListMailContactsRequest) ([]protocol.MailContactView, error) {
	st, err := a.mailStore()
	if err != nil {
		return nil, wireError(err)
	}
	list, err := st.ListContacts(context.Background(), mailOwners(), req.Query, req.Limit, req.Offset)
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]protocol.MailContactView, 0, len(list))
	for _, c := range list {
		out = append(out, publicMailContact(c))
	}
	return out, nil
}

// SaveMailContact 新建或 CAS 更新联系人。
func (a *App) SaveMailContact(req protocol.SaveMailContactRequest) (protocol.MailContactView, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailContactView{}, wireError(err)
	}
	st, err := a.mailStore()
	if err != nil {
		return protocol.MailContactView{}, wireError(err)
	}
	c := mail.Contact{ID: strings.TrimSpace(req.ID), OwnerScope: mailOwnerLocal, Name: req.Name, Emails: req.Emails}
	saved, err := st.SaveContact(context.Background(), c, req.ExpectedRevision)
	if err != nil {
		return protocol.MailContactView{}, wireError(err)
	}
	return publicMailContact(saved), nil
}

// DeleteMailContact 删除联系人。
func (a *App) DeleteMailContact(req protocol.DeleteMailContactRequest) error {
	if err := mailRefuseIfExiting(a); err != nil {
		return wireError(err)
	}
	st, err := a.mailStore()
	if err != nil {
		return wireError(err)
	}
	return wireError(st.DeleteContact(context.Background(), req.ID, mailOwners(), req.ExpectedRevision))
}

// PreviewMailContactImport 预览 CSV 导入。
func (a *App) PreviewMailContactImport(req protocol.PreviewMailContactImportRequest) (protocol.MailContactImportPreview, error) {
	st, err := a.mailStore()
	if err != nil {
		return protocol.MailContactImportPreview{}, wireError(err)
	}
	rows := make([]mail.ContactImportRow, 0, len(req.Rows))
	for _, r := range req.Rows {
		rows = append(rows, mail.ContactImportRow{Name: r.Name, Emails: r.Emails})
	}
	prev, err := st.PreviewContactImport(context.Background(), mailOwnerLocal, rows)
	if err != nil {
		return protocol.MailContactImportPreview{}, wireError(err)
	}
	out := protocol.MailContactImportPreview{Skip: prev.Skip, Accept: make([]protocol.MailContactImportRow, 0, len(prev.Accept))}
	for _, r := range prev.Accept {
		out.Accept = append(out.Accept, protocol.MailContactImportRow{Name: r.Name, Emails: r.Emails})
	}
	if out.Skip == nil {
		out.Skip = []string{}
	}
	return out, nil
}

// CommitMailContactImport 提交导入。
func (a *App) CommitMailContactImport(req protocol.CommitMailContactImportRequest) ([]protocol.MailContactView, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return nil, wireError(err)
	}
	st, err := a.mailStore()
	if err != nil {
		return nil, wireError(err)
	}
	rows := make([]mail.ContactImportRow, 0, len(req.Rows))
	for _, r := range req.Rows {
		rows = append(rows, mail.ContactImportRow{Name: r.Name, Emails: r.Emails})
	}
	created, err := st.CommitContactImport(context.Background(), mailOwnerLocal, rows)
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]protocol.MailContactView, 0, len(created))
	for _, c := range created {
		out = append(out, publicMailContact(c))
	}
	return out, nil
}

// ListMailDrafts 列出仍可编辑的草稿（已投递的进发件箱，不在此出现）。
func (a *App) ListMailDrafts(req protocol.ListMailDraftsRequest) ([]protocol.MailDraftView, error) {
	st, err := a.mailStore()
	if err != nil {
		return nil, wireError(err)
	}
	list, err := st.ListDrafts(context.Background(), mailOwners(), req.Limit, req.Offset)
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]protocol.MailDraftView, 0, len(list))
	for _, d := range list {
		switch d.Status {
		case mail.StatusDraft, mail.StatusFailed, mail.StatusCancelled, mail.StatusMissed, mail.StatusScheduled:
			out = append(out, publicMailDraft(d))
		}
	}
	return out, nil
}

// ListMailOutbox 列出发件箱投递项（含终态与失败原因）。
func (a *App) ListMailOutbox(req protocol.ListMailOutboxRequest) ([]protocol.MailOutboxEntryView, error) {
	st, err := a.mailStore()
	if err != nil {
		return nil, wireError(err)
	}
	list, err := st.ListOutbox(context.Background(), mailOwners(), req.Limit, req.Offset)
	if err != nil {
		return nil, wireError(err)
	}
	out := make([]protocol.MailOutboxEntryView, 0, len(list))
	for _, e := range list {
		out = append(out, publicMailOutbox(e))
	}
	return out, nil
}

func publicMailOutbox(e mail.OutboxEntry) protocol.MailOutboxEntryView {
	v := protocol.MailOutboxEntryView{
		ItemID: e.ItemID, SubmissionID: e.SubmissionID, DraftID: e.DraftID,
		DraftRevision: e.DraftRevision, Subject: e.Subject, BodyMarkdown: e.BodyMarkdown,
		Recipient: e.Recipient.Address, Status: e.Status, BlockedReason: e.BlockedReason,
		CreatedAtUTC: e.CreatedAtUTC.UTC().Format(time.RFC3339Nano),
		AttemptCount: e.AttemptCount,
	}
	if e.Recipient.Name != "" && e.Recipient.Address != "" {
		v.Recipient = e.Recipient.Name + " <" + e.Recipient.Address + ">"
	}
	if e.ScheduledAtUTC != nil {
		v.ScheduledAtUTC = e.ScheduledAtUTC.UTC().Format(time.RFC3339Nano)
	}
	if e.FinishedAtUTC != nil {
		v.FinishedAtUTC = e.FinishedAtUTC.UTC().Format(time.RFC3339Nano)
	}
	return v
}

// GetMailDraft 读取草稿。
func (a *App) GetMailDraft(req protocol.GetMailDraftRequest) (protocol.MailDraftView, error) {
	st, err := a.mailStore()
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	d, err := st.GetDraft(context.Background(), req.ID, mailOwners())
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	return publicMailDraft(d), nil
}

// DeleteMailDraft 删除草稿箱中的草稿。
func (a *App) DeleteMailDraft(req protocol.DeleteMailDraftRequest) error {
	if err := mailRefuseIfExiting(a); err != nil {
		return wireError(err)
	}
	id := strings.TrimSpace(req.ID)
	if id == "" {
		return wireError(fmt.Errorf("%w: id", mail.ErrInvalid))
	}
	st, err := a.mailStore()
	if err != nil {
		return wireError(err)
	}
	return wireError(st.DeleteDraft(context.Background(), id, mailOwners()))
}

// CreateMailDraft 新建空白用户草稿。
func (a *App) CreateMailDraft(req protocol.CreateMailDraftRequest) (protocol.MailDraftView, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	st, err := a.mailStore()
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	accountID := strings.TrimSpace(req.AccountID)
	if accountID == "" {
		if cfg := loadRawConfig().MailAccount; cfg != nil && cfg.Enabled {
			accountID = cfg.ID
		}
	}
	d, err := st.CreateUserDraft(context.Background(), mailOwnerLocal, accountID)
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	return publicMailDraft(d), nil
}

// SaveMailDraft 以 expectedRevision 做 CAS 保存用户编辑；不发信。
func (a *App) SaveMailDraft(req protocol.SaveMailDraftRequest) (protocol.MailDraftView, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	st, err := a.mailStore()
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	cur, err := st.GetDraft(context.Background(), req.ID, mailOwners())
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	if cur.Revision != req.ExpectedRevision {
		return protocol.MailDraftView{}, wireError(fmt.Errorf("%w: 草稿已被其它位置更新，请刷新后重试", mail.ErrConflict))
	}
	edit := mail.DraftEdit{
		AccountID: req.AccountID, Subject: req.Subject, BodyMarkdown: req.BodyMarkdown,
		To: fromMailAddressViews(req.To), Cc: fromMailAddressViews(req.Cc), Bcc: fromMailAddressViews(req.Bcc),
		SendMode: req.SendMode, DispatchMode: req.DispatchMode,
		ScheduledLocal: req.ScheduledLocal, TimeZone: req.TimeZone,
	}
	next, err := mail.ApplyDraftEdit(cur, edit, time.Now().UTC())
	if err != nil {
		return protocol.MailDraftView{}, wireError(err)
	}
	saved, err := st.SaveDraft(context.Background(), next, req.ExpectedRevision)
	if err != nil {
		if errors.Is(err, mail.ErrConflict) {
			return protocol.MailDraftView{}, wireError(fmt.Errorf("%w: 草稿已被其它位置更新，本地编辑未覆盖", err))
		}
		return protocol.MailDraftView{}, wireError(err)
	}
	return publicMailDraft(saved), nil
}
