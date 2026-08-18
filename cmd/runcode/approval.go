package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

const maxApprovalAttempts = 3

type approvalPrompter struct {
	lines lineReader
	err   io.Writer
}

func newApprovalPrompter(lines lineReader, err io.Writer) *approvalPrompter {
	return &approvalPrompter{lines: lines, err: err}
}

func (p *approvalPrompter) Prompt(ctx context.Context, req permissions.ApprovalRequest) (permissions.ApprovalResponse, error) {
	if err := p.writePrompt(req); err != nil {
		return permissions.ApprovalResponse{}, err
	}
	for attempt := 0; attempt < maxApprovalAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return permissions.ApprovalResponse{}, err
		}
		if attempt > 0 {
			if _, err := fmt.Fprintf(p.err, "Please answer %s: ", answerList(req.Grantable)); err != nil {
				return permissions.ApprovalResponse{}, err
			}
		}
		line, err := p.readLine(ctx)
		if err != nil && !errors.Is(err, io.EOF) {
			return permissions.ApprovalResponse{}, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "y" || answer == "yes" || answer == "allow" {
			return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeOnce}, nil
		}
		// The remembering answers are only accepted when the engine says this action
		// has a key to remember them by. Otherwise they are not offered and not
		// understood — the alternative, quietly downgrading "session" to "once",
		// answers a question the user did not ask.
		if req.Grantable {
			if answer == "s" || answer == "session" {
				return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeSession}, nil
			}
			if answer == "p" || answer == "project" {
				return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeProject}, nil
			}
		}
		if answer == "" || answer == "n" || answer == "no" || answer == "deny" {
			return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
		}
		if errors.Is(err, io.EOF) {
			return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
		}
	}
	return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
}

func (p *approvalPrompter) readLine(ctx context.Context) (string, error) {
	return p.lines.ReadLine(ctx)
}

// answerList names the answers a request accepts, for the prompt and the retry
// line. Both read it, so what the user is told to type is always what the parser
// above will take.
func answerList(grantable bool) string {
	if grantable {
		return "y, s, p, or n"
	}
	return "y or n"
}

// allowLine renders the trailing question. An action with no grant key drops the
// session/project offers and says why, so the missing letters read as a property
// of this action rather than a missing feature.
func allowLine(grantable bool) string {
	if grantable {
		return "Allow? [y]es once / [s]ession / [p]roject / [N]o: "
	}
	return "Allow? [y]es once / [N]o (this call cannot be remembered): "
}

func (p *approvalPrompter) writePrompt(req permissions.ApprovalRequest) error {
	if req.SamplingServer != "" {
		note := "a session grant stops re-asking."
		if !req.Grantable {
			note = "this call cannot be remembered, so it is asked each time."
		}
		_, err := fmt.Fprintf(p.err, "Permission request\nMCP server %q requests to use your model (sampling).\nAllowing spends your model on the server's behalf; %s\n%s",
			req.SamplingServer, note, allowLine(req.Grantable))
		return err
	}
	summary := req.Summary
	if req.Command != "" {
		if _, err := fmt.Fprintf(p.err, "Command to run: %s\n", req.Command); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(p.err, "Permission request\nTool: %s\nOperation: %s\nRisk: %s\nResources: %s/%s (%d)\nMutation: %s\nRead state: %s\nTarget exists: %v\n",
		summary.ToolName,
		summary.Operation,
		summary.Risk,
		strings.Join(summary.ResourceTypes, ","),
		summary.ResourceScope,
		summary.ResourceCount,
		orNA(summary.MutationKind),
		orNA(summary.ReadState),
		summary.TargetExists,
	); err != nil {
		return err
	}
	if summary.CommandSummary != "" {
		if _, err := fmt.Fprintf(p.err, "Command category: %s\nCommand capabilities: %s\nCommand risk reasons: %s\nCommand summary: %s\n",
			orNA(summary.CommandCategory),
			orNA(strings.Join(summary.CommandCapabilities, ",")),
			orNA(strings.Join(summary.CommandRiskReasons, ",")),
			summary.CommandSummary,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(p.err, "Policy: %s\n%s", summary.PolicyRule, allowLine(req.Grantable))
	return err
}

// orNA 把空字段显示成 n/a —— 审批摘要里所有可缺字段共用这一个占位符。
func orNA(value string) string {
	if value == "" {
		return "n/a"
	}
	return value
}
