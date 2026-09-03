package protocol

// SkillInfo is one project skill for the UI's skill manager.
type SkillInfo struct {
	Name string `json:"name"`
	// DisplayName 是 SKILL.md frontmatter 里的 display-name（市场装来的技能带中文名），
	// 界面优先显示它、把 Name 降为副标题。为空表示这个技能没写展示名，界面回退到 Name。
	//
	// 它不是引擎的字段：引擎只读 name/description，其余 frontmatter 键一律忽略。展示名
	// 纯粹是给人看的，模型不该因为多一个中文别名而多一种称呼它的方式。
	DisplayName string `json:"displayName"`
	// DisplayDescription 是给**人**看的那句描述（市场目录里的 description）。空则
	// 界面回退到 Description。
	//
	// 与 Description 并存而不是覆盖它：那一句是**给模型**看的，决定何时加载这个技能
	// （市场包里常写成 "Use when normalizing academic references, converting…"）。
	// 那是一条判断规则，摆进中文列表读着很别扭；但拿目录里的介绍盖掉它，列表好看了，
	// 技能的触发时机会悄悄变差。两句各留各的，各给各的读者。
	DisplayDescription string `json:"displayDescription"`
	Description        string `json:"description"`
	Body               string `json:"body"`
	Source             string `json:"source"` // "project" or "user"
	Path               string `json:"path"`
	Editable           bool   `json:"editable"` // project skills under the workspace can be edited here
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
	// DisplayName 可空。保存时会原样写回 frontmatter——不带这一栏就等于把它删掉，
	// 所以编辑页必须把读到的值带回来（否则编辑一次描述就把市场给的中文名弄丢了）。
	DisplayName string `json:"displayName"`
	// DisplayDescription 同理可空、同理必须由编辑页带回来。
	DisplayDescription string `json:"displayDescription"`
	Description        string `json:"description"`
	Body               string `json:"body"`
	Scope              string `json:"scope"`
}
