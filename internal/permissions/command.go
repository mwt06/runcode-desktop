package permissions

import "strings"

const (
	MetadataCommandCategory     = "command_category"
	MetadataCommandCapabilities = "command_capabilities"
	MetadataCommandRiskReasons  = "command_risk_reasons"
	MetadataCommandSummary      = "command_summary"
)

type CommandCategory string

const (
	CommandCategoryUnknown        CommandCategory = "unknown"
	CommandCategoryReadOnly       CommandCategory = "read_only"
	CommandCategoryTest           CommandCategory = "test"
	CommandCategoryBuild          CommandCategory = "build"
	CommandCategoryPackageManager CommandCategory = "package_manager"
	CommandCategoryNetwork        CommandCategory = "network"
	CommandCategoryVCS            CommandCategory = "vcs"
	CommandCategoryVCSDestructive CommandCategory = "vcs_destructive"
	CommandCategoryWorkspaceWrite CommandCategory = "workspace_write"
	CommandCategoryOutsideWrite   CommandCategory = "outside_write"
	CommandCategoryPrivileged     CommandCategory = "privileged"
)

type CommandCapability string

const (
	CommandCapabilityReadsWorkspace    CommandCapability = "reads_workspace"
	CommandCapabilityWritesWorkspace   CommandCapability = "writes_workspace"
	CommandCapabilityWritesOutside     CommandCapability = "writes_outside"
	CommandCapabilityUsesNetwork       CommandCapability = "uses_network"
	CommandCapabilityModifiesVCS       CommandCapability = "modifies_vcs"
	CommandCapabilityDestructiveVCS    CommandCapability = "destructive_vcs"
	CommandCapabilityRequiresPrivilege CommandCapability = "requires_privilege"
	CommandCapabilityUnknownEffects    CommandCapability = "unknown_effects"
)

type CommandRiskReason string

const (
	CommandRiskUnknownCommand        CommandRiskReason = "unknown_command"
	CommandRiskShellControlOperator  CommandRiskReason = "shell_control_operator"
	CommandRiskRedirectsOutput       CommandRiskReason = "redirects_output"
	CommandRiskPackageManager        CommandRiskReason = "package_manager"
	CommandRiskNetworkAccess         CommandRiskReason = "network_access"
	CommandRiskDestructiveVCS        CommandRiskReason = "destructive_vcs"
	CommandRiskOutsideWorkspaceWrite CommandRiskReason = "outside_workspace_write"
	CommandRiskPrivilegedCommand     CommandRiskReason = "privileged_command"
	CommandRiskWorkspaceWrite        CommandRiskReason = "workspace_write"
)

type CommandClassification struct {
	Category     CommandCategory
	Capabilities []CommandCapability
	Reasons      []CommandRiskReason
	Risk         Risk
	Summary      string
}

func classifyCommand(command string) CommandClassification {
	fields := strings.Fields(strings.TrimSpace(command))
	if len(fields) == 0 {
		return unknownCommand()
	}
	if hasShellControlOperator(command) {
		return CommandClassification{Category: CommandCategoryUnknown, Capabilities: []CommandCapability{CommandCapabilityUnknownEffects}, Reasons: []CommandRiskReason{CommandRiskShellControlOperator}, Risk: RiskCritical, Summary: "complex shell command"}
	}
	if hasOutputRedirect(command) {
		return CommandClassification{Category: CommandCategoryWorkspaceWrite, Capabilities: []CommandCapability{CommandCapabilityWritesWorkspace}, Reasons: []CommandRiskReason{CommandRiskRedirectsOutput, CommandRiskWorkspaceWrite}, Risk: RiskHigh, Summary: "command with output redirection"}
	}
	cmd := fields[0]
	args := fields[1:]
	if isPrivilegedCommand(cmd) {
		return CommandClassification{Category: CommandCategoryPrivileged, Capabilities: []CommandCapability{CommandCapabilityRequiresPrivilege}, Reasons: []CommandRiskReason{CommandRiskPrivilegedCommand}, Risk: RiskCritical, Summary: "privileged command"}
	}
	switch cmd {
	case "pwd", "ls", "dir", "cat", "head", "tail", "wc", "sort", "uniq", "printf", "echo":
		return CommandClassification{Category: CommandCategoryReadOnly, Capabilities: []CommandCapability{CommandCapabilityReadsWorkspace}, Risk: RiskLow, Summary: "read-only workspace command"}
	case "go":
		return classifyGo(args)
	case "npm", "pnpm", "yarn":
		return classifyPackageCommand(cmd, args)
	case "git":
		return classifyGit(args)
	case "curl", "wget":
		return CommandClassification{Category: CommandCategoryNetwork, Capabilities: []CommandCapability{CommandCapabilityUsesNetwork}, Reasons: []CommandRiskReason{CommandRiskNetworkAccess}, Risk: RiskHigh, Summary: "network command"}
	case "rm", "rmdir", "del", "erase":
		return CommandClassification{Category: CommandCategoryOutsideWrite, Capabilities: []CommandCapability{CommandCapabilityWritesOutside}, Reasons: []CommandRiskReason{CommandRiskOutsideWorkspaceWrite}, Risk: RiskCritical, Summary: "destructive file command"}
	case "mkdir", "touch", "cp", "mv":
		return CommandClassification{Category: CommandCategoryWorkspaceWrite, Capabilities: []CommandCapability{CommandCapabilityWritesWorkspace}, Reasons: []CommandRiskReason{CommandRiskWorkspaceWrite}, Risk: RiskHigh, Summary: "workspace write command"}
	default:
		return unknownCommand()
	}
}

