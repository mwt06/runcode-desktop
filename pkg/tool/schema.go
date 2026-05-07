package tool

// Schema describes a JSON-compatible input shape for a tool.
type Schema struct {
	Type                 SchemaType        `json:"type,omitempty"`
	Description          string            `json:"description,omitempty"`
	Properties           map[string]Schema `json:"properties,omitempty"`
	Required             []string          `json:"required,omitempty"`
	Items                *Schema           `json:"items,omitempty"`
	Enum                 []any             `json:"enum,omitempty"`
	Default              any               `json:"default,omitempty"`
	AdditionalProperties any               `json:"additionalProperties,omitempty"`
}

// SchemaType names a JSON schema primitive or aggregate type.
type SchemaType string

const (
	// SchemaTypeObject represents a JSON object.
	SchemaTypeObject SchemaType = "object"
	// SchemaTypeArray represents a JSON array.
	SchemaTypeArray SchemaType = "array"
	// SchemaTypeString represents a JSON string.
	SchemaTypeString SchemaType = "string"
	// SchemaTypeNumber represents a JSON number.
	SchemaTypeNumber SchemaType = "number"
	// SchemaTypeInteger represents a JSON integer.
	SchemaTypeInteger SchemaType = "integer"
	// SchemaTypeBoolean represents a JSON boolean.
	SchemaTypeBoolean SchemaType = "boolean"
)
