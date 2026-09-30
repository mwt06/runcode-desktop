package protocol

// ModelReference identifies a saved connection, not just an ambiguous model ID.
// Platform references are pinned to their bridge and tenant; custom ones name
// a saved profile. Neither contains credentials.
type ModelReference struct {
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	Bridge   string `json:"bridge,omitempty"`
	TenantID string `json:"tenantId,omitempty"`
}

// PlatformImageCapability is a local override of optional catalog metadata.
type PlatformImageCapability struct {
	Model          ModelReference `json:"model"`
	SupportsImages bool           `json:"supportsImages"`
}

// VisionSettings configures the fixed destination for automatic image analysis.
type VisionSettings struct {
	Disabled             bool                      `json:"disabled,omitempty"`
	Bridge               string                    `json:"bridge,omitempty"`
	DefaultModel         *ModelReference           `json:"defaultModel,omitempty"`
	PlatformCapabilities []PlatformImageCapability `json:"platformCapabilities"`
}

// SetPlatformImageCapabilityRequest updates one override; nil removes it.
type SetPlatformImageCapabilityRequest struct {
	Model          ModelReference `json:"model"`
	SupportsImages *bool          `json:"supportsImages,omitempty"`
}

// SaveVisionSettingsRequest changes only the default destination, not a stale
// snapshot of the catalog or credentials. nil follows the platform unless Disabled.
type SaveVisionSettingsRequest struct {
	Disabled     bool            `json:"disabled,omitempty"`
	DefaultModel *ModelReference `json:"defaultModel,omitempty"`
}
