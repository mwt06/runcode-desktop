package protocol

// SkillInfo is one project skill for the UI's skill manager.
type SkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Body        string `json:"body"`
	Source      string `json:"source"` // "project" or "user"
	Path        string `json:"path"`
	Editable    bool   `json:"editable"` // project skills under the workspace can be edited here
	// DisabledUser / DisabledProject report whether this skill is turned off at that
	// scope. Effective-enabled = neither is true. Takes effect on the next new session.
	DisabledUser    bool `json:"disabledUser"`
	DisabledProject bool `json:"disabledProject"`
}

// SkillLoad is what the desktop's Skill tool attaches to its progress event's
// `data` when the model loads a skill, so the chat can render a card naming what
// was loaded instead of a bare "加载技能" row.
//
// It carries what the tool result does not expose to the UI: the skill's
// description, which scope it came from, and the directory its bundled files
// live in. The frontend cannot derive these from the call's arguments — those
// hold only the name.
type SkillLoad struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Source      string `json:"source"` // "user" or "project"
	Dir         string `json:"dir"`
	// Truncated marks a skill whose body hit the engine's size cap, so the card can
	// warn that the model is acting on incomplete instructions.
	Truncated bool `json:"truncated,omitempty"`
}

// SkillProblem reports a skill directory that failed to load.
type SkillProblem struct {
	Dir    string `json:"dir"`
	Reason string `json:"reason"`
}

// SkillList is the skill manager's view: loaded skills plus load problems.
type SkillList struct {
	Skills   []SkillInfo    `json:"skills"`
	Problems []SkillProblem `json:"problems"`
}

// SkillSaveRequest creates or updates a skill. Scope is "project" (workspace) or
// "user" (global). OriginalName is set when renaming (its old directory is removed).
type SkillSaveRequest struct {
	OriginalName string `json:"originalName"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Body         string `json:"body"`
	Scope        string `json:"scope"`
}
