package desktop

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/wt68/runcode/internal/protocol"
)

// 可替换钩子：测试强制保护失败或打开 URL，不改系统钥匙串。
var (
	mailProtectSecret   = protectSecret
	mailUnprotectSecret = unprotectSecret
	mailOpenBrowser     = openBrowser
)

const (
	mailSMTPPort         = 465
	mailConnectionBudget = 15 * time.Second
	mailTestSendBudget   = 20 * time.Second
	mailPassedTestTTL    = 30 * time.Minute
)

var (
	errMailExiting = errors.New("应用正在退出，无法继续邮箱操作")
	errMailBusy    = errors.New("邮箱连接测试进行中，请稍候")
)

var mailTestFlight atomic.Bool

type mailPassedTest struct {
	fingerprint string
	address     string
	version     string
	expires     time.Time
}

var mailPassedMu sync.Mutex
var mailPassedByFP = map[string]mailPassedTest{}

var mailSelfTestMu sync.Mutex
var mailSelfTestByClient = map[string]protocol.MailTestSendResult{}

func mailRefuseIfExiting(a *App) error {
	if a != nil && a.exit.closing.Load() {
		return errMailExiting
	}
	return nil
}

// mailSMTPHost 按版本返回固定官方主机；不接受任意主机覆盖。
func mailSMTPHost(version string) (string, error) {
	switch strings.TrimSpace(version) {
	case protocol.MailVersionStandard, "":
		return "smtp.qiye.aliyun.com", nil
	case protocol.MailVersionXC:
		return "smtp.xc.aliyun.com", nil
	case protocol.MailVersionHK:
		return "smtphk.qiye.aliyun.com", nil
	default:
		return "", fmt.Errorf("不支持的邮箱版本 %q（可选：standard、xc、hk）", version)
	}
}

func mailOfficialURL(page string) (string, error) {
	switch strings.TrimSpace(page) {
	case "webmail_standard":
		return "https://qiye.aliyun.com/", nil
	case "webmail_xc":
		return "https://mail.xc.aliyun.com/", nil
	case "help_security_password":
		return "https://help.aliyun.com/zh/document_detail/444269.html", nil
	default:
		return "", errors.New("只能打开预设的官方邮箱页面")
	}
}

func normalizeMailAddress(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", errors.New("请填写完整邮箱地址，包含 @ 与单位域名")
	}
	addr, err := mail.ParseAddress(raw)
	if err != nil || addr.Address == "" || !strings.Contains(addr.Address, "@") {
		return "", errors.New("邮箱地址格式不正确")
	}
	return strings.ToLower(addr.Address), nil
}

func mailConfigFingerprint(address, version, password string) string {
	sum := sha256.Sum256([]byte(strings.ToLower(strings.TrimSpace(address)) + "\n" + strings.TrimSpace(version) + "\n" + password))
	return hex.EncodeToString(sum[:])
}

func rememberMailPassedTest(fp, address, version string) {
	mailPassedMu.Lock()
	defer mailPassedMu.Unlock()
	now := time.Now()
	for k, v := range mailPassedByFP {
		if now.After(v.expires) {
			delete(mailPassedByFP, k)
		}
	}
	mailPassedByFP[fp] = mailPassedTest{
		fingerprint: fp,
		address:     address,
		version:     version,
		expires:     now.Add(mailPassedTestTTL),
	}
}

func lookupMailPassedTest(fp string) (mailPassedTest, bool) {
	mailPassedMu.Lock()
	defer mailPassedMu.Unlock()
	v, ok := mailPassedByFP[fp]
	if !ok || time.Now().After(v.expires) {
		delete(mailPassedByFP, fp)
		return mailPassedTest{}, false
	}
	return v, true
}

