package desktop

import (
	"testing"
	"time"
)

func TestAccountIdentityUsesOneOpaqueFallback(t *testing.T) {
	jwt := imageGenJWT("user-a")
	stable := accountIdentityForToken(jwt, "login-epoch")
	if !stable.Stable || stable.Issuer == "" || stable.Subject != "user-a" || stable.Digest == "" {
		t.Fatalf("JWT identity = %+v", stable)
	}
	opaque := accountIdentityForToken("opaque-token", "login-epoch")
	if opaque.Stable || opaque.Digest == "" || opaque.Issuer != "" || opaque.Subject != "" {
		t.Fatalf("opaque identity = %+v", opaque)
	}
	empty := accountIdentityForToken("opaque-token", "")
	if empty.Stable || empty.Digest != "" {
		t.Fatalf("no-fallback identity = %+v", empty)
	}

	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: "opaque-token", Expiry: time.Now().Add(time.Hour)})
	app.tokens.mu.Lock()
	token, epoch := app.tokens.ts.AccessToken, app.tokens.identity
	app.tokens.mu.Unlock()
	if app.visionAccountIdentity() != accountIdentityForToken(token, epoch).Digest {
		t.Fatal("vision identity did not use the shared opaque fallback")
	}
}
