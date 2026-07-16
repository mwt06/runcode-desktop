// Package protocol is the single source of truth for the wire protocol shared
// by the two ends of the desktop app: the Go host and the frontend. Every
// request/response/event payload that crosses that boundary is defined here.
//
// Rules for this package:
//
//   - Imports are restricted to the standard library. It must never import
//     engine/* or internal/* packages, so either end (and any future code
//     generator) can depend on it without pulling in implementation code.
//   - Adding a field is always safe: new fields must be optional and carry
//     omitempty in their json tag, so an older peer is unaffected.
//   - Unknown fields must be ignored by both sides, so either end can be
//     upgraded independently.
package protocol