func publicMailAccount(rec *mailAccountRecord) *protocol.MailAccountView {
	if rec == nil {
		return nil
	}
	return &protocol.MailAccountView{
		ID:                   rec.ID,
		Address:              rec.Address,
		DisplayName:          rec.DisplayName,
		Version:              rec.Version,
		Host:                 rec.Host,
		Port:                 rec.Port,
		Enabled:              rec.Enabled,
		HasPassword:          rec.PasswordProtected != "",
		Revision:             rec.Revision,
		CredentialRevision:   rec.CredentialRevision,
		OnboardingStatus:     rec.OnboardingStatus,
		LastConnectionTestAt: rec.LastConnectionTestAt,
		TestMailAcceptedAt:   rec.TestMailAcceptedAt,
		ReceiptConfirmedAt:   rec.ReceiptConfirmedAt,
		LastTestShortID:      rec.LastTestShortID,
		LastTestSubmissionID: rec.LastTestSubmissionID,
	}
}

// GetMailSettings 返回脱敏邮箱设置；不含密码。
func (a *App) GetMailSettings() (protocol.MailSettings, error) {
	cfg := loadRawConfig()
	return protocol.MailSettings{
		Account:         publicMailAccount(cfg.MailAccount),
		SecretStorageOK: a.SecretStorageStatus().OK,
	}, nil
}

// TestMailConnection 用当前表单探测 SMTP：连接/TLS/认证后退出，不发信、不落盘。
func (a *App) TestMailConnection(req protocol.TestMailConnectionRequest) (protocol.MailConnectionTestResult, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailConnectionTestResult{}, wireError(err)
	}
	if !mailTestFlight.CompareAndSwap(false, true) {
		return protocol.MailConnectionTestResult{}, wireError(errMailBusy)
	}
	defer mailTestFlight.Store(false)

	address, err := normalizeMailAddress(req.Address)
	if err != nil {
		return protocol.MailConnectionTestResult{OK: false, ErrorCategory: "address", Message: err.Error()}, nil
	}
	if req.Password == "" {
		return protocol.MailConnectionTestResult{OK: false, ErrorCategory: "auth", Message: "请粘贴专用安全密码后再测试"}, nil
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = protocol.MailVersionStandard
	}
	host, err := mailSMTPHost(version)
	if err != nil {
		return protocol.MailConnectionTestResult{OK: false, ErrorCategory: "version", Message: err.Error()}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), mailConnectionBudget)
	defer cancel()
	phase := "connecting"
	err = mailSMTP.TestLogin(ctx, host, mailSMTPPort, address, req.Password)
	if err != nil {
		cat, msg := classifyMailSMTPError(err)
		return protocol.MailConnectionTestResult{
			OK: false, Phase: phase, ErrorCategory: cat, Message: msg,
		}, nil
	}
	now := time.Now().UTC()
	fp := mailConfigFingerprint(address, version, req.Password)
	rememberMailPassedTest(fp, address, version)
	return protocol.MailConnectionTestResult{
		OK: true, Phase: "done", TestedAtUTC: now.Format(time.RFC3339Nano),
		ConfigFingerprint: fp,
		Message:           "连接测试通过。已用当前填写的账号登录发信服务器；尚未保存，也未发送邮件。",
	}, nil
}

