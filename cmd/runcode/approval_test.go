package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

func TestApprovalPrompterAllowsYes(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	response, err := newApprovalPrompter(newLineInput(strings.NewReader("y\n")), &errOut).Prompt(context.Background(), approvalRequest())
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.Effect != permissions.EffectAllow {
		t.Fatalf("response = %#v, want allow", response)
	}
	if !strings.Contains(errOut.String(), "Permission request") || !strings.Contains(errOut.String(), "Tool: Write") {
		t.Fatalf("prompt output missing summary: %q", errOut.String())
	}
}

func TestApprovalPrompterDeniesNoAndDefault(t *testing.T) {
	t.Parallel()

	for _, input := range []string{"n\n", "\n"} {
		response, err := newApprovalPrompter(newLineInput(strings.NewReader(input)), &bytes.Buffer{}).Prompt(context.Background(), approvalRequest())
		if err != nil {
			t.Fatalf("prompt: %v", err)
		}
		if response.Effect != permissions.EffectDeny || response.Reason != permissions.ReasonApprovalDenied {
			t.Fatalf("response = %#v, want deny", response)
		}
	}
}

func TestApprovalPrompterKeepsBufferedInputAcrossPrompts(t *testing.T) {
	t.Parallel()

	prompter := newApprovalPrompter(newLineInput(strings.NewReader("y\nn\n")), &bytes.Buffer{})
	first, err := prompter.Prompt(context.Background(), approvalRequest())
	if err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	second, err := prompter.Prompt(context.Background(), approvalRequest())
	if err != nil {
		t.Fatalf("second prompt: %v", err)
	}
	if first.Effect != permissions.EffectAllow || second.Effect != permissions.EffectDeny {
		t.Fatalf("responses = %#v, %#v; want allow then deny", first, second)
	}
}

func TestApprovalPrompterDeniesEOF(t *testing.T) {
	t.Parallel()

	response, err := newApprovalPrompter(newLineInput(strings.NewReader("")), &bytes.Buffer{}).Prompt(context.Background(), approvalRequest())
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.Effect != permissions.EffectDeny {
		t.Fatalf("response = %#v, want deny", response)
	}
}

func TestApprovalPrompterReturnsOnContextCancelWhileReading(t *testing.T) {
	t.Parallel()

	reader := newBlockingReader()
	defer reader.Close()
	ctx, cancel := context.WithCancel(context.Background())
	prompter := newApprovalPrompter(newLineInput(reader), &bytes.Buffer{})
	cancel()
	_, err := prompter.Prompt(ctx, approvalRequest())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context canceled", err)
	}
}

func TestApprovalPrompterDeniesAfterInvalidAttempts(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	response, err := newApprovalPrompter(newLineInput(strings.NewReader("maybe\nwhat\n?\n")), &errOut).Prompt(context.Background(), approvalRequest())
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.Effect != permissions.EffectDeny {
		t.Fatalf("response = %#v, want deny", response)
	}
	if strings.Count(errOut.String(), "Please answer y, s, p, or n") != 2 {
		t.Fatalf("unexpected retry prompt output: %q", errOut.String())
	}
}

func TestApprovalPrompterDoesNotPrintRawPath(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	_, err := newApprovalPrompter(newLineInput(strings.NewReader("n\n")), &errOut).Prompt(context.Background(), approvalRequest())
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	for _, forbidden := range []string{"secret.txt", "D:/secret"} {
		if strings.Contains(errOut.String(), forbidden) {
			t.Fatalf("prompt leaked %q: %q", forbidden, errOut.String())
		}
	}
}

func TestApprovalPrompterPrintsCommandClassification(t *testing.T) {
	t.Parallel()

	var errOut bytes.Buffer
	_, err := newApprovalPrompter(newLineInput(strings.NewReader("n\n")), &errOut).Prompt(context.Background(), commandApprovalRequest())
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	output := errOut.String()
	for _, want := range []string{"Command category: network", "Command capabilities: uses_network", "Command risk reasons: network_access", "Command summary: network command"} {
		if !strings.Contains(output, want) {
			t.Fatalf("prompt output missing %q: %q", want, output)
		}
	}
	for _, forbidden := range []string{"curl", "secret.example.invalid", "token"} {
		if strings.Contains(output, forbidden) {
			t.Fatalf("prompt leaked %q: %q", forbidden, output)
		}
	}
}

