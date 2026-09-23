package desktop

import (
	"context"
	"testing"

	"gitlab.ouc-online.com.cn/aibase/agentloop/permissions"
)

func TestNativeSudoFormsAlwaysRequireApproval(t *testing.T) {
	for _, command := range []string{
		`sudo.exe whoami`,
		`"C:\Windows\System32\sudo.exe" --disable-input whoami`,
		`C:/Windows/System32/SUDO.EXE --new-window cmd /c ver`,
		`echo done&sudo.exe whoami`,
		`echo done;/usr/bin/sudo -A -k id`,
	} {
		action, err := (privilegeResolver{inner: fixedResolver{execAction(command)}}).Resolve(context.Background(), permissions.ResolveRequest{})
		if err != nil || action.Risk != permissions.RiskCritical {
			t.Fatalf("%q not normalized: %v %+v", command, err, action)
		}
		p := privilegePolicy{inner: fixedPolicy{permissions.Allow(permissions.Reason("test"), "test")}, enabled: func() bool { return true }}
		if got := p.Decide(context.Background(), action); got.Effect != permissions.EffectAsk {
			t.Errorf("%q bypassed approval: %+v", command, got)
		}
		inner := &recordingApprover{resp: permissions.ApprovalResponse{Effect: permissions.EffectAllow, Scope: permissions.ApprovalScopeSession}}
		resp, err := (privilegeApprover{inner: inner}).Prompt(context.Background(), permissions.ApprovalRequest{Command: command, Grantable: true})
		if err != nil || inner.got.Grantable || resp.Scope != permissions.ApprovalScopeOnce {
			t.Errorf("%q has reusable approval: %+v %v", command, resp, err)
		}
	}
}

func TestNativeDestructiveAndAlternateElevationRefused(t *testing.T) {
	p := privilegePolicy{inner: fixedPolicy{permissions.Allow(permissions.Reason("test"), "test")}, enabled: func() bool { return true }}
	for _, command := range []string{
		`sudo.exe cmd /c RD /s C:\Data`,
		`sudo.exe diskpart.exe`,
		`sudo.exe powershell -Command Remove-Item C:\Data`,
		`sudo.exe powershell -Command Clear-Disk 0`,
		`sudo.exe cmd /c del C:\Data\*`,
		`/usr/bin/sudo -A diskutil eraseDisk APFS Data /dev/disk2`,
		`sudo -A sh -c "echo x;rm -rf /tmp/test"`,
		`powershell Start-Process cmd -Verb RunAs`,
		`C:\Windows\System32\runas.exe /user:Administrator cmd`,
	} {
		if got := p.Decide(context.Background(), execAction(command)); got.Effect != permissions.EffectDeny {
			t.Errorf("%q permitted: %+v", command, got)
		}
	}
}

func TestUnavailableSudoCannotFallThroughToAllow(t *testing.T) {
	p := privilegePolicy{inner: fixedPolicy{permissions.Allow(permissions.Reason("test"), "test")}, enabled: func() bool { return false }}
	if got := p.Decide(context.Background(), execAction(`sudo.exe whoami`)); got.Effect != permissions.EffectDeny {
		t.Fatalf("unavailable sudo passed through: %+v", got)
	}
}