// SaveMailAccount 仅在同配置连接测试通过且保护成功时原子写入。
func (a *App) SaveMailAccount(req protocol.SaveMailAccountRequest) (protocol.MailSettings, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	address, err := normalizeMailAddress(req.Address)
	if err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	if req.ClearPassword && req.Password != "" {
		return protocol.MailSettings{}, wireError(errors.New("不能同时提交密码与清空密码"))
	}
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = protocol.MailVersionStandard
	}
	host, err := mailSMTPHost(version)
	if err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	if strings.TrimSpace(req.ConfigFingerprint) == "" {
		return protocol.MailSettings{}, wireError(errors.New("请先用当前填写的账号完成连接测试"))
	}
	passed, ok := lookupMailPassedTest(req.ConfigFingerprint)
	if !ok || passed.address != address || passed.version != version {
		return protocol.MailSettings{}, wireError(errors.New("连接测试已失效：邮箱、版本或密码变更后请重新测试"))
	}

	var out protocol.MailSettings
	err = updateRawConfig(func(cfg *desktopConfig) error {
		prev := cfg.MailAccount
		rev := int64(0)
		credRev := int64(0)
		id := ""
		oldProtected := ""
		if prev != nil {
			if prev.Revision != req.ExpectedRevision {
				return fmt.Errorf("邮箱配置已变更（修订 %d），请刷新后重试", prev.Revision)
			}
			rev = prev.Revision
			credRev = prev.CredentialRevision
			id = prev.ID
			oldProtected = prev.PasswordProtected
		} else if req.ExpectedRevision != 0 {
			return errors.New("邮箱配置已变更，请刷新后重试")
		}
		if id == "" {
			id = "mail-" + shortRandomID(8)
		}

		passwordPlain := req.Password
		protected := ""
		switch {
		case req.ClearPassword:
			protected = ""
			credRev++
		case passwordPlain != "":
			p, ok := mailProtectSecret(passwordPlain)
			if !ok {
				return errors.New("当前系统无法安全保存邮箱密码，请检查系统凭据存储后重试")
			}
			// 指纹必须与本次明文一致（已在上方用 passed 校验地址/版本）。
			if mailConfigFingerprint(address, version, passwordPlain) != req.ConfigFingerprint {
				return errors.New("连接测试与当前密码不一致，请重新测试")
			}
			protected = p
			if oldProtected == "" {
				credRev++
			} else if old, ok := mailUnprotectSecret(oldProtected); !ok || old != passwordPlain {
				credRev++
			}
		default:
			if oldProtected == "" {
				return errors.New("请填写专用安全密码")
			}
			old, ok := mailUnprotectSecret(oldProtected)
			if !ok {
				return errors.New("已保存的邮箱密码无法解密，请重新输入专用安全密码并测试")
			}
			if mailConfigFingerprint(address, version, old) != req.ConfigFingerprint {
				return errors.New("连接测试已失效：请用当前密码重新测试")
			}
			protected = oldProtected
		}

		now := time.Now().UTC().Format(time.RFC3339Nano)
		rec := &mailAccountRecord{
			ID:                   id,
			Address:              address,
			DisplayName:          strings.TrimSpace(req.DisplayName),
			Version:              version,
			Host:                 host,
			Port:                 mailSMTPPort,
			PasswordProtected:    protected,
			Enabled:              req.Enabled,
			Revision:             rev + 1,
			CredentialRevision:   credRev,
			OnboardingStatus:     protocol.MailStatusSaved,
			LastConnectionTestAt: now,
			LastTestFingerprint:  req.ConfigFingerprint,
		}
		if prev != nil {
			// 换地址/服务器/密码后旧收信确认失效；仅显示名变更可保留自测进度。
			if prev.Address == address && prev.Version == version && prev.PasswordProtected == protected {
				rec.TestMailAcceptedAt = prev.TestMailAcceptedAt
				rec.ReceiptConfirmedAt = prev.ReceiptConfirmedAt
				rec.LastTestShortID = prev.LastTestShortID
				rec.LastTestSubmissionID = prev.LastTestSubmissionID
				switch prev.OnboardingStatus {
				case protocol.MailStatusReceiptConfirmed:
					rec.OnboardingStatus = protocol.MailStatusReceiptConfirmed
				case protocol.MailStatusTestMailAccepted:
					rec.OnboardingStatus = protocol.MailStatusTestMailAccepted
				}
			}
		}
		cfg.MailAccount = rec
		out = protocol.MailSettings{Account: publicMailAccount(rec), SecretStorageOK: a.SecretStorageStatus().OK}
		return nil
	})
	if err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	return out, nil
}

