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

// passportMCPNames returns the set of MCP server names that opted in to Passport
// injection, read from the user-level config.toml (the same source the MCP
// management page uses).
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
		if s.Passport != nil && *s.Passport {
			out[name] = true
		}
	}
	return out
}

// applyMCPPassport marks every server named in names as the platform's own: it
// gets the per-request identity headers, and it is Trusted, so its tool calls run
// without the per-call approval an arbitrary external server needs. Both follow
// from the same fact — the platform ships this server and vouches for it — so
// they are set together and never for a third-party endpoint. Pure, so the gating
// is unit-tested without config or token I/O.
func applyMCPPassport(servers []mcp.ServerConfig, names map[string]bool, headers func() (map[string]string, error)) {
	for i := range servers {
		if names[servers[i].Name] {
			servers[i].HeaderSource = headers
			servers[i].Trusted = true
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
