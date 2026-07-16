package permissions

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWebFetchRequiresApproval(t *testing.T) {
	t.Parallel()
	// DefaultService is safe (non-interactive), so the ask becomes a denial.
	action, decision := DefaultService().AuthorizeTool(context.Background(), ResolveRequest{
		ToolName: "WebFetch",
		Input:    json.RawMessage(`{"url":"https://example.com/docs/page?q=1"}`),
	})
	if action.Operation != OperationNetwork {
		t.Fatalf("operation = %q, want network", action.Operation)
	}
	if host := metadataString(action.Metadata, MetadataNetworkHost); host != "example.com" {
		t.Fatalf("host = %q, want example.com (no path/query)", host)
	}
	if decision.Effect != EffectAsk {
		t.Fatalf("policy effect = %q, want ask", decision.Effect)
	}
	if decision.FinalEffect != EffectDeny {
		t.Fatalf("safe mode should deny network, got %q", decision.FinalEffect)
	}
}

func TestWebFetchSessionKeyPerHost(t *testing.T) {
	t.Parallel()
	action := Action{
		ToolName:  "WebFetch",
		Operation: OperationNetwork,
		Metadata:  map[string]any{MetadataNetworkHost: "example.com"},
	}
	key := DefaultSessionKey(action)
	if key == "" || !strings.Contains(key, "example.com") {
		t.Fatalf("session key = %q, want a per-host key", key)
	}

	// A different host yields a different grant.
	other := Action{ToolName: "WebFetch", Operation: OperationNetwork, Metadata: map[string]any{MetadataNetworkHost: "other.com"}}
	if DefaultSessionKey(other) == key {
		t.Fatal("different hosts must not share a session grant")
	}

	// No host: not rememberable.
	if DefaultSessionKey(Action{ToolName: "WebFetch", Operation: OperationNetwork}) != "" {
		t.Fatal("a hostless network action must not be rememberable")
	}
}
