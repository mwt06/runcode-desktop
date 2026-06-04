package main

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/wt68/runcode/internal/permissions"
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
			if _, err := fmt.Fprint(p.err, "Please answer y, s, p, or n: "); err != nil {
				return permissions.ApprovalResponse{}, err
			}
		}
		line, err := p.readLine(ctx)
		if err != nil && err != io.EOF {
			return permissions.ApprovalResponse{}, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "y" || answer == "yes" || answer == "allow" {
			return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeOnce}, nil
		}
		if answer == "s" || answer == "session" {
			return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeSession}, nil
		}
		if answer == "p" || answer == "project" {
			return permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeProject}, nil
		}
		if answer == "" || answer == "n" || answer == "no" || answer == "deny" {
			return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
		}
		if err == io.EOF {
			return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
		}
	}
	return permissions.ApprovalResponse{Effect: permissions.EffectDeny, Reason: permissions.ReasonApprovalDenied}, nil
}

func (p *approvalPrompter) readLine(ctx context.Context) (string, error) {
	return p.lines.ReadLine(ctx)
}

func (p *approvalPrompter) writePrompt(req permissions.ApprovalRequest) error {
	summary := req.Summary
	if _, err := fmt.Fprintf(p.err, "Permission request\nTool: %s\nOperation: %s\nRisk: %s\nResources: %s/%s (%d)\nMutation: %s\nRead state: %s\nTarget exists: %v\n",
		summary.ToolName,
		summary.Operation,
		summary.Risk,
		strings.Join(summary.ResourceTypes, ","),
		summary.ResourceScope,
		summary.ResourceCount,
		fallbackString(summary.MutationKind, "n/a"),
		fallbackString(summary.ReadState, "n/a"),
		summary.TargetExists,
	); err != nil {
		return err
	}
	if summary.CommandSummary != "" {
		if _, err := fmt.Fprintf(p.err, "Command category: %s\nCommand capabilities: %s\nCommand risk reasons: %s\nCommand summary: %s\n",
			fallbackString(summary.CommandCategory, "n/a"),
			fallbackString(strings.Join(summary.CommandCapabilities, ","), "n/a"),
			fallbackString(strings.Join(summary.CommandRiskReasons, ","), "n/a"),
			summary.CommandSummary,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(p.err, "Policy: %s\nAllow? [y]es once / [s]ession / [p]roject / [N]o: ", summary.PolicyRule)
	return err
}

func fallbackString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
