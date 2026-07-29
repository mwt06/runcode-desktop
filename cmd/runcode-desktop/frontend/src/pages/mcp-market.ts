// Pure helpers for the MCP market view: turn a market entry into the payload
// SaveMCPServer expects, and decide whether an entry is already installed. Kept
// out of the component so both are unit-testable (see mcp-market.test.ts).
import type { McpMarketEntry, MCPServerInfo, MCPServerInput } from '@/core/bridge'

// marketEntryToInput maps a market entry to a config.toml server entry. It carries
// the passport flag through so a platform server (e.g. the OA MCP) gets the
// logged-in user's token injected per request; third-party entries with passport
// false stay anonymous. id is the server name so re-installing updates the same
// entry and isInstalled can match it. enabled=true so it connects on the next new
// session.
export function marketEntryToInput(e: McpMarketEntry): MCPServerInput {
  return {
    originalName: '',
    name: e.id,
    transport: e.transport || 'http',
    command: '',
    args: [],
    env: {},
    dir: '',
    url: e.url,
    headers: {},
    passport: e.passport,
    enabled: true,
  }
}

// MarketInstallState is how a market entry relates to the installed servers:
// 'none' (not installed), 'installed' (present and still matches what the market
// prescribes), or 'outdated' (present but drifted — e.g. installed before it
// carried passport, or the market changed the url). 'outdated' lets the market
// offer a one-click 更新 instead of a dead "已安装", so a platform server always
// gets its passport injection without the user touching the manual checkbox.
export type MarketInstallState = 'none' | 'installed' | 'outdated'

export function installState(e: McpMarketEntry, servers: MCPServerInfo[]): MarketInstallState {
  const s = servers.find((x) => x.name === e.id)
  if (!s) return 'none'
  const matches =
    s.url === e.url &&
    s.passport === e.passport &&
    (s.transport || 'http') === (e.transport || 'http')
  return matches ? 'installed' : 'outdated'
}
