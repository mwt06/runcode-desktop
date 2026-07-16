// Package engine hosts the transport-agnostic session engine: Build assembles
// a Session from a Config (pure data) and Options (behavior ports), and every
// subsystem a shell needs to run conversations lives in this module's public
// packages (llm, tool, turn, permissions, mcp, sessions, tools, ...). The
// ReAct loop and other internals are under engine/internal and stay private —
// consumers depend on the Session facade, not the machinery behind it.
package engine
