package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/wt68/runcode/internal/permissions"
)

const maxApprovalAttempts = 3

type approvalPrompter struct {
	in     io.Reader
	err    io.Writer
	once   sync.Once
	lines  chan approvalLine
	closed chan struct{}
}

type approvalLine struct {
	text string
	err  error
}

func newApprovalPrompter(in io.Reader, err io.Writer) *approvalPrompter {
	return &approvalPrompter{
		in:     in,
		err:    err,
		lines:  make(chan approvalLine, 1),
		closed: make(chan struct{}),
	}
}

func readApprovalLines(reader *bufio.Reader, lines chan<- approvalLine, closed chan<- struct{}) {
	defer close(lines)
	defer close(closed)
	for {
		line, err := reader.ReadString('\n')
		lines <- approvalLine{text: line, err: err}
		if err != nil {
			return
		}
	}
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
			if _, err := fmt.Fprint(p.err, "Please answer y or n: "); err != nil {
				return permissions.ApprovalResponse{}, err
			}
		}
		line, err := p.readLine(ctx)
		if err != nil && err != io.EOF {
			return permissions.ApprovalResponse{}, err
		}
		answer := strings.ToLower(strings.TrimSpace(line))
		if answer == "y" || answer == "yes" || answer == "allow" {
			return permissions.ApprovalResponse{Effect: permissions.EffectAllow}, nil
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
	p.once.Do(func() {
		go readApprovalLines(bufio.NewReader(p.in), p.lines, p.closed)
	})
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case line, ok := <-p.lines:
		if !ok {
			return "", io.EOF
		}
		return line.text, line.err
	}
}

func (p *approvalPrompter) writePrompt(req permissions.ApprovalRequest) error {
	summary := req.Summary
	_, err := fmt.Fprintf(p.err, "Permission request\nTool: %s\nOperation: %s\nRisk: %s\nResources: %s/%s (%d)\nMutation: %s\nRead state: %s\nTarget exists: %v\nPolicy: %s\nAllow once? [y/N]: ",
		summary.ToolName,
		summary.Operation,
		summary.Risk,
		strings.Join(summary.ResourceTypes, ","),
		summary.ResourceScope,
		summary.ResourceCount,
		fallbackString(summary.MutationKind, "n/a"),
		fallbackString(summary.ReadState, "n/a"),
		summary.TargetExists,
		summary.PolicyRule,
	)
	return err
}

func fallbackString(value string, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}
