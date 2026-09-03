package desktop

import (
	"encoding/json"
	"time"
)

// McpMarket returns the platform MCP market (installable servers) from the
// bridge's GET /api/mcp/market. It needs a logged-in Passport session —
// bridgeGet attaches the login token and fails clearly when not logged in, so
// the market view surfaces that as an error rather than an empty list.
//
// The list is global (not tenant-scoped): the bridge serves it from config
// (bridge.mcp-market), overridable per environment without a client change.
// Installing an entry reuses SaveMCPServer (frontend side), so nothing here
// writes to config.toml.
func (a *App) McpMarket() ([]McpMarketEntry, error) {
	entries, err := a.fetchMarket(30 * time.Second)
	if err != nil {
		return nil, wireError(err)
	}
	a.syncMarketPassport(entries)
	return entries, nil
}

// fetchMarket GETs and decodes the market list.
func (a *App) fetchMarket(timeout time.Duration) ([]McpMarketEntry, error) {
	body, _, err := a.bridgeGetStatusTimeout("/api/mcp/market", timeout)
	if err != nil {
		return nil, err
	}
	var entries []McpMarketEntry
	if err := json.Unmarshal(body, &entries); err != nil {
		return nil, err
	}
	return entries, nil
}

// syncMarketOnce refreshes the market-declared Passport flags at most once per
// run: the platform's list changes on its own schedule, not per session, so
// re-fetching for every New/Resume/Open would put a network
// round-trip on the session-open path for nothing.
//
// It runs on startup and again right after a login (a cold start with no stored
// token cannot fetch anything), and stops trying once a fetch succeeds. Best
// effort throughout: offline, logged out, or an older bridge without the
// endpoint all leave the existing config untouched. Opening the MCP page still
// forces a fresh sync via McpMarket.
func (a *App) syncMarketOnce() {
	a.mu.Lock()
	done := a.marketSynced
	a.mu.Unlock()
	if done || a.tokens == nil || !a.tokens.LoggedIn() {
		return
	}
	entries, err := a.fetchMarket(5 * time.Second)
	if err != nil {
		return // transient: a later login or the MCP page retries
	}
	a.mu.Lock()
	a.marketSynced = true
	a.mu.Unlock()
	a.syncMarketPassport(entries)
}

// syncMarketPassport aligns installed servers with what the market says about
// Passport injection, so a platform-built server (the OA MCP) authenticates as
// the logged-in user without anyone ticking a box — the platform declares it,
// the client honors it. It also repairs entries installed by an older build that
// predates the flag.
//
// Only servers whose name matches a market entry are touched: a third-party
// server someone added by hand is never granted the user's token.
func (a *App) syncMarketPassport(entries []McpMarketEntry) {
	if len(entries) == 0 {
		return
	}
	want := make(map[string]bool, len(entries))
	for _, e := range entries {
		want[e.ID] = e.Passport
	}
	mcpMu.Lock()
	defer mcpMu.Unlock()
	servers, err := a.loadMCPServers()
	if err != nil {
		return
	}
	changed := false
	for name, s := range servers {
		w, ok := want[name]
		if !ok {
			continue // not a market server — leave it alone
		}
		if mcpPassportEnabled(s) != w {
			servers[name] = withMCPPassport(s, w)
			changed = true
		}
	}
	if changed {
		_ = a.writeMCPServers(servers) // best effort: a read-only config must not fail the listing
	}
}
