package codexauth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// idToken builds an unsigned JWT whose payload is claims — enough for the code
// under test, which reads the payload and never verifies the signature.
func idToken(t *testing.T, claims map[string]any) string {
	t.Helper()
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	return "hdr." + base64.RawURLEncoding.EncodeToString(payload) + ".sig"
}

// server stands up a fake OpenAI auth endpoint set and returns a Client wired to
// it plus the recorded request bodies keyed by path.
func server(t *testing.T, h http.HandlerFunc) (*Client, *[]string) {
	t.Helper()
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		h(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New(srv.Client())
	c.usercode = srv.URL + "/usercode"
	c.poll = srv.URL + "/poll"
	c.token = srv.URL + "/token"
	return c, &paths
}

// The user code comes back with the page to open and a poll interval that adds
// the safety margin — polling exactly at the server's interval risks being
// judged too fast.
func TestStartDeviceFlow(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_auth_id":"dev-1","user_code":"ABCD-EFGH","interval":5,"expires_in":900}`))
	})

	dc, err := c.StartDeviceFlow(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceFlow: %v", err)
	}
	if dc.DeviceAuthID != "dev-1" || dc.UserCode != "ABCD-EFGH" {
		t.Fatalf("device code = %+v, want the server's values", dc)
	}
	if dc.VerificationURL != VerificationURL {
		t.Fatalf("verification URL = %q, want the page users must open", dc.VerificationURL)
	}
	if dc.Interval != 5*time.Second+pollMargin {
		t.Fatalf("interval = %v, want the server's 5s plus the safety margin", dc.Interval)
	}
	if time.Until(dc.ExpiresAt) < 14*time.Minute {
		t.Fatalf("expiry = %v, want ~15 minutes out", dc.ExpiresAt)
	}
}

// A server that omits interval/expires_in must not yield a zero interval — that
// would spin the poll loop at full speed.
func TestStartDeviceFlowDefaults(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"device_auth_id":"d","user_code":"u"}`))
	})

	dc, err := c.StartDeviceFlow(context.Background())
	if err != nil {
		t.Fatalf("StartDeviceFlow: %v", err)
	}
	if dc.Interval < defaultInterval {
		t.Fatalf("interval = %v, want at least the default", dc.Interval)
	}
	if dc.ExpiresAt.IsZero() {
		t.Fatal("expiry not defaulted")
	}
}

// 403 and 404 both mean "the user hasn't finished yet" — a normal state of the
// flow, not a failure, or the login would abort the moment polling starts.
func TestPollPendingStatuses(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
		_, err := c.PollOnce(context.Background(), DeviceCode{DeviceAuthID: "d", UserCode: "u"})
		if !errors.Is(err, ErrAuthorizationPending) {
			t.Fatalf("status %d gave %v, want ErrAuthorizationPending", status, err)
		}
	}
}

// 410 means the code is dead; the caller must start over rather than keep polling.
func TestPollExpired(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusGone) })
	_, err := c.PollOnce(context.Background(), DeviceCode{DeviceAuthID: "d", UserCode: "u"})
	if !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("err = %v, want ErrCodeExpired", err)
	}
}

// The happy path: the poll hands back the authorization code together with the
// server-held PKCE verifier, and the client exchanges both for tokens in one go.
func TestPollSuccessExchangesTokens(t *testing.T) {
	var tokenForm string
	c, paths := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/poll":
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code","code_verifier":"verifier-1"}`))
		case "/token":
			_ = r.ParseForm()
			tokenForm = r.Form.Encode()
			claims := map[string]any{
				"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "acct-9"},
				"email":                       "u@example.com",
			}
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":3600,"id_token":"` + idToken(t, claims) + `"}`))
		}
	})

	tok, err := c.PollOnce(context.Background(), DeviceCode{DeviceAuthID: "d", UserCode: "u"})
	if err != nil {
		t.Fatalf("PollOnce: %v", err)
	}
	if tok.AccessToken != "AT" || tok.RefreshToken != "RT" {
		t.Fatalf("tokens = %+v, want the server's pair", tok)
	}
	if tok.AccountID != "acct-9" {
		t.Fatalf("account id = %q, want it read from the namespaced id_token claim", tok.AccountID)
	}
	if tok.Email != "u@example.com" {
		t.Fatalf("email = %q, want it read from the id_token", tok.Email)
	}
	if time.Until(tok.Expiry) < 59*time.Minute {
		t.Fatalf("expiry = %v, want ~1h out", tok.Expiry)
	}
	// The verifier is the server's, echoed back verbatim; generating our own
	// would fail the exchange.
	for _, want := range []string{"grant_type=authorization_code", "code=auth-code", "code_verifier=verifier-1", "client_id=" + ClientID} {
		if !strings.Contains(tokenForm, strings.ReplaceAll(want, "+", "%2B")) {
			t.Fatalf("token form %q missing %q", tokenForm, want)
		}
	}
	if len(*paths) != 2 || (*paths)[0] != "/poll" || (*paths)[1] != "/token" {
		t.Fatalf("request path order = %v, want poll then token", *paths)
	}
}

