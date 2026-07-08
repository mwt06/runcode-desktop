package desktop

import "testing"

// On a platform with protection (Windows/DPAPI), a secret round-trips through
// protect/unprotect and the protected form is not the plaintext.
func TestProtectSecretRoundTrip(t *testing.T) {
	t.Parallel()
	const secret = "sk-ant-verysecret-0xEF"
	protected, ok := protectSecret(secret)
	if !ok {
		t.Skip("no secret protection available on this platform")
	}
	if protected == secret {
		t.Fatal("protected value equals the plaintext — not encrypted")
	}
	plain, ok := unprotectSecret(protected)
	if !ok || plain != secret {
		t.Fatalf("round-trip = (%q, %v), want the original secret", plain, ok)
	}
}

func TestProtectSecretEmptyIsNotProtected(t *testing.T) {
	t.Parallel()
	if _, ok := protectSecret(""); ok {
		t.Fatal(`protectSecret("") should report ok=false`)
	}
}

// The persisted request must never carry a plaintext credential; non-secret fields
// are untouched, and on a protecting platform the credentials round-trip back.
func TestProtectRequestSecretsRedactsPlaintext(t *testing.T) {
	t.Parallel()
	req := StartSessionRequest{APIKey: "sk-secret", AuthToken: "tok-secret", Model: "m"}

	p := protectRequestSecrets(req)
	if p.APIKey != "" || p.AuthToken != "" {
		t.Fatalf("plaintext credentials survived: apiKey=%q authToken=%q", p.APIKey, p.AuthToken)
	}
	if p.Model != "m" {
		t.Fatal("a non-secret field was lost during protection")
	}

	if _, ok := protectSecret("probe"); ok { // platform protects
		r := unprotectRequestSecrets(p)
		if r.APIKey != "sk-secret" || r.AuthToken != "tok-secret" {
			t.Fatalf("round-trip = apiKey=%q authToken=%q, want the originals", r.APIKey, r.AuthToken)
		}
		if r.APIKeyProtected != "" || r.AuthTokenProtected != "" {
			t.Fatal("protected fields were not cleared after unprotect")
		}
	}
}