// DisableMailAccount 禁用账号但保留密文配置。
func (a *App) DisableMailAccount(req protocol.DisableMailAccountRequest) (protocol.MailSettings, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	var out protocol.MailSettings
	err := updateRawConfig(func(cfg *desktopConfig) error {
		if cfg.MailAccount == nil {
			return errors.New("尚未配置邮箱账号")
		}
		if cfg.MailAccount.Revision != req.ExpectedRevision {
			return fmt.Errorf("邮箱配置已变更（修订 %d），请刷新后重试", cfg.MailAccount.Revision)
		}
		cfg.MailAccount.Enabled = false
		cfg.MailAccount.Revision++
		cfg.MailAccount.OnboardingStatus = protocol.MailStatusNeedsAttention
		out = protocol.MailSettings{Account: publicMailAccount(cfg.MailAccount), SecretStorageOK: a.SecretStorageStatus().OK}
		return nil
	})
	if err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	return out, nil
}

// RemoveMailAccount 删除账号与密文。
func (a *App) RemoveMailAccount(req protocol.RemoveMailAccountRequest) (protocol.MailSettings, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	err := updateRawConfig(func(cfg *desktopConfig) error {
		if cfg.MailAccount == nil {
			return nil
		}
		if cfg.MailAccount.Revision != req.ExpectedRevision {
			return fmt.Errorf("邮箱配置已变更（修订 %d），请刷新后重试", cfg.MailAccount.Revision)
		}
		cfg.MailAccount = nil
		return nil
	})
	if err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	return protocol.MailSettings{SecretStorageOK: a.SecretStorageStatus().OK}, nil
}

// SendMailTestToSelf 向已保存账号自身发送固定测试信；不接受外部收件人。
func (a *App) SendMailTestToSelf(req protocol.SendMailTestToSelfRequest) (protocol.MailTestSendResult, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailTestSendResult{}, wireError(err)
	}
	clientID := strings.TrimSpace(req.ClientRequestID)
	if clientID != "" {
		mailSelfTestMu.Lock()
		if prev, ok := mailSelfTestByClient[clientID]; ok {
			mailSelfTestMu.Unlock()
			return prev, nil
		}
		mailSelfTestMu.Unlock()
	}

	cfg := loadRawConfig()
	rec := cfg.MailAccount
	if rec == nil || !rec.Enabled {
		return protocol.MailTestSendResult{}, wireError(errors.New("请先保存并启用邮箱账号"))
	}
	if rec.Revision != req.ExpectedRevision {
		return protocol.MailTestSendResult{}, wireError(fmt.Errorf("邮箱配置已变更（修订 %d），请刷新后重试", rec.Revision))
	}
	if rec.PasswordProtected == "" {
		return protocol.MailTestSendResult{}, wireError(errors.New("邮箱密码未保存"))
	}
	password, ok := mailUnprotectSecret(rec.PasswordProtected)
	if !ok {
		return protocol.MailTestSendResult{}, wireError(errors.New("邮箱密码无法解密，请重新保存"))
	}

	shortID := shortRandomID(6)
	subject := fmt.Sprintf("智开邮箱接入测试－%s", shortID)
	body := fmt.Sprintf("这是一封智开邮箱接入测试邮件。\n短编号：%s\n发送时间（UTC）：%s\n\n请到同一邮箱网页收件箱查找该短编号，确认收到后再回到智开点「我已收到」。\n本邮件不含密码或会话内容。\n",
		shortID, time.Now().UTC().Format(time.RFC3339))
	submissionID := "mail-test-" + shortRandomID(10)

	ctx, cancel := context.WithTimeout(context.Background(), mailTestSendBudget)
	defer cancel()
	status, detail, err := mailSMTP.SendSelfTest(ctx, rec.Host, rec.Port, rec.Address, password, subject, body)
	_ = detail
	result := protocol.MailTestSendResult{
		Status: status, ShortID: shortID, SubmissionID: submissionID,
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	switch status {
	case "accepted":
		result.AcceptedAt = now
		result.Message = "发信服务器已接受测试邮件，请到邮箱网页检查收件箱。尚未确认收信。"
	case "unknown":
		result.Message = "发送结果待核实：邮件可能已发出。请先按短编号搜索，不要自动重发。"
		result.ErrorCategory = "unknown"
	default:
		cat, msg := classifyMailSMTPError(err)
		result.ErrorCategory = cat
		result.Message = msg
		if result.Message == "" {
			result.Message = "测试邮件发送失败"
		}
	}

	if status == "accepted" || status == "unknown" {
		_ = updateRawConfig(func(cfg *desktopConfig) error {
			if cfg.MailAccount == nil || cfg.MailAccount.ID != rec.ID {
				return errors.New("邮箱账号已变更")
			}
			if cfg.MailAccount.Revision != req.ExpectedRevision {
				return fmt.Errorf("邮箱配置已变更（修订 %d）", cfg.MailAccount.Revision)
			}
			cfg.MailAccount.LastTestShortID = shortID
			cfg.MailAccount.LastTestSubmissionID = submissionID
			cfg.MailAccount.Revision++
			if status == "accepted" {
				cfg.MailAccount.TestMailAcceptedAt = now
				cfg.MailAccount.OnboardingStatus = protocol.MailStatusTestMailAccepted
				cfg.MailAccount.ReceiptConfirmedAt = ""
			} else {
				cfg.MailAccount.OnboardingStatus = protocol.MailStatusNeedsAttention
			}
			return nil
		})
	}

	if clientID != "" {
		mailSelfTestMu.Lock()
		mailSelfTestByClient[clientID] = result
		mailSelfTestMu.Unlock()
	}
	return result, nil
}

