package protocol

// Version is the protocol's single monotonically increasing version number.
// It only changes on breaking wire changes (removed/renamed fields, changed
// event semantics); adding optional fields never bumps it. It is exchanged in
// a handshake (GetProtocolInfo today, the transport handshake on a future
// server), never per message.
const Version = 1

// Info is the handshake payload describing the host's protocol support.
type Info struct {
	ProtocolVersion int `json:"protocolVersion"`
	// AppVersion is the host application's release version, informational only.
	AppVersion string `json:"appVersion,omitempty"`
}