func classifyGo(args []string) CommandClassification {
	if len(args) == 0 {
		return unknownCommand()
	}
	switch args[0] {
	case "test":
		return CommandClassification{Category: CommandCategoryTest, Capabilities: []CommandCapability{CommandCapabilityReadsWorkspace}, Risk: RiskMedium, Summary: "go test command"}
	case "build", "vet", "list", "version", "env":
		return CommandClassification{Category: CommandCategoryBuild, Capabilities: []CommandCapability{CommandCapabilityReadsWorkspace}, Risk: RiskMedium, Summary: "go build or inspection command"}
	case "mod", "get", "install", "run":
		return CommandClassification{Category: CommandCategoryPackageManager, Capabilities: []CommandCapability{CommandCapabilityWritesWorkspace, CommandCapabilityUsesNetwork}, Reasons: []CommandRiskReason{CommandRiskPackageManager, CommandRiskNetworkAccess}, Risk: RiskHigh, Summary: "go dependency or execution command"}
	default:
		return unknownCommand()
	}
}

func classifyPackageCommand(cmd string, args []string) CommandClassification {
	if len(args) == 0 {
		return unknownCommand()
	}
	verb := args[0]
	if verb == "test" || verb == "run" && len(args) > 1 && args[1] == "test" {
		return CommandClassification{Category: CommandCategoryTest, Capabilities: []CommandCapability{CommandCapabilityReadsWorkspace}, Risk: RiskMedium, Summary: cmd + " test command"}
	}
	if verb == "install" || verb == "add" || verb == "update" || verb == "upgrade" {
		return CommandClassification{Category: CommandCategoryPackageManager, Capabilities: []CommandCapability{CommandCapabilityWritesWorkspace, CommandCapabilityUsesNetwork}, Reasons: []CommandRiskReason{CommandRiskPackageManager, CommandRiskNetworkAccess}, Risk: RiskHigh, Summary: "package manager command"}
	}
	return unknownCommand()
}

func classifyGit(args []string) CommandClassification {
	if len(args) == 0 {
		return unknownCommand()
	}
	switch args[0] {
	case "status", "diff", "log", "show", "branch":
		return CommandClassification{Category: CommandCategoryVCS, Capabilities: []CommandCapability{CommandCapabilityReadsWorkspace}, Risk: RiskLow, Summary: "read-only git command"}
	case "reset", "clean", "checkout", "restore", "rebase":
		return CommandClassification{Category: CommandCategoryVCSDestructive, Capabilities: []CommandCapability{CommandCapabilityDestructiveVCS}, Reasons: []CommandRiskReason{CommandRiskDestructiveVCS}, Risk: RiskCritical, Summary: "destructive git command"}
	case "add", "commit", "merge", "pull", "fetch", "push":
		return CommandClassification{Category: CommandCategoryVCS, Capabilities: []CommandCapability{CommandCapabilityModifiesVCS}, Reasons: []CommandRiskReason{CommandRiskWorkspaceWrite}, Risk: RiskHigh, Summary: "git state-changing command"}
	default:
		return unknownCommand()
	}
}

func unknownCommand() CommandClassification {
	return CommandClassification{Category: CommandCategoryUnknown, Capabilities: []CommandCapability{CommandCapabilityUnknownEffects}, Reasons: []CommandRiskReason{CommandRiskUnknownCommand}, Risk: RiskCritical, Summary: "unknown command"}
}

func isPrivilegedCommand(command string) bool {
	switch command {
	case "sudo", "su", "doas", "runas", "chmod", "chown":
		return true
	default:
		return false
	}
}

func hasShellControlOperator(command string) bool {
	return strings.Contains(command, "&&") || strings.Contains(command, "||") || strings.Contains(command, ";") || strings.Contains(command, "|") || strings.Contains(command, "`") || strings.Contains(command, "$(")
}

func hasOutputRedirect(command string) bool {
	return strings.Contains(command, ">") || strings.Contains(command, "2>")
}

func commandCapabilitiesStrings(values []CommandCapability) []string {
	items := make([]string, len(values))
	for i, value := range values {
		items[i] = string(value)
	}
	return items
}

func commandRiskReasonStrings(values []CommandRiskReason) []string {
	items := make([]string, len(values))
	for i, value := range values {
		items[i] = string(value)
	}
	return items
}
