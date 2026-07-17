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
