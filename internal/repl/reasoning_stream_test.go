package repl

import "testing"

func TestPartialJSONStringField(t *testing.T) {
	cases := []struct {
		name, buf, key, want string
		done                 bool
	}{
		{"complete", `{"a":"hello"}`, "a", "hello", true},
		{"partial value", `{"a":"hel`, "a", "hel", false},
		{"value not started", `{"a":`, "a", "", false},
		{"missing key", `{"b":"x"}`, "a", "", false},
		{"escaped quote", `{"a":"he\"llo"}`, "a", `he"llo`, true},
		{"escaped newline partial", `{"a":"line1\nline2`, "a", "line1\nline2", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, done := partialJSONStringField(c.buf, c.key)
			if got != c.want || done != c.done {
				t.Fatalf("got (%q,%v), want (%q,%v)", got, done, c.want, c.done)
			}
		})
	}
}

func TestPartialInTurnContentToleratesTruncation(t *testing.T) {
	// Two complete steps and a third mid-content — the trailing element must still
	// surface its partial content.
	buf := `{"steps":[{"key":"symptom","content":"401"},{"key":"hypotheses","content":"竞态"},{"key":"root_cause","content":"旧 token 覆`
	got := partialInTurnContent(buf)
	if got["symptom"] != "401" || got["hypotheses"] != "竞态" {
		t.Fatalf("complete steps = %+v", got)
	}
	if got["root_cause"] != "旧 token 覆" {
		t.Fatalf("trailing partial = %q, want %q", got["root_cause"], "旧 token 覆")
	}
}

func TestPartialPreTurnContentFillsProgressively(t *testing.T) {
	p, _ := protocolFor(ReasoningScenarioTroubleshooting)
	// A flat object mid-stream: symptom done, root_cause still streaming.
	buf := `{"symptom":"偶发 401","hypotheses":"刷新竞态","root_cause":"旧 token 覆盖`
	got := p.partialPreTurnContent(buf)
	if got["symptom"] != "偶发 401" || got["hypotheses"] != "刷新竞态" {
		t.Fatalf("done fields = %+v", got)
	}
	if got["root_cause"] != "旧 token 覆盖" {
		t.Fatalf("streaming field = %q", got["root_cause"])
	}
	// The rendered input carries method + every protocol step (empty ones included).
	input, sig := p.analysisInputFrom(got)
	if input == nil || sig == "" {
		t.Fatal("expected non-empty analysis input and signature")
	}
}
