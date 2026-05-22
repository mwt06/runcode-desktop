package repl

import (
	"errors"
	"testing"
)

func TestParseReasoningClassificationJSON(t *testing.T) {
	t.Parallel()

	classification, err := parseReasoningClassification(`{"scenario":"architecture","confidence":"medium"}`)
	if err != nil {
		t.Fatalf("parse classification: %v", err)
	}
	if classification.Scenario != ReasoningScenarioArchitecture {
		t.Fatalf("scenario = %q, want %q", classification.Scenario, ReasoningScenarioArchitecture)
	}
	if classification.Confidence != "medium" {
		t.Fatalf("confidence = %q, want medium", classification.Confidence)
	}
}

func TestParseReasoningClassificationNormalizesScenario(t *testing.T) {
	t.Parallel()

	classification, err := parseReasoningClassification(`{"scenario":" Incident Response ","confidence":"high"}`)
	if err != nil {
		t.Fatalf("parse classification: %v", err)
	}
	if classification.Scenario != ReasoningScenarioIncident {
		t.Fatalf("scenario = %q, want %q", classification.Scenario, ReasoningScenarioIncident)
	}
}

func TestParseReasoningClassificationRejectsUnknownScenario(t *testing.T) {
	t.Parallel()

	_, err := parseReasoningClassification(`{"scenario":"unknown"}`)
	if !errors.Is(err, ErrInvalidReasoningClassification) {
		t.Fatalf("expected invalid classification error, got %v", err)
	}
}

func TestParseReasoningClassificationRejectsInvalidJSON(t *testing.T) {
	t.Parallel()

	_, err := parseReasoningClassification(`architecture`)
	if !errors.Is(err, ErrInvalidReasoningClassification) {
		t.Fatalf("expected invalid classification error, got %v", err)
	}
}

func TestDefaultReasoningScenario(t *testing.T) {
	t.Parallel()

	if got := defaultReasoningScenario(ReasoningOptions{}); got != ReasoningScenarioGeneral {
		t.Fatalf("default scenario = %q, want %q", got, ReasoningScenarioGeneral)
	}
	if got := defaultReasoningScenario(ReasoningOptions{DefaultScenario: ReasoningScenarioProposal}); got != ReasoningScenarioProposal {
		t.Fatalf("default scenario = %q, want %q", got, ReasoningScenarioProposal)
	}
}
