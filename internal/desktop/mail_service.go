package desktop

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/wt68/runcode/internal/mail"
)

// mailCtl 是邮件库可用性与独立派发器（与 automation.autoCtl 分离）。
type mailCtl struct {
	mu        sync.Mutex
	available bool
	reason    string
	dispatch  *mail.Dispatcher
	store     *mail.Store
}

func (a *App) initMail() {
	if a == nil {
		return
	}
	path, err := mailDBPath()
	if err != nil {
		a.setMailUnavailable("邮件库路径不可用")
		debugLog("mail init path: %v", err)
		return
	}
	st, err := openMailStore(path)
	if err != nil {
		a.setMailUnavailable("邮件库不可用")
		debugLog("mail init open: %v", err)
		return
	}
	mailStoreMu.Lock()
	mailStoreInst = st
	mailStoreMu.Unlock()

	if err := a.attachMailDispatcher(st); err != nil {
		_ = st.Close()
		mailStoreMu.Lock()
		mailStoreInst = nil
		mailStoreMu.Unlock()
		a.setMailUnavailable("邮件派发器启动失败")
		debugLog("mail dispatcher start: %v", err)
		return
	}
}

// attachMailDispatcher 把派发器挂到同一 Store；已有派发器则复用，避免双循环重复投递。
func (a *App) attachMailDispatcher(st *mail.Store) error {
	if a == nil || st == nil {
		return fmt.Errorf("mail unavailable")
	}
	a.mailCtl.mu.Lock()
	defer a.mailCtl.mu.Unlock()
	if a.mailCtl.dispatch != nil {
		a.mailCtl.store = st
		a.mailCtl.available = true
		a.mailCtl.reason = ""
		return nil
	}
	d := mail.NewDispatcher(st, mail.SystemClock{}, a.mailDeliver, a.mailAccountGate)
	if err := d.Start(context.Background()); err != nil {
		return err
	}
	a.mailCtl.dispatch = d
	a.mailCtl.store = st
	a.mailCtl.available = true
	a.mailCtl.reason = ""
	return nil
}

// ensureMailDispatcher 在确认发信前保证派发器活着；修「库懒打开但无人投递」的缝。
func (a *App) ensureMailDispatcher() error {
	st, err := a.mailStore()
	if err != nil {
		return err
	}
	if err := a.attachMailDispatcher(st); err != nil {
		return fmt.Errorf("邮件派发器不可用: %w", err)
	}
	if a.mailDispatcher() == nil {
		return fmt.Errorf("邮件派发器不可用")
	}
	return nil
}

func (a *App) setMailUnavailable(reason string) {
	a.mailCtl.mu.Lock()
	a.mailCtl.available = false
	a.mailCtl.reason = reason
	a.mailCtl.dispatch = nil
	a.mailCtl.store = nil
	a.mailCtl.mu.Unlock()
}

func (a *App) mailDispatcher() *mail.Dispatcher {
	if a == nil {
		return nil
	}
	a.mailCtl.mu.Lock()
	defer a.mailCtl.mu.Unlock()
	return a.mailCtl.dispatch
}

func (a *App) wakeMailDispatcher() {
	if d := a.mailDispatcher(); d != nil {
		d.Wake()
	}
}

func (a *App) holdMailAdmission(reason string) func() {
	d := a.mailDispatcher()
	if d == nil {
		return func() {}
	}
	return d.HoldAdmission(reason)
}

func (a *App) mailExitSummary(ctx context.Context) (MailExitSummary, error) {
	d := a.mailDispatcher()
	if d == nil {
		// 库不可用：保守提示，不假装无待发。
		a.mailCtl.mu.Lock()
		avail := a.mailCtl.available
		a.mailCtl.mu.Unlock()
		if !avail {
			return MailExitSummary{}, fmt.Errorf("mail unavailable")
		}
		return MailExitSummary{PendingCount: 0}, nil
	}
	n, err := d.ExitSummary(ctx, mailOwners())
	if err != nil {
		return MailExitSummary{}, err
	}
	return MailExitSummary{PendingCount: n}, nil
}

func (a *App) prepareMailShutdown(ctx context.Context) error {
	d := a.mailDispatcher()
	if d == nil {
		return nil
	}
	_ = d.HoldAdmission("shutdown")
	// 有界等待：派发循环看到 hold 后不再认领；在途 SMTP 由 ctx 取消。
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(50 * time.Millisecond):
		return nil
	}
}

func (a *App) stopMail(ctx context.Context) {
	a.mailCtl.mu.Lock()
	d := a.mailCtl.dispatch
	st := a.mailCtl.store
	a.mailCtl.dispatch = nil
	a.mailCtl.store = nil
	a.mailCtl.available = false
	a.mailCtl.mu.Unlock()
	if d != nil {
		done := make(chan struct{})
		go func() {
			d.Stop()
			close(done)
		}()
		select {
		case <-done:
		case <-ctx.Done():
		}
	}
	mailStoreMu.Lock()
	if mailStoreInst == st && st != nil {
		_ = st.Close()
		mailStoreInst = nil
	} else if st != nil {
		_ = st.Close()
	}
	mailStoreMu.Unlock()
	_ = ctx
}

func (a *App) mailAccountGate(_ context.Context, accountID string) (bool, string) {
	cfg := loadRawConfig().MailAccount
	if cfg == nil || !cfg.Enabled {
		return false, "account_disabled"
	}
	if strings.TrimSpace(accountID) != "" && accountID != cfg.ID {
		return false, "account_mismatch"
	}
	if strings.TrimSpace(cfg.PasswordProtected) == "" {
		return false, "account_no_password"
	}
	return true, ""
}

func (a *App) mailDeliver(ctx context.Context, item mail.SubmissionItem, snap mail.PreparedSnapshot, _ mail.Submission) (status, reason string, err error) {
	cfg := loadRawConfig().MailAccount
	if cfg == nil || !cfg.Enabled {
		return mail.StatusFailed, "account_disabled", nil
	}
	password, ok := mailUnprotectSecret(cfg.PasswordProtected)
	if !ok || password == "" {
		return mail.StatusFailed, "password_unavailable", nil
	}
	from := mail.DisplayFrom{Name: cfg.DisplayName, Address: cfg.Address}
	msg, err := mail.BuildOutboundMessage(from, snap, item, time.Now().UTC())
	if err != nil {
		return mail.StatusFailed, err.Error(), err
	}
	host := cfg.Host
	port := cfg.Port
	if port == 0 {
		port = mailSMTPPort
	}
	res, sendErr := mailSMTP.SendMessage(ctx, host, port, cfg.Address, password, msg)
	a.recordMailAttempts(ctx, item, res)
	status = res.Status
	if status == "" {
		status = mail.StatusFailed
	}
	reason = res.Detail
	if sendErr != nil && reason == "" {
		reason = redactSecret(sendErr.Error(), password)
	}
	return status, reason, sendErr
}

func (a *App) recordMailAttempts(ctx context.Context, item mail.SubmissionItem, res mailDeliveryResult) {
	st, err := a.mailStore()
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for i, p := range res.Phases {
		_ = st.RecordAttempt(ctx, mail.Attempt{
			ID:     fmt.Sprintf("att-%s-%d", item.ID, i),
			ItemID: item.ID, OwnerScope: item.OwnerScope,
			Phase: p.Phase, Outcome: p.Outcome, Detail: p.Detail,
			StartedAtUTC: now, FinishedAtUTC: now,
		})
	}
}
