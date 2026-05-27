package permissions

import (
	"strings"
	"testing"
)

func TestClassifyCommandReadOnly(t *testing.T) {
	t.Parallel()

	for _, command := range []string{"pwd", "ls -la", "git status", "git diff --stat"} {
		command := command
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			classification := classifyCommand(command)
			if classification.Risk != RiskLow {
				t.Fatalf("risk = %q, want low", classification.Risk)
			}
			if !hasCommandCapability(classification.Capabilities, CommandCapabilityReadsWorkspace) {
				t.Fatalf("capabilities = %#v, want reads workspace", classification.Capabilities)
			}
		})
	}
}

func TestClassifyCommandTestAndBuild(t *testing.T) {
	t.Parallel()

	tests := map[string]CommandCategory{
		"go test ./...":          CommandCategoryTest,
		"go build ./cmd/runcode": CommandCategoryBuild,
		"npm test":               CommandCategoryTest,
		"pnpm run test":          CommandCategoryTest,
		"yarn run test":          CommandCategoryTest,
	}
	for command, wantCategory := range tests {
		command := command
		wantCategory := wantCategory
		t.Run(command, func(t *testing.T) {
			t.Parallel()
			classification := classifyCommand(command)
			if classification.Category != wantCategory || classification.Risk != RiskMedium {
				t.Fatalf("classification = %#v, want category %q medium risk", classification, wantCategory)
			}
		})
	}
}

func TestClassifyCommandHighRiskAskCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command    string
		category   CommandCategory
		capability CommandCapability
		reason     CommandRiskReason
	}{
		{command: "npm install left-pad", category: CommandCategoryPackageManager, capability: CommandCapabilityUsesNetwork, reason: CommandRiskPackageManager},
		{command: "go get example.com/mod", category: CommandCategoryPackageManager, capability: CommandCapabilityUsesNetwork, reason: CommandRiskPackageManager},
		{command: "curl https://example.invalid", category: CommandCategoryNetwork, capability: CommandCapabilityUsesNetwork, reason: CommandRiskNetworkAccess},
		{command: "mkdir output", category: CommandCategoryWorkspaceWrite, capability: CommandCapabilityWritesWorkspace, reason: CommandRiskWorkspaceWrite},
		{command: "git add .", category: CommandCategoryVCS, capability: CommandCapabilityModifiesVCS, reason: CommandRiskWorkspaceWrite},
		{command: "go test ./... > out.txt", category: CommandCategoryWorkspaceWrite, capability: CommandCapabilityWritesWorkspace, reason: CommandRiskRedirectsOutput},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()
			classification := classifyCommand(tt.command)
			if classification.Category != tt.category || classification.Risk != RiskHigh {
				t.Fatalf("classification = %#v, want category %q high risk", classification, tt.category)
			}
			if !hasCommandCapability(classification.Capabilities, tt.capability) {
				t.Fatalf("capabilities = %#v, want %q", classification.Capabilities, tt.capability)
			}
			if !hasCommandRiskReason(classification.Reasons, tt.reason) {
				t.Fatalf("reasons = %#v, want %q", classification.Reasons, tt.reason)
			}
		})
	}
}

func TestClassifyCommandHardDenyCandidates(t *testing.T) {
	t.Parallel()

	tests := []struct {
		command    string
		category   CommandCategory
		capability CommandCapability
		reason     CommandRiskReason
	}{
		{command: "unknown-tool --flag", category: CommandCategoryUnknown, capability: CommandCapabilityUnknownEffects, reason: CommandRiskUnknownCommand},
		{command: "sudo go test", category: CommandCategoryPrivileged, capability: CommandCapabilityRequiresPrivilege, reason: CommandRiskPrivilegedCommand},
		{command: "rm -rf build", category: CommandCategoryOutsideWrite, capability: CommandCapabilityWritesOutside, reason: CommandRiskOutsideWorkspaceWrite},
		{command: "git reset --hard", category: CommandCategoryVCSDestructive, capability: CommandCapabilityDestructiveVCS, reason: CommandRiskDestructiveVCS},
		{command: "ls | wc", category: CommandCategoryUnknown, capability: CommandCapabilityUnknownEffects, reason: CommandRiskShellControlOperator},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.command, func(t *testing.T) {
			t.Parallel()
			classification := classifyCommand(tt.command)
			if classification.Category != tt.category || classification.Risk != RiskCritical {
				t.Fatalf("classification = %#v, want category %q critical risk", classification, tt.category)
			}
			if !hasCommandCapability(classification.Capabilities, tt.capability) {
				t.Fatalf("capabilities = %#v, want %q", classification.Capabilities, tt.capability)
			}
			if !hasCommandRiskReason(classification.Reasons, tt.reason) {
				t.Fatalf("reasons = %#v, want %q", classification.Reasons, tt.reason)
			}
		})
	}
}

func TestClassifyCommandRedirect(t *testing.T) {
	t.Parallel()

	classification := classifyCommand("echo hello > out.txt")
	if classification.Category != CommandCategoryWorkspaceWrite || classification.Risk != RiskHigh || !hasCommandRiskReason(classification.Reasons, CommandRiskRedirectsOutput) {
		t.Fatalf("classification = %#v, want redirect workspace-write high risk", classification)
	}
}

func TestClassifyCommandDoesNotExposeRawCommand(t *testing.T) {
	t.Parallel()

	command := "curl https://secret.example.invalid/token"
	classification := classifyCommand(command)
	for _, value := range []string{string(classification.Category), classification.Summary} {
		if strings.Contains(value, "secret.example.invalid") || strings.Contains(value, command) {
			t.Fatalf("classification leaked raw command in %q", value)
		}
	}
	for _, value := range commandCapabilitiesStrings(classification.Capabilities) {
		if strings.Contains(value, "secret.example.invalid") || strings.Contains(value, command) {
			t.Fatalf("capability leaked raw command in %q", value)
		}
	}
	for _, value := range commandRiskReasonStrings(classification.Reasons) {
		if strings.Contains(value, "secret.example.invalid") || strings.Contains(value, command) {
			t.Fatalf("reason leaked raw command in %q", value)
		}
	}
}

func hasCommandCapability(values []CommandCapability, want CommandCapability) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasCommandRiskReason(values []CommandRiskReason, want CommandRiskReason) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
