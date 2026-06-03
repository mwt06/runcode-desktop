package tool

// Result is the final structured output returned by a tool run.
type Result struct {
	Content  []ResultContent `json:"content,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
	IsError  bool            `json:"is_error,omitempty"`
	// Output is an optional sanitized, display-only rendering of what the tool
	// produced (e.g. a unified diff). When set, the executor surfaces it to the
	// UI instead of deriving a generic excerpt from Content. It is never sent to
	// the model or recorded to telemetry/transcripts.
	Output []OutputLine `json:"-"`
}

// ResultContentType identifies a tool result content block.
type ResultContentType string

const (
	// ResultContentTypeText represents plain text output.
	ResultContentTypeText ResultContentType = "text"
	// ResultContentTypeJSON represents structured JSON-compatible output.
	ResultContentTypeJSON ResultContentType = "json"
)

// ResultContent is one content block in a tool result.
type ResultContent struct {
	Type ResultContentType `json:"type"`
	Text string            `json:"text,omitempty"`
	Data any               `json:"data,omitempty"`
}
