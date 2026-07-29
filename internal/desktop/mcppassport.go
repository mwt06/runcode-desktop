package desktop

import (
	"os"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/mcp"
	"gitlab.ouc-online.com.cn/aibase/agentloop/settings"
)

// Passport identity injection for MCP servers that opt in (passport = true in
// config.toml). The desktop attaches a per-request header source so a platform
// server like the OA MCP receives the logged-in user's fresh Passport token and
// selected tenant — each request re-evaluates both, so a token refresh or a
// mid-session tenant switch applies to the next tool call without reconnecting.
//
// Strictly opt-in: only servers marked passport=true (by the user or the market
// install) get the token. A user's Passport token must never be sent to an
// arbitrary third-party endpoint someone configured here.

// passportKey is the config.toml key marking a server as ours. The engine stores
// it verbatim in MCPServerConfig.Extra and never interprets it: "this server is
// part of our product, hand it the user's identity" is a statement only the
// product can make.
const passportKey = "passport"

// mcpPassportEnabled reports whether a server opted in.
func mcpPassportEnabled(s settings.MCPServerConfig) bool {
	on, _ := s.Extra[passportKey].(bool)
	return on
}

// withMCPPassport returns s with the opt-in set, leaving any other host keys in
// Extra untouched.
func withMCPPassport(s settings.MCPServerConfig, on bool) settings.MCPServerConfig {
	extra := make(map[string]any, len(s.Extra)+1)
	for k, v := range s.Extra {
		extra[k] = v
	}
	extra[passportKey] = on
	s.Extra = extra
	return s
}

// passportMCPNames returns the set of MCP server names that opted in to Passport
// injection, read from the user-level config.toml (the same source the MCP
// management page uses). It is the single source for both things that follow from
// opting in — the identity headers and the approval bypass — so the two can never
// disagree about which servers are ours.
func passportMCPNames() map[string]bool {
	dir, err := os.UserConfigDir()
	if err != nil {
		return nil
	}
	res, err := settings.Load(settings.LoadOptions{UserConfigDir: dir})
	if err != nil {
		return nil
	}
	out := map[string]bool{}
	for name, s := range res.Config.MCP.Servers {
		if mcpPassportEnabled(s) {
			out[name] = true
		}
	}
	return out
}

// applyMCPPassport attaches the per-request identity headers to every server named
// in names. The other half of opting in — skipping the per-call approval an
// arbitrary external server needs — is applied where the permission service is
// built (configureSession), from this same name set: the engine has no notion of
// which servers are ours, so both halves are the host's to declare. Pure, so the
// gating is unit-tested without config or token I/O.
func applyMCPPassport(servers []mcp.ServerConfig, names map[string]bool, headers func() (map[string]string, error)) {
	for i := range servers {
		if names[servers[i].Name] {
			servers[i].HeaderSource = headers
		}
	}
}

// passportHeaders builds the injected headers from a token function and the
// selected tenant. A token error propagates (the caller fails the request rather
// than sending it unauthenticated); an empty tenant omits X-Tenant-Id.
func passportHeaders(token func() (string, error), tenant string) (map[string]string, error) {
	tok, err := token()
	if err != nil {
		return nil, err
	}
	h := map[string]string{"Authorization": "Bearer " + tok}
	if t := strings.TrimSpace(tenant); t != "" {
		h["X-Tenant-Id"] = t
	}
	return h, nil
}

// attachMCPPassport wires the Passport header source onto the opted-in servers.
// Called after the engine config is built, on both session start and a live
// settings rebuild, so passport MCPs keep authenticating across a model switch.
func (a *App) attachMCPPassport(servers []mcp.ServerConfig) {
	if len(servers) == 0 {
		return
	}
	names := passportMCPNames()
	if len(names) == 0 {
		return
	}
	applyMCPPassport(servers, names, a.mcpPassportHeaders)
}

// mcpPassportHeaders is the per-request header source: the token is fetched fresh
// each call (tokenManager refreshes it proactively) and the tenant read live.
func (a *App) mcpPassportHeaders() (map[string]string, error) {
	a.mu.Lock()
	tenant := a.passportTenant
	a.mu.Unlock()
	return passportHeaders(a.tokens.Token, tenant)
}
