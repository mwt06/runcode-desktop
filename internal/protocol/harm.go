package protocol

// HarmAutoAllow is the payload of EventHarmAutoAllow: a sanitized record of a
// harm-gate decision (no raw command or path). Outcome is "auto_allowed" or
// "breaker_tripped"; Count is how many actions smart mode has auto-allowed this
// session.
type HarmAutoAllow struct {
	Tool      string `json:"tool"`
	ToolUseID string `json:"toolUseID"`
	Operation string `json:"operation"`
	Risk      string `json:"risk"`
	Reason    string `json:"reason"`
	Outcome   string `json:"outcome"`
	Count     int    `json:"count"`
}