// ConfirmMailTestReceipt 记录本人核实收到测试信。
func (a *App) ConfirmMailTestReceipt(req protocol.ConfirmMailTestReceiptRequest) (protocol.MailSettings, error) {
	if err := mailRefuseIfExiting(a); err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	var out protocol.MailSettings
	err := updateRawConfig(func(cfg *desktopConfig) error {
		if cfg.MailAccount == nil {
			return errors.New("尚未配置邮箱账号")
		}
		if cfg.MailAccount.Revision != req.ExpectedRevision {
			return fmt.Errorf("邮箱配置已变更（修订 %d），请刷新后重试", cfg.MailAccount.Revision)
		}
		if cfg.MailAccount.LastTestShortID == "" || cfg.MailAccount.LastTestShortID != strings.TrimSpace(req.ShortID) {
			return errors.New("短编号与最近一次测试邮件不一致")
		}
		if cfg.MailAccount.OnboardingStatus != protocol.MailStatusTestMailAccepted && cfg.MailAccount.TestMailAcceptedAt == "" {
			return errors.New("请先完成测试邮件发送（服务器已接受）后再确认收信")
		}
		cfg.MailAccount.ReceiptConfirmedAt = time.Now().UTC().Format(time.RFC3339Nano)
		cfg.MailAccount.OnboardingStatus = protocol.MailStatusReceiptConfirmed
		cfg.MailAccount.Revision++
		out = protocol.MailSettings{Account: publicMailAccount(cfg.MailAccount), SecretStorageOK: a.SecretStorageStatus().OK}
		return nil
	})
	if err != nil {
		return protocol.MailSettings{}, wireError(err)
	}
	return out, nil
}

// OpenMailOfficialURL 只打开固定官方页面。
func (a *App) OpenMailOfficialURL(req protocol.OpenMailOfficialURLRequest) error {
	if err := mailRefuseIfExiting(a); err != nil {
		return wireError(err)
	}
	url, err := mailOfficialURL(req.Page)
	if err != nil {
		return wireError(err)
	}
	if err := mailOpenBrowser(url); err != nil {
		return wireError(fmt.Errorf("无法打开浏览器：%w", err))
	}
	return nil
}

func shortRandomID(n int) string {
	if n <= 0 {
		n = 6
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

func resetMailTestCachesForTest() {
	mailPassedMu.Lock()
	mailPassedByFP = map[string]mailPassedTest{}
	mailPassedMu.Unlock()
	mailSelfTestMu.Lock()
	mailSelfTestByClient = map[string]protocol.MailTestSendResult{}
	mailSelfTestMu.Unlock()
	mailTestFlight.Store(false)
}
