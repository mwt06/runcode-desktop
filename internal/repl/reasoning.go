package repl

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const defaultReasoningMaxTokens = 128

// reasoningAnalysisMaxTokens budgets the pre-turn structured analysis pass; it
// needs room for several filled steps plus the model's own reasoning tokens
// (reasoning models spend a large prefix thinking before emitting the JSON).
const reasoningAnalysisMaxTokens = 4096

var ErrInvalidReasoningClassification = errors.New("invalid reasoning classification")

type ReasoningScenario string

const (
	ReasoningScenarioTroubleshooting ReasoningScenario = "troubleshooting"
	ReasoningScenarioProposal        ReasoningScenario = "proposal"
	ReasoningScenarioArchitecture    ReasoningScenario = "architecture"
	ReasoningScenarioProject         ReasoningScenario = "project_management"
	ReasoningScenarioIncident        ReasoningScenario = "incident_response"
	ReasoningScenarioGeneral         ReasoningScenario = "general"
)

type ReasoningOptions struct {
	Enabled bool
	// AutoClassify makes each turn classify the task into a scenario via a small
	// model call. When false, DefaultScenario's guidance is used directly (no extra
	// call) — the "manual" mode.
	AutoClassify    bool
	DefaultScenario ReasoningScenario
	Strict          bool
	MaxTokens       int
}

type ReasoningClassification struct {
	Scenario   ReasoningScenario
	Confidence string
}

type reasoningClassificationPayload struct {
	Scenario   string `json:"scenario"`
	Confidence string `json:"confidence"`
}

func defaultReasoningScenario(options ReasoningOptions) ReasoningScenario {
	if scenario, ok := normalizeReasoningScenario(string(options.DefaultScenario)); ok {
		return scenario
	}
	return ReasoningScenarioGeneral
}

func reasoningMaxTokens(options ReasoningOptions) int {
	if options.MaxTokens > 0 {
		return options.MaxTokens
	}
	return defaultReasoningMaxTokens
}

func parseReasoningClassification(text string) (ReasoningClassification, error) {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return ReasoningClassification{}, fmt.Errorf("%w: empty classification", ErrInvalidReasoningClassification)
	}

	var payload reasoningClassificationPayload
	if err := json.Unmarshal([]byte(raw), &payload); err != nil {
		return ReasoningClassification{}, fmt.Errorf("%w: parse json: %v", ErrInvalidReasoningClassification, err)
	}
	scenario, ok := normalizeReasoningScenario(payload.Scenario)
	if !ok {
		return ReasoningClassification{}, fmt.Errorf("%w: unknown scenario %q", ErrInvalidReasoningClassification, payload.Scenario)
	}
	return ReasoningClassification{Scenario: scenario, Confidence: strings.TrimSpace(payload.Confidence)}, nil
}

func normalizeReasoningScenario(value string) (ReasoningScenario, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch ReasoningScenario(normalized) {
	case ReasoningScenarioTroubleshooting,
		ReasoningScenarioProposal,
		ReasoningScenarioArchitecture,
		ReasoningScenarioProject,
		ReasoningScenarioIncident,
		ReasoningScenarioGeneral:
		return ReasoningScenario(normalized), true
	default:
		return "", false
	}
}
