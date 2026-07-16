package repl

import (
	"encoding/json"
	"testing"
)

type analyzeInputShape struct {
	Method string `json:"method"`
	Steps  []struct {
		Key     string `json:"key"`
		Label   string `json:"label"`
		Content string `json:"content"`
	} `json:"steps"`
}

func TestAnalysisInputCarriesMethodAndLabels(t *testing.T) {
	p, ok := protocolFor(ReasoningScenarioTroubleshooting)
	if !ok {
		t.Fatal("missing troubleshooting protocol")
	}
	filled := map[string]string{"symptom": "偶发 401", "root_cause": "刷新竞态"}
	var got analyzeInputShape
	if err := json.Unmarshal(p.analysisInput(filled), &got); err != nil {
		t.Fatalf("unmarshal analysisInput: %v", err)
	}
	if got.Method != p.Method {
		t.Fatalf("method = %q, want %q", got.Method, p.Method)
	}
	if len(got.Steps) != len(p.Steps) {
		t.Fatalf("steps = %d, want %d", len(got.Steps), len(p.Steps))
	}
	if got.Steps[0].Label != p.Steps[0].Label || got.Steps[0].Content != "偶发 401" {
		t.Fatalf("first step = %+v, want label %q content %q", got.Steps[0], p.Steps[0].Label, "偶发 401")
	}
}

func TestEnrichAnalysisInputAddsLabelsAndCanonicalOrder(t *testing.T) {
	p, _ := protocolFor(ReasoningScenarioTroubleshooting)
	// Model emits steps out of order and without labels.
	modelInput := []byte(`{"steps":[{"key":"root_cause","content":"竞态"},{"key":"symptom","content":"401"}]}`)
	var got analyzeInputShape
	if err := json.Unmarshal(p.enrichAnalysisInput(modelInput), &got); err != nil {
		t.Fatalf("unmarshal enriched: %v", err)
	}
	if got.Method != p.Method {
		t.Fatalf("method = %q, want %q", got.Method, p.Method)
	}
	// Re-ordered to the protocol's canonical order (symptom is step 0), labeled.
	if got.Steps[0].Key != p.Steps[0].Key || got.Steps[0].Label != p.Steps[0].Label {
		t.Fatalf("first step = %+v, want key %q label %q", got.Steps[0], p.Steps[0].Key, p.Steps[0].Label)
	}
	if got.Steps[0].Content != "401" {
		t.Fatalf("symptom content = %q, want 401", got.Steps[0].Content)
	}
	// Unparseable input is returned unchanged.
	bad := []byte("not json")
	if out := p.enrichAnalysisInput(bad); string(out) != "not json" {
		t.Fatalf("bad input = %q, want passthrough", out)
	}
}
