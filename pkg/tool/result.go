package tool

// Result is the final structured output returned by a tool run.
type Result struct {
	Content  []ResultContent `json:"content,omitempty"`
	Metadata map[string]any  `json:"metadata,omitempty"`
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