// The account id may sit at the top level instead of the namespaced claim; both
// shapes appear in the wild and either one must work.
func TestAccountIDFromTopLevelClaim(t *testing.T) {
	id, email := accountFromIDToken(idToken(t, map[string]any{"chatgpt_account_id": "top", "email": "a@b.c"}))
	if id != "top" || email != "a@b.c" {
		t.Fatalf("accountFromIDToken = (%q, %q), want the top-level claim", id, email)
	}
	// A malformed token must degrade to empty, never panic.
	if id, _ := accountFromIDToken("not-a-jwt"); id != "" {
		t.Fatalf("malformed id_token yielded %q, want empty", id)
	}
}

// The refresh endpoint often omits refresh_token; dropping it would leave the
// account unable to refresh ever again.
func TestRefreshKeepsExistingRefreshToken(t *testing.T) {
	var form string
	c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.Form.Encode()
		_, _ = w.Write([]byte(`{"access_token":"AT2","expires_in":3600}`))
	})

	tok, err := c.Refresh(context.Background(), "OLD-RT")
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "AT2" {
		t.Fatalf("access token = %q, want the refreshed one", tok.AccessToken)
	}
	if tok.RefreshToken != "OLD-RT" {
		t.Fatalf("refresh token = %q, want the existing one carried forward", tok.RefreshToken)
	}
	if !strings.Contains(form, "grant_type=refresh_token") || !strings.Contains(form, "scope=openid") {
		t.Fatalf("refresh form = %q, want the grant and scope the server requires", form)
	}
}

// WaitForToken keeps polling through pending answers and returns as soon as the
// user finishes.
func TestWaitForTokenPollsUntilAuthorized(t *testing.T) {
	polls := 0
	c, _ := server(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/poll":
			polls++
			if polls < 3 {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = w.Write([]byte(`{"authorization_code":"c","code_verifier":"v"}`))
		case "/token":
			_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","id_token":"` + idToken(t, map[string]any{"chatgpt_account_id": "a"}) + `"}`))
		}
	})

	dc := DeviceCode{DeviceAuthID: "d", UserCode: "u", Interval: time.Millisecond, ExpiresAt: time.Now().Add(time.Minute)}
	tok, err := c.WaitForToken(context.Background(), dc)
	if err != nil {
		t.Fatalf("WaitForToken: %v", err)
	}
	if tok.AccessToken != "AT" || polls != 3 {
		t.Fatalf("token=%q after %d polls, want success on the third", tok.AccessToken, polls)
	}
}

// A code that runs out while the user dithers must end the wait, not spin forever.
func TestWaitForTokenStopsAtExpiry(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	dc := DeviceCode{DeviceAuthID: "d", UserCode: "u", Interval: time.Millisecond, ExpiresAt: time.Now().Add(-time.Second)}
	if _, err := c.WaitForToken(context.Background(), dc); !errors.Is(err, ErrCodeExpired) {
		t.Fatalf("err = %v, want ErrCodeExpired once the code's deadline passed", err)
	}
}

// A cancelled login (the user closed the dialog) stops the loop.
func TestWaitForTokenHonorsContext(t *testing.T) {
	c, _ := server(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dc := DeviceCode{DeviceAuthID: "d", UserCode: "u", Interval: time.Millisecond, ExpiresAt: time.Now().Add(time.Minute)}
	if _, err := c.WaitForToken(ctx, dc); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
