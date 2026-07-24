// Package protocol defines the desktop shell's own wire payloads: the types
// that cross the Go↔frontend boundary for features the shell invents rather
// than the engine's host package. Sessions and settings forms, the passport
// account system, the skill / sub-agent / MCP / tool managers, edit review and
// the harm-gate notice all live here.
//
// It complements — and imports — the engine's protocol package, which owns the
// contract for running a turn (assistant deltas, tool events, permission
// requests, turn results, session state, errors, the request envelope). The
// division is by ownership, not by topic: a payload belongs to the engine's
// package when the engine's host package produces or consumes it, and here when
// only this shell does. That keeps a change to a settings form or a manager page
// from requiring an engine release.
//
// The same rules apply as to the engine's package:
//
//   - Imports are restricted to the standard library and the engine's protocol
//     package. No implementation code, so the TypeScript generator can depend on
//     it freely.
//   - Adding a field is always safe: new fields must be optional and carry
//     omitempty in their json tag, so an older peer is unaffected.
//   - Unknown fields must be ignored by both sides.
//
// The frontend's TypeScript mirror of both packages (types.ts / events.ts /
// commands.ts under cmd/runcode-desktop/frontend/src/core/protocol) is generated
// by tools/protogen, which reads this package and the engine's as one contract.
package protocol
