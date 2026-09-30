package desktop

import (
	"context"

	"github.com/wt68/runcode/internal/codexproxy"
	engine "gitlab.ouc-online.com.cn/aibase/agentloop"
	"gitlab.ouc-online.com.cn/aibase/agentloop/llm"
)

func (a *App) visionCodexIdentity() string {
	a.codex.mu.Lock()
	defer a.codex.mu.Unlock()
	return digestVision([]string{a.codex.ts.AccountID, a.codex.ts.Email, tokenIdentity(a.codex.ts.Access)})
}

// Unlike ordinary chat's dynamically resolved Codex profiles, an image turn
// must never change its destination when the settings page edits a profile.
func (a *App) visionProvider(cfg engine.Config, ref modelReference, stored desktopConfig, check func(context.Context) error) (llm.Provider, func(), error) {
	cleanup := func() {}
	if isCodexProfile(cfg.Provider) {
		cm, err := resolveCustomModelFrom(stored, ref.Name)
		if err != nil {
			return nil, cleanup, err
		}
		upstream := a.codexUpstream(cm)
		bearer := upstream.Bearer
		upstream.Bearer = func() (string, error) {
			if err := check(context.Background()); err != nil {
				return "", err
			}
			token, err := bearer()
			if err != nil {
				return "", err
			}
			if err := check(context.Background()); err != nil {
				return "", err
			}
			return token, nil
		}
		server, err := codexproxy.Start(func(profile string) (codexproxy.Upstream, bool) { return upstream, profile == "image-analysis" }, proxyFromSettings)
		if err != nil {
			return nil, cleanup, err
		}
		cleanup = func() { _ = server.Close() }
		cfg.Provider, cfg.BaseURL, cfg.APIKey = engineProviderForCodex, server.BaseURL("image-analysis"), codexProxyPlaceholderKey
	}
	// Retry inside Client, where every attempt checks the live OA/account gate.
	cfg.MaxRetries = -1
	cfg.OnUnauthorized = nil // Client owns the single refresh attempt and its live gate.
	if cfg.Provider == "openai" || cfg.Provider == "openai-responses" {
		source := cfg.TokenSource
		key := cfg.APIKey
		if cfg.AuthToken != "" {
			key = cfg.AuthToken
		}
		cfg.TokenSource = func() (string, error) {
			if err := check(context.Background()); err != nil {
				return "", err
			}
			token := key
			if source != nil {
				var err error
				token, err = source()
				if err != nil {
					return "", err
				}
			}
			if err := check(context.Background()); err != nil {
				return "", err
			}
			return token, nil
		}
	}
	provider, err := engine.BuildProvider(cfg)
	if err != nil {
		cleanup()
		return nil, func() {}, err
	}
	return provider, cleanup, nil
}
