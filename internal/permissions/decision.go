package permissions

type Effect string

const (
	EffectAllow Effect = "allow"
	EffectAsk   Effect = "ask"
	EffectDeny  Effect = "deny"
)

type Reason string

const (
	ReasonAllowedRead         Reason = "allowed_read"
	ReasonRequiresApproval    Reason = "requires_approval"
	ReasonApprovalUnavailable Reason = "approval_unavailable"
	ReasonApprovalGranted     Reason = "approval_granted"
	ReasonApprovalDenied      Reason = "approval_denied"
	ReasonSessionAllowed      Reason = "session_allowed"
	ReasonOutsideWorkspace    Reason = "outside_workspace"
	ReasonUnknownTool         Reason = "unknown_tool"
	ReasonInvalidInput        Reason = "invalid_input"
	ReasonInvalidTarget       Reason = "invalid_target"
	ReasonReadRequired        Reason = "read_required"
	ReasonReadStale           Reason = "read_stale"
	ReasonPolicyDenied        Reason = "policy_denied"
)

// Decision is the original policy decision plus the final effect after authorization.
type Decision struct {
	Effect      Effect
	FinalEffect Effect
	Reason      Reason
	Rule        string
}

func Allow(reason Reason, rule string) Decision {
	return Decision{Effect: EffectAllow, FinalEffect: EffectAllow, Reason: reason, Rule: rule}
}

func Ask(reason Reason, rule string) Decision {
	return Decision{Effect: EffectAsk, FinalEffect: EffectAsk, Reason: reason, Rule: rule}
}

func Deny(reason Reason, rule string) Decision {
	return Decision{Effect: EffectDeny, FinalEffect: EffectDeny, Reason: reason, Rule: rule}
}
