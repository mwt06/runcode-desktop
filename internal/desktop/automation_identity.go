package desktop

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/wt68/runcode/internal/automation"
)

const (
	automationKindPlatform = "platform"
	automationKindCustom   = "custom"
	automationKindCodex    = "codex"
	automationBindVersion  = 1
)

var (
	errAutomationLeased          = errors.New("会话正在执行自动化任务，不能更改、关闭或另行提交")
	errAutomationIdentityChanged = errors.New("任务绑定的账号或密钥已变化")
	errAutomationUnbound         = errors.New("缺少稳定身份，请先保存连接或重新登录后再创建自动化任务")
	errAutomationCodexSubject    = errors.New("ChatGPT 登录缺少稳定身份，请重新登录")
	errAutomationBusy            = errors.New("目标会话正忙，本次自动化不插入进行中的回合")
	errAutomationDrift           = errors.New("可复用会话的模型或权限已变化，已阻止本次运行")
	errAutomationSessionUnknown  = errors.New("无法确认关联会话是否已删除，已阻止本次运行")
)

// automationCredentialGap 在身份核对和再次读取凭据之间留一个口子。
// 测试用它把账号换成另一个，确认返回值不会带上新账号的令牌。
var automationCredentialGap func()

// effectiveCredentialRevision 把旧配置上的空版本解释成稳定的初值。
// 已经有密钥时是 1，没有密钥时是 0。真正换了密钥才在这个数上加一。
func effectiveCredentialRevision(stored int64, hasKey bool) int64 {
	if stored > 0 {
		return stored
	}
	if hasKey {
		return 1
	}
	return 0
}

func automationDigest(parts ...string) string {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			_, _ = h.Write([]byte{0})
		}
		_, _ = h.Write([]byte(p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

type automationPrincipal struct {
	kind       string
	issuer     string
	subject    string
	account    string
	tenant     string
	bridge     string
	provider   string
	baseURL    string
	modelID    string
	profile    string
	permission string
	revision   int64
	static     bool
}

func (p automationPrincipal) digest() string {
	switch p.kind {
	case automationKindPlatform:
		return automationDigest("platform", p.issuer, p.subject, p.bridge, p.tenant, p.modelID)
	case automationKindCodex:
		if p.static {
			return automationDigest("codex", "apikey", p.provider, p.baseURL, p.modelID, p.profile)
		}
		return automationDigest("codex", "chatgpt", p.issuer, p.subject, p.account, p.profile, p.modelID)
	default:
		return automationDigest("custom", p.provider, p.baseURL, p.modelID, p.profile)
	}
}

func (p automationPrincipal) binding() automation.Binding {
	rev := int64(0)
	if p.static {
		rev = p.revision
	}
	return automation.Binding{
		ModelKind:          p.kind,
		ModelName:          p.profile,
		Bridge:             p.bridge,
		TenantID:           p.tenant,
		PermissionMode:     p.permission,
		ConnectionDigest:   p.digest(),
		CredentialRevision: rev,
		BindingVersion:     automationBindVersion,
	}
}

// freezeAutomationBinding 按此刻的登录或已保存的密钥做一份不含秘密的绑定。
// name 对平台是模型 id，对自定义和 Codex 是配置名。
//
//nolint:unparam // permission is part of the durable binding contract.
func (a *App) freezeAutomationBinding(kind, name, permission string) (automation.Binding, error) {
	p, err := a.automationPrincipal(kind, name, permission)
	if err != nil {
		return automation.Binding{}, err
	}
	return p.binding(), nil
}

// validateAutomationBinding 在每次开跑前重算身份。对不上就停，不用新的令牌或密钥。
func (a *App) validateAutomationBinding(b automation.Binding) error {
	if b.BindingVersion != automationBindVersion || b.ConnectionDigest == "" || b.ModelKind == "" {
		return errAutomationUnbound
	}
	p, err := a.automationPrincipal(b.ModelKind, b.ModelName, b.PermissionMode)
	if err != nil {
		return err
	}
	if p.digest() != b.ConnectionDigest {
		return errAutomationIdentityChanged
	}
	if p.static && p.revision != b.CredentialRevision {
		return errAutomationIdentityChanged
	}
	return nil
}

func (a *App) automationPrincipal(kind, name, permission string) (automationPrincipal, error) {
	kind = strings.TrimSpace(kind)
	name = strings.TrimSpace(name)
	p := automationPrincipal{kind: kind, permission: strings.TrimSpace(permission), profile: name}
	switch kind {
	case automationKindPlatform:
		if name == "" {
			return automationPrincipal{}, errAutomationUnbound
		}
		iss, sub, err := a.passportPrincipal()
		if err != nil {
			return automationPrincipal{}, err
		}
		p.issuer, p.subject = iss, sub
		p.modelID = name
		p.profile = name
		p.bridge = strings.TrimRight(passportConfig().BridgeBaseURL, "/")
		p.tenant = a.passportTenantNow()
		if p.subject == "" || p.tenant == "" {
			return automationPrincipal{}, errAutomationUnbound
		}
		return p, nil
	case automationKindCustom, automationKindCodex:
		if name == "" {
			return automationPrincipal{}, errAutomationUnbound
		}
		cm, err := a.resolveCustomModel(name)
		if err != nil {
			return automationPrincipal{}, err
		}
		p.provider = cm.Provider
		p.baseURL = strings.TrimSpace(cm.BaseURL)
		p.modelID = cm.Model
		p.profile = cm.Name
		if kind == automationKindCodex || isCodexProfile(cm.Provider) {
			p.kind = automationKindCodex
			if strings.EqualFold(cm.AuthMode, codexAuthAPIKey) {
				p.static = true
				p.revision = effectiveCredentialRevision(cm.CredentialRevision, cm.APIKey != "")
				if p.revision == 0 {
					return automationPrincipal{}, errAutomationUnbound
				}
				return p, nil
			}
			iss, sub, account, err := a.codexPrincipal()
			if err != nil {
				return automationPrincipal{}, err
			}
			p.issuer, p.subject, p.account = iss, sub, account
			return p, nil
		}
		p.kind = automationKindCustom
		p.static = true
		p.revision = effectiveCredentialRevision(cm.CredentialRevision, cm.APIKey != "")
		if p.revision == 0 {
			return automationPrincipal{}, errAutomationUnbound
		}
		return p, nil
	default:
		return automationPrincipal{}, errAutomationUnbound
	}
}

func (a *App) passportTenantNow() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return strings.TrimSpace(a.passportTenant)
}

func (a *App) passportPrincipal() (issuer, subject string, err error) {
	a.tokens.mu.Lock()
	access := a.tokens.ts.AccessToken
	a.tokens.mu.Unlock()
	issuer, subject = tokenIdentityParts(access)
	if subject == "" {
		return "", "", errAutomationUnbound
	}
	return issuer, subject, nil
}

func (a *App) codexPrincipal() (issuer, subject, account string, err error) {
	a.codex.mu.Lock()
	ts := a.codex.ts
	a.codex.mu.Unlock()
	if ts.Access == "" && ts.Refresh == "" {
		return "", "", "", errAutomationUnbound
	}
	if strings.TrimSpace(ts.Subject) == "" {
		return "", "", "", errAutomationCodexSubject
	}
	return ts.Issuer, ts.Subject, ts.AccountID, nil
}
