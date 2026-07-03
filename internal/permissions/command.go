package permissions

import "strings"

const (
	MetadataCommandCategory     = "command_category"
	MetadataCommandCapabilities = "command_capabilities"
	MetadataCommandRiskReasons  = "command_risk_reasons"
	MetadataCommandSummary      = "command_summary"
	// MetadataCommandReadsOutside marks a shell command that reads a path outside
	// the workspace (e.g. `cat D:\other\file`), so the policy can deny it the same
	// way Read/Glob/Grep are bounded — shell must not be a way around the boundary.
	MetadataCommandReadsOutside = "command_reads_outside"
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
		// Shell-control operators (&&, ||, ;, |, $(), ``) compose commands, which is
		// normal in real workflows (go build && go test, ls | grep). They are
		// high-risk because the effects can't be statically reasoned about, so they
		// go to approval rather than a hard deny — the user sees the full command and
		// decides. The exception is a chain that hides privilege escalation or a
		// destructive delete: those stay critical (hard-denied) so they can never run,
		// approved or not.
		risk := RiskHigh
		summary := "shell command with control operators"
		if containsDangerousToken(command) {
			risk = RiskCritical
			summary = "shell command chaining a dangerous operation"
		}
		return CommandClassification{Category: CommandCategoryUnknown, Capabilities: []CommandCapability{CommandCapabilityUnknownEffects}, Reasons: []CommandRiskReason{CommandRiskShellControlOperator}, Risk: risk, Summary: summary}
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
	// An unrecognized command is high-risk (we cannot reason about its effects)
	// but not critical: the default policy routes it to approval rather than a
	// hard deny, so the user can run real tools (python, node, test/build runners)
	// by approving each. Genuinely critical cases — privilege escalation,
	// out-of-workspace deletes, destructive VCS, and complex shell-control
	// operators — keep their RiskCritical classification and stay hard-denied.
	return CommandClassification{Category: CommandCategoryUnknown, Capabilities: []CommandCapability{CommandCapabilityUnknownEffects}, Reasons: []CommandRiskReason{CommandRiskUnknownCommand}, Risk: RiskHigh, Summary: "unknown command"}
}

// containsDangerousToken reports whether any whitespace-separated token is a
// privilege-escalation or destructive-delete command. It is used to keep a
// composed shell command hard-denied even though plain control operators are only
// approval-gated, so e.g. `echo ok && rm -rf /` can never run. It is deliberately
// conservative (token-exact) rather than a full parse — false negatives degrade
// to "requires approval", where the user still sees the raw command.
func containsDangerousToken(command string) bool {
	for _, field := range strings.Fields(command) {
		switch strings.Trim(field, "\"'`()") {
		case "sudo", "su", "doas", "runas", "chmod", "chown",
			"rm", "rmdir", "del", "erase", "format", "mkfs", "dd":
			return true
		}
	}
	return false
}

func isPrivilegedCommand(command string) bool {
	switch command {
	case "sudo", "su", "doas", "runas", "chmod", "chown":
		return true
	default:
		return false
	}
}

// pathReadingCommands take file/dir paths as arguments and open them, so an
// out-of-workspace path argument is an out-of-workspace read. Commands that don't
// open path arguments (echo, pwd, git, go, …) are excluded to avoid false denials.
var pathReadingCommands = map[string]bool{
	"cat": true, "head": true, "tail": true, "type": true, "dir": true, "ls": true,
	"find": true, "grep": true, "egrep": true, "fgrep": true, "rg": true, "ag": true,
	"findstr": true, "stat": true, "file": true, "wc": true, "sort": true, "nl": true,
	"tac": true, "cut": true, "tree": true, "more": true, "less": true,
	"readlink": true, "realpath": true,
}

