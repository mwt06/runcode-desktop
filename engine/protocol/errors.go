package protocol

import "encoding/json"

// Error codes. Clients switch on Code; Message is human-readable and may be
// localized by the host.
const (
	// ErrCodeNoSession: the command needs an active session and none exists.
	ErrCodeNoSession = "no_session"
	// ErrCodeBusy: a turn is in progress and the command cannot run concurrently.
	ErrCodeBusy = "busy"
	// ErrCodeInvalidArgument: the request failed validation.
	ErrCodeInvalidArgument = "invalid_argument"
	// ErrCodeNotFound: the referenced entity (session, request id, ...) does not exist.
	ErrCodeNotFound = "not_found"
	// ErrCodeNotLoggedIn: the command needs an authenticated passport session.
	ErrCodeNotLoggedIn = "not_logged_in"
	// ErrCodeUnavailable: a required capability is missing in this environment
	// (e.g. no native dialog support).
	ErrCodeUnavailable = "unavailable"
	// ErrCodeInternal: anything else.
	ErrCodeInternal = "internal"
)

// Error is the wire form of a command failure. Hosts serialize it as the
// rejection value; a client that receives a plain string (a host that has not
// adopted structured errors for that command yet) must wrap it as an
// ErrCodeInternal Error rather than fail.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	// Details carries optional structured context for specific codes.
	Details map[string]string `json:"details,omitempty"`
}

// Error implements the error interface. It deliberately returns the JSON
// serialization of the whole value, not just Message: string-only transports
// (Wails serializes a rejected command's error via Error()) then still deliver
// the structured {code, message} to the client, which parses the JSON and — per
// the contract above — wraps anything unparseable as an ErrCodeInternal Error.
// Code that needs the bare human-readable text must read .Message, never Error().
func (e Error) Error() string {
	b, err := json.Marshal(e)
	if err != nil {
		return e.Message
	}
	return string(b)
}
