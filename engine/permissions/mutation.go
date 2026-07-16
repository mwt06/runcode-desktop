package permissions

const (
	MetadataMutationKind    = "mutation_kind"
	MetadataReadRequirement = "read_requirement"
	MetadataReadState       = "read_state"
	MetadataTargetExists    = "target_exists"
)

const (
	MutationKindCreate    = "create"
	MutationKindOverwrite = "overwrite"
	MutationKindReplace   = "replace"
)

const (
	ReadRequirementNotRequired = "not_required"
	ReadRequirementRequired    = "required"
)

const (
	ReadStateNotRequired = "not_required"
	ReadStateFresh       = "fresh"
	ReadStateMissing     = "missing"
	ReadStatePartial     = "partial"
	ReadStateStale       = "stale"
)
