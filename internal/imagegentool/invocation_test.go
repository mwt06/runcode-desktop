package imagegentool

import (
	"context"
	"testing"
)

func TestInvocationTokenLifecycleAndContext(t *testing.T) {
	token := NewInvocationToken("sess", "generation", "question", "turn")
	if token.Accepted() || token.Closed() {
		t.Fatal("new invocation must not be accepted or closed")
	}
	ctx := WithInvocation(context.Background(), token)
	got, ok := InvocationFromContext(ctx)
	if !ok || got != token {
		t.Fatal("invocation token was not preserved in private context")
	}
	token.MarkPersisted(nil)
	if !token.Accepted() {
		t.Fatal("successful persistence did not activate invocation")
	}
	token.Close()
	if !token.Closed() || !token.Accepted() {
		t.Fatal("closing invocation changed its accepted state unexpectedly")
	}
}

func TestInvocationTokenPersistenceFailureDoesNotActivate(t *testing.T) {
	token := NewInvocationToken("sess", "generation", "question", "turn")
	token.MarkPersisted(context.Canceled)
	if token.Accepted() {
		t.Fatal("failed persistence activated invocation")
	}
}
