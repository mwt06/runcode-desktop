package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

// defaultNetworkTool is the tool that network allow/deny rules apply to. WebFetch
// is currently the only network-class tool, so it is the default for the
// host-oriented allow/deny subcommands.
const defaultNetworkTool = "WebFetch"

// permissionsCmd manages the persistent permission rules stored at
// <workspace>/.runcode/permissions.json — the same allow/deny grain the
// interactive "allow for project" prompt writes. Listing and removal cover every
// rule kind (network, mutation, command); adding rules from the CLI targets
// network hosts, the one key kind that is reliably typeable and matches exactly.
func permissionsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "permissions",
		Short:        "Inspect and edit persisted permission rules (.runcode/permissions.json)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPermissionsList(cmd)
		},
	}
	cmd.PersistentFlags().String("cwd", "", "workspace directory (default: current directory; env RUNCODE_CWD)")

	cmd.AddCommand(permissionsListCmd())
	cmd.AddCommand(permissionsDenyCmd())
	cmd.AddCommand(permissionsAllowCmd())
	cmd.AddCommand(permissionsRemoveCmd())
	cmd.AddCommand(permissionsClearCmd())
	return cmd
}

func permissionsListCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "list",
		Short:        "List the persisted allow and deny rules",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runPermissionsList(cmd)
		},
	}
}

func permissionsDenyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "deny <host>",
		Short:        "Add a denylist rule for a network host (deny always wins over allow)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openPermissionsStore(cmd)
			if err != nil {
				return err
			}
			tool, _ := cmd.Flags().GetString("tool")
			key, err := networkRuleKey(tool, args[0])
			if err != nil {
				return err
			}
			changed, err := store.DenyPersistent(key)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			if changed {
				fmt.Fprintf(out, "denied %s for %s\n", args[0], tool)
			} else {
				fmt.Fprintf(out, "already denied: %s for %s\n", args[0], tool)
			}
			return nil
		},
	}
	cmd.Flags().String("tool", defaultNetworkTool, "tool the rule applies to")
	return cmd
}

func permissionsAllowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "allow <host>",
		Short:        "Add a project allow rule for a network host (pre-approve so it never prompts)",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openPermissionsStore(cmd)
			if err != nil {
				return err
			}
			tool, _ := cmd.Flags().GetString("tool")
			key, err := networkRuleKey(tool, args[0])
			if err != nil {
				return err
			}
			if store.Denied(key) {
				return fmt.Errorf("%s is on the denylist; remove the deny rule first (deny wins over allow)", args[0])
			}
			if err := store.RememberPersistent(key); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "allowed %s for %s\n", args[0], tool)
			return nil
		},
	}
	cmd.Flags().String("tool", defaultNetworkTool, "tool the rule applies to")
	return cmd
}

func permissionsRemoveCmd() *cobra.Command {
	return &cobra.Command{
		Use:          "remove <number>",
		Short:        "Remove a rule by its number from `permissions list`",
		Args:         cobra.ExactArgs(1),
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			store, err := openPermissionsStore(cmd)
			if err != nil {
				return err
			}
			n, err := strconv.Atoi(strings.TrimSpace(args[0]))
			if err != nil || n < 1 {
				return fmt.Errorf("invalid rule number %q (run `runcode permissions list`)", args[0])
			}
			rules := orderedRules(store)
			if n > len(rules) {
				return fmt.Errorf("no rule numbered %d (there are %d)", n, len(rules))
			}
			entry := rules[n-1]
			removed, err := store.Forget(entry.rule.Key)
			if err != nil {
				return err
			}
			if !removed {
				return fmt.Errorf("rule %d was already gone", n)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed [%d] %s\n", n, formatRule(entry))
			return nil
		},
	}
}

func permissionsClearCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "clear",
		Short:        "Remove all persisted rules (or only allow/deny with the flags)",
		Args:         cobra.NoArgs,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := openPermissionsStore(cmd)
			if err != nil {
				return err
			}
			allowOnly, _ := cmd.Flags().GetBool("allow")
			denyOnly, _ := cmd.Flags().GetBool("deny")
			// With neither flag, clear both; with one, clear only that list.
			clearAllow := allowOnly || !denyOnly
			clearDeny := denyOnly || !allowOnly
			removed, err := store.ClearPersistent(clearAllow, clearDeny)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "cleared %d rule(s)\n", removed)
			return nil
		},
	}
	cmd.Flags().Bool("allow", false, "clear only the allow list")
	cmd.Flags().Bool("deny", false, "clear only the deny list")
	return cmd
}

// openPermissionsStore resolves the workspace and opens its permissions file. A
// corrupt file is surfaced as an error (never silently dropped) so deny rules are
// not lost without notice.
func openPermissionsStore(cmd *cobra.Command) (*permissions.FileAllowStore, error) {
	cwd, err := cwdConfig(cmd)
	if err != nil {
		return nil, err
	}
	return permissions.OpenFileAllowStore(cwd)
}

func networkRuleKey(tool, host string) (string, error) {
	tool = strings.TrimSpace(tool)
	host = strings.TrimSpace(strings.ToLower(host))
	if tool == "" {
		return "", fmt.Errorf("empty tool")
	}
	if host == "" {
		return "", fmt.Errorf("empty host")
	}
	key := permissions.NetworkSessionKey(tool, host)
	if key == "" {
		return "", fmt.Errorf("invalid host %q", host)
	}
	return key, nil
}

// ruleEntry pairs a parsed rule with its list (allow/deny) for display.
type ruleEntry struct {
	list string // "allow" or "deny"
	rule permissions.Rule
}

// orderedRules returns deny rules first, then allow rules, each already sorted by
// the store. The order is stable, so `permissions list` numbering and
// `permissions remove <n>` agree.
func orderedRules(store *permissions.FileAllowStore) []ruleEntry {
	var entries []ruleEntry
	for _, key := range store.Denies() {
		entries = append(entries, ruleEntry{list: "deny", rule: permissions.ParseRule(key)})
	}
	for _, key := range store.Allows() {
		entries = append(entries, ruleEntry{list: "allow", rule: permissions.ParseRule(key)})
	}
	return entries
}

func runPermissionsList(cmd *cobra.Command) error {
	store, err := openPermissionsStore(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	entries := orderedRules(store)
	if len(entries) == 0 {
		fmt.Fprintln(out, "No persisted permission rules.")
		fmt.Fprintln(out, "Add one with `runcode permissions deny <host>` or grant \"allow for project\" in the TUI.")
		return nil
	}
	for i, entry := range entries {
		fmt.Fprintf(out, "[%d] %s\n", i+1, formatRule(entry))
	}
	return nil
}

// formatRule renders a rule as "<list> <scope> <tool> <target>", e.g.
// "deny  network WebFetch example.com". A key that did not parse falls back to
// showing the raw key so nothing is hidden.
func formatRule(entry ruleEntry) string {
	r := entry.rule
	scope := r.Scope
	if scope == "" {
		scope = "?"
	}
	parts := []string{fmt.Sprintf("%-5s", entry.list), scope}
	if r.Tool != "" {
		parts = append(parts, r.Tool)
	}
	if r.Target != "" {
		parts = append(parts, r.Target)
	}
	return strings.Join(parts, " ")
}
