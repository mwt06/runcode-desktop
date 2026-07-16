package hooks

import (
	"context"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fakeExec(exit int, output string, err error) execFunc {
	return func(context.Context, []string, []byte, time.Duration) (int, string, error) {
		return exit, output, err
	}
}

func TestRunnerBlocksOnNonZeroExit(t *testing.T) {
	t.Parallel()
	r := &commandRunner{
		hooks: []Hook{{Event: EventPreToolUse, Matcher: "Bash", Command: []string{"x"}}},
		exec:  fakeExec(2, "denied: rm -rf is forbidden", nil),
	}
	d := r.Run(context.Background(), Input{Event: EventPreToolUse, ToolName: "Bash"})
	if !d.Block || !strings.Contains(d.Output, "rm -rf is forbidden") {
		t.Fatalf("decision = %#v, want blocked with reason", d)
	}
}

func TestRunnerAllowsAndAggregatesOutput(t *testing.T) {
	t.Parallel()
	r := &commandRunner{
		hooks: []Hook{
			{Event: EventPostToolUse, Matcher: "*", Command: []string{"a"}},
			{Event: EventPostToolUse, Matcher: "Write", Command: []string{"b"}},
		},
		exec: fakeExec(0, "note", nil),
	}
	d := r.Run(context.Background(), Input{Event: EventPostToolUse, ToolName: "Write"})
	if d.Block {
		t.Fatalf("exit 0 must not block")
	}
	if d.Output != "note\n\nnote" {
		t.Fatalf("aggregated output = %q, want both notes joined", d.Output)
	}
}

func TestRunnerFiltersByEventAndMatcher(t *testing.T) {
	t.Parallel()
	calls := 0
	r := &commandRunner{
		hooks: []Hook{
			{Event: EventPreToolUse, Matcher: "Bash", Command: []string{"x"}},  // wrong tool
			{Event: EventPostToolUse, Matcher: "Read", Command: []string{"y"}}, // wrong event
			{Event: EventPreToolUse, Matcher: "Read", Command: []string{"z"}},  // matches
		},
		exec: func(context.Context, []string, []byte, time.Duration) (int, string, error) {
			calls++
			return 0, "", nil
		},
	}
	r.Run(context.Background(), Input{Event: EventPreToolUse, ToolName: "Read"})
	if calls != 1 {
		t.Fatalf("exec called %d times, want 1 (only the matching hook)", calls)
	}
}

func TestRunnerFailOpenOnInfraError(t *testing.T) {
	t.Parallel()
	warned := false
	r := &commandRunner{
		hooks: []Hook{{Event: EventPreToolUse, Matcher: "*", Command: []string{"missing"}}},
		exec:  fakeExec(0, "", errors.New("cannot start")),
		warn:  func(Event, error) { warned = true },
	}
	d := r.Run(context.Background(), Input{Event: EventPreToolUse, ToolName: "Bash"})
	if d.Block {
		t.Fatal("an infrastructure failure must fail open (not block)")
	}
	if !warned {
		t.Fatal("an infrastructure failure must warn")
	}
}

func TestRunnerPayloadCarriesEventData(t *testing.T) {
	t.Parallel()
	var gotStdin []byte
	r := &commandRunner{
		hooks: []Hook{{Event: EventPreToolUse, Matcher: "*", Command: []string{"x"}}},
		exec: func(_ context.Context, _ []string, stdin []byte, _ time.Duration) (int, string, error) {
			gotStdin = stdin
			return 0, "", nil
		},
	}
	r.Run(context.Background(), Input{Event: EventPreToolUse, ToolName: "Bash", Prompt: "", CWD: "/work"})
	for _, want := range []string{`"event":"PreToolUse"`, `"tool_name":"Bash"`, `"cwd":"/work"`} {
		if !strings.Contains(string(gotStdin), want) {
			t.Fatalf("payload missing %q:\n%s", want, gotStdin)
		}
	}
}

func TestMatcherMatches(t *testing.T) {
	t.Parallel()
	if !matcherMatches("", "Bash") || !matcherMatches("*", "Bash") || !matcherMatches("Bash", "Bash") {
		t.Fatal("empty/star/exact should match")
	}
	if matcherMatches("Bash", "Read") {
		t.Fatal("a different tool must not match")
	}
}

func TestSanitizeOutput(t *testing.T) {
	t.Parallel()
	if got := sanitizeOutput("  hi\x00\x07 there \n"); got != "hi there" {
		t.Fatalf("sanitize = %q, want control chars stripped", got)
	}
	if sanitizeOutput("   ") != "" {
		t.Fatal("whitespace-only should be empty")
	}
	long := strings.Repeat("a", maxHookFeedbackRunes+100)
	if got := sanitizeOutput(long); len([]rune(got)) > maxHookFeedbackRunes {
		t.Fatalf("output not bounded: %d runes", len([]rune(got)))
	}
}

func TestNoopRunner(t *testing.T) {
	t.Parallel()
	if d := (Noop{}).Run(context.Background(), Input{Event: EventPreToolUse}); d.Block || d.Output != "" {
		t.Fatalf("noop decision = %#v, want empty", d)
	}
	if _, ok := NewRunner(nil, Options{}).(Noop); !ok {
		t.Fatal("NewRunner with no hooks should return Noop")
	}
}

// TestHookHelperProcess is not a real test: when HOOK_HELPER=1 it acts as a hook
// command, echoing its stdin and exiting with HOOK_EXIT, so the real exec path is
// exercised cross-platform by re-executing the test binary.
func TestHookHelperProcess(t *testing.T) {
	if os.Getenv("HOOK_HELPER") != "1" {
		return
	}
	data, _ := io.ReadAll(os.Stdin)
	_, _ = os.Stdout.WriteString("got:" + string(data))
	code, _ := strconv.Atoi(os.Getenv("HOOK_EXIT"))
	os.Exit(code)
}

func TestRunCommandRealSubprocess(t *testing.T) {
	t.Setenv("HOOK_HELPER", "1")
	t.Setenv("HOOK_EXIT", "0")
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("executable: %v", err)
	}
	r := &commandRunner{
		hooks: []Hook{{Event: EventPreToolUse, Matcher: "*", Command: []string{exe, "-test.run=TestHookHelperProcess"}}},
		exec:  runCommand,
	}
	d := r.Run(context.Background(), Input{Event: EventPreToolUse, ToolName: "Bash"})
	if d.Block {
		t.Fatalf("exit 0 must not block, got %#v", d)
	}
	if !strings.Contains(d.Output, "got:") || !strings.Contains(d.Output, `"tool_name":"Bash"`) {
		t.Fatalf("hook did not receive/echo the payload: %q", d.Output)
	}
}