type blockingReader struct {
	done chan struct{}
	once sync.Once
}

func newBlockingReader() *blockingReader {
	return &blockingReader{done: make(chan struct{})}
}

func (r *blockingReader) Read([]byte) (int, error) {
	<-r.done
	return 0, io.EOF
}

func (r *blockingReader) Close() {
	r.once.Do(func() { close(r.done) })
}

var _ io.Reader = (*blockingReader)(nil)

func approvalRequest() permissions.ApprovalRequest {
	return permissions.ApprovalRequest{Summary: permissions.NewApprovalSummary(permissions.Action{
		ToolName:  "Write",
		Operation: permissions.OperationWrite,
		Risk:      permissions.RiskHigh,
		Resources: []permissions.Resource{{Type: permissions.ResourceFile, Scope: permissions.ResourceScopeWorkspace, Path: "D:/secret/secret.txt"}},
		Metadata: map[string]any{
			permissions.MetadataMutationKind: "overwrite",
			permissions.MetadataReadState:    "fresh",
			permissions.MetadataTargetExists: true,
		},
	}, permissions.Ask(permissions.ReasonRequiresApproval, "default.mutate.workspace")),
		// The ordinary case: a workspace mutation has a grant key, so all four
		// answers are on offer. The unrememberable case has its own test.
		Grantable: true,
	}
}

func commandApprovalRequest() permissions.ApprovalRequest {
	return permissions.ApprovalRequest{Summary: permissions.NewApprovalSummary(permissions.Action{
		ToolName:  "Bash",
		Operation: permissions.OperationExecute,
		Risk:      permissions.RiskHigh,
		Resources: []permissions.Resource{{Type: permissions.ResourceCommand, Scope: permissions.ResourceScopeWorkspace}},
		Metadata: map[string]any{
			permissions.MetadataCommandCategory:     "network",
			permissions.MetadataCommandCapabilities: []string{"uses_network"},
			permissions.MetadataCommandRiskReasons:  []string{"network_access"},
			permissions.MetadataCommandSummary:      "network command",
		},
	}, permissions.Ask(permissions.ReasonRequiresApproval, "default.execute.requires_approval")),
		Grantable: true,
	}
}

// A request the engine cannot remember must not advertise the remembering
// answers — and must not quietly accept them either. "s" on such a request used
// to come back as an allow-session the engine then dropped, which is the user
// asking not to be asked again and being asked again with nothing to explain it.
func TestApprovalPrompterHidesGrantScopesWhenNotGrantable(t *testing.T) {
	t.Parallel()

	req := approvalRequest()
	req.Grantable = false

	var errOut bytes.Buffer
	response, err := newApprovalPrompter(newLineInput(strings.NewReader("y\n")), &errOut).Prompt(context.Background(), req)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.Effect != permissions.EffectAllow || response.Scope != permissions.ApprovalScopeOnce {
		t.Fatalf("response = %#v, want allow once", response)
	}
	prompt := errOut.String()
	if strings.Contains(prompt, "[s]ession") || strings.Contains(prompt, "[p]roject") {
		t.Fatalf("prompt offers scopes the engine cannot honor: %q", prompt)
	}
	if !strings.Contains(prompt, "cannot be remembered") {
		t.Fatalf("prompt = %q, want it to say why the scopes are gone", prompt)
	}
}

// "s" is not an answer on an unrememberable request: it must re-ask rather than
// resolve, and the retry line must name only the answers that exist.
func TestApprovalPrompterRejectsScopeAnswerWhenNotGrantable(t *testing.T) {
	t.Parallel()

	req := approvalRequest()
	req.Grantable = false

	var errOut bytes.Buffer
	response, err := newApprovalPrompter(newLineInput(strings.NewReader("s\ny\n")), &errOut).Prompt(context.Background(), req)
	if err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if response.Effect != permissions.EffectAllow || response.Scope != permissions.ApprovalScopeOnce {
		t.Fatalf("response = %#v, want the retry to land on allow once", response)
	}
	if !strings.Contains(errOut.String(), "Please answer y or n") {
		t.Fatalf("retry line = %q, want it to offer y or n only", errOut.String())
	}
}