// tokenizeCommand splits a command into whitespace-separated tokens, honoring
// single/double quotes and stripping them (so `dir "D:\a b"` yields one token).
func tokenizeCommand(command string) []string {
	var tokens []string
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	for _, r := range command {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return tokens
}

// isPathArgument reports whether a token denotes a filesystem path worth bounds-
// checking: an absolute Windows/UNC/Unix path, a home path, or a relative path
// with a separator. Bare words (cwd-local names) and switches (-x, /s) are not.
func isPathArgument(token string) bool {
	if token == "" || strings.HasPrefix(token, "-") {
		return false
	}
	if len(token) >= 3 && isASCIILetter(token[0]) && token[1] == ':' && (token[2] == '\\' || token[2] == '/') {
		return true // C:\ or C:/
	}
	if strings.HasPrefix(token, `\\`) || strings.HasPrefix(token, "~") {
		return true // UNC share or home
	}
	if strings.HasPrefix(token, "/") {
		// A leading slash is a Unix absolute path only with a second separator;
		// `/s` `/b` `/i` are Windows command switches, not paths.
		return strings.Count(token, "/") >= 2
	}
	return strings.ContainsAny(token, `/\`)
}

func isASCIILetter(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

func hasShellControlOperator(command string) bool {
	return strings.Contains(command, "&&") || strings.Contains(command, "||") || strings.Contains(command, ";") || strings.Contains(command, "|") || strings.Contains(command, "`") || strings.Contains(command, "$(")
}

func hasOutputRedirect(command string) bool {
	return strings.Contains(command, ">") || strings.Contains(command, "2>")
}

// planReadOnlyCommands are commands that only read, for plan mode's exploration
// allowance. It is broader than the approval classifier's read-only set (it adds
// search/inspection tools) but deliberately excludes read-lookalikes that can
// mutate (sed -i, awk system(), find -delete/-exec — the last guarded by flags).
var planReadOnlyCommands = map[string]bool{
	"pwd": true, "ls": true, "dir": true, "cat": true, "head": true, "tail": true,
	"wc": true, "sort": true, "uniq": true, "printf": true, "echo": true, "nl": true,
	"tac": true, "cut": true, "column": true, "grep": true, "egrep": true, "fgrep": true,
	"rg": true, "ag": true, "find": true, "findstr": true, "type": true, "where": true,
	"which": true, "tree": true, "file": true, "stat": true, "basename": true,
	"dirname": true, "realpath": true, "readlink": true, "env": true, "date": true,
	"whoami": true, "hostname": true, "uname": true, "wslpath": true,
}

// planReadOnlyGitVerbs are git subcommands that only inspect.
var planReadOnlyGitVerbs = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "branch": true,
	"ls-files": true, "rev-parse": true, "describe": true, "blame": true, "tag": true,
}

// planReadOnlyPipVerbs are pip subcommands (and version flags) that only inspect —
// they neither install nor modify the environment. Keys are lowercase because the
// command line is matched case-insensitively.
var planReadOnlyPipVerbs = map[string]bool{
	"list": true, "show": true, "freeze": true, "check": true, "--version": true, "-v": true,
}

// planReadOnlyBlockedFlags turn an otherwise read-only command (notably find) into
// a mutating one, so a line carrying any of them is not treated as read-only.
var planReadOnlyBlockedFlags = map[string]bool{
	"-delete": true, "-exec": true, "-execdir": true, "-ok": true, "-okdir": true,
	"-fprint": true, "-fprintf": true, "-fls": true, "-i": true, "--in-place": true,
}

// isReadOnlyCommandLine reports whether a command line only reads: a chain/pipeline
// (&&, ||, ;, |) of known read-only commands, optionally discarding output to the
// null device. It is intentionally stricter than the approval classifier and is
// used solely to let plan mode explore without mutating. It still rejects command
// substitution, input/real-file redirects, and read-lookalikes that can mutate —
// and any dangerous token anywhere makes the whole line non-read-only.
func isReadOnlyCommandLine(command string) bool {
	c := strings.TrimSpace(command)
	if c == "" {
		return false
	}
	if strings.ContainsAny(c, "`<") || strings.Contains(c, "$(") {
		return false
	}
	if containsDangerousToken(c) {
		return false
	}
	// Drop benign null-device redirects; a remaining ">" means a real-file write.
	stripped := stripNullRedirects(c)
	if strings.Contains(stripped, ">") {
		return false
	}
	// Treat &&, ||, ;, and | all as segment separators: a chain of read-only
	// commands is itself read-only.
	for _, sep := range []string{"&&", "||", ";"} {
		stripped = strings.ReplaceAll(stripped, sep, "|")
	}
	for _, segment := range strings.Split(stripped, "|") {
		if strings.TrimSpace(segment) == "" {
			continue
		}
		fields := strings.Fields(segment)
		if len(fields) == 0 {
			continue
		}
		cmd, args := fields[0], fields[1:]
		switch cmd {
		case "git":
			if len(args) == 0 || !planReadOnlyGitVerbs[args[0]] {
				return false
			}
			continue
		case "pip", "pip3":
			// pip's first argument is the subcommand or a version flag.
			if len(args) == 0 || !planReadOnlyPipVerbs[args[0]] {
				return false
			}
			continue
		case "python", "python3", "py":
			// Only a version check is read-only; -c/-m and scripts execute code. The
			// line is lowercased, so -V arrives as -v.
			if !(len(args) == 1 && (args[0] == "--version" || args[0] == "-v")) {
				return false
			}
			continue
		}
		if !planReadOnlyCommands[cmd] {
			return false
		}
		for _, a := range args {
			if planReadOnlyBlockedFlags[a] {
				return false
			}
		}
	}
	return true
}

// stripNullRedirects removes redirects that discard output to the null device
// (2>nul, 2>/dev/null, >nul, >/dev/null, 2>&1, …). Matching is done on a lowercased
// copy so Windows' case-insensitive NUL is handled; the result is only inspected,
// never executed.
func stripNullRedirects(command string) string {
	s := strings.ToLower(command)
	for _, tok := range []string{"2>/dev/null", "1>/dev/null", ">/dev/null", "2>nul", "1>nul", ">nul", "2>&1", "1>&2", ">&2"} {
		s = strings.ReplaceAll(s, tok, " ")
	}
	return s
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
