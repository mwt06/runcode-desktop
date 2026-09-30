package protocol

// QuestionReference identifies one original user input, never a UI block index.
type QuestionReference struct {
	SessionID  string `json:"sessionId"`
	QuestionID string `json:"questionId"`
}

// QuestionImage exposes metadata only; original bytes remain in session storage.
type QuestionImage struct {
	Index     int    `json:"index"`
	Name      string `json:"name"`
	MediaType string `json:"mediaType"`
}

// QuestionDraft is the actual submitted text, not the bubble's shortened label.
type QuestionDraft struct {
	Source QuestionReference `json:"source"`
	Text   string            `json:"text"`
	Images []QuestionImage   `json:"images"`
}

// SubmitQuestionRequest supports ordinary input and a prepared retry branch.
// OriginalImages indexes only the Source question authorized by branch lineage.
type SubmitQuestionRequest struct {
	SessionID      string             `json:"sessionId"`
	Text           string             `json:"text"`
	ImagePaths     []string           `json:"imagePaths"`
	Source         *QuestionReference `json:"source,omitempty"`
	OriginalImages []int              `json:"originalImages"`
	AllowSteering  bool               `json:"allowSteering"`
}
