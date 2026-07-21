package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestGenPKCE(t *testing.T) {
	verifier, challenge, err := genPKCE()
	if err != nil {
		t.Fatal(err)
	}
	if len(verifier) < 43 || len(verifier) > 128 {
		t.Fatalf("verifier length %d out of RFC 7636 range", len(verifier))
	}
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Fatalf("challenge = %q, want S256(verifier) = %q", challenge, want)
	}
	v2, _, _ := genPKCE()
	if v2 == verifier {
		t.Fatal("two PKCE verifiers must differ")
	}
}

func TestGenStateCarriesPort(t *testing.T) {
	state, err := genState(53699)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(state, ".53699") {
		t.Fatalf("state %q must end with .<port>", state)
	}
	if nonce := stateNonce(state); nonce == "" || strings.Contains(nonce, ".") {
		t.Fatalf("nonce %q malformed", nonce)
	}
}

func TestBuildAuthorizeURL(t *testing.T) {
	u := buildAuthorizeURL("https://passport.example", "runcode-desktop",
		"http://localhost:8199/oauth/callback", "openid profile offline_access passportapi",
		"n.51000", "CHALLENGE", "OneOuchnPassport")
	parsed, err := url.Parse(u)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Path != "/connect/authorize" {
		t.Fatalf("path = %q", parsed.Path)
	}
	q := parsed.Query()
	for k, want := range map[string]string{
		"client_id":             "runcode-desktop",
		"redirect_uri":          "http://localhost:8199/oauth/callback",
		"response_type":         "code",
		"scope":                 "openid profile offline_access passportapi",
		"state":                 "n.51000",
		"code_challenge":        "CHALLENGE",
		"code_challenge_method": "S256",
		"scheme":                "OneOuchnPassport",
	} {
		if q.Get(k) != want {
			t.Fatalf("%s = %q, want %q", k, q.Get(k), want)
		}
	}
}

func TestBuildAuthorizeURLNoSchemeOmitsParam(t *testing.T) {
	u := buildAuthorizeURL("https://passport.example", "runcode-desktop",
		"http://localhost:8199/oauth/callback", "openid", "n.5", "C", "")
	parsed, _ := url.Parse(u)
	if _, ok := parsed.Query()["scheme"]; ok {
		t.Fatal("empty scheme must not add a scheme param (base passport)")
	}
}

func TestExchangeCode(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT","refresh_token":"RT","expires_in":3600,"token_type":"Bearer"}`))
	}))
	defer srv.Close()

	ts, err := exchangeCode(context.Background(), srv.Client(), srv.URL, "runcode-desktop",
		"CODE", "VERIFIER", "http://localhost:8199/oauth/callback")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "AT" || ts.RefreshToken != "RT" {
		t.Fatalf("tokenSet = %+v", ts)
	}
	if until := time.Until(ts.Expiry); until < 55*time.Minute || until > 61*time.Minute {
		t.Fatalf("expiry %v not ≈ now+1h", ts.Expiry)
	}
	for k, want := range map[string]string{
		"grant_type":    "authorization_code",
		"code":          "CODE",
		"code_verifier": "VERIFIER",
		"client_id":     "runcode-desktop",
		"redirect_uri":  "http://localhost:8199/oauth/callback",
	} {
		if form.Get(k) != want {
			t.Fatalf("form %s = %q, want %q", k, form.Get(k), want)
		}
	}
}

func TestExchangeCodeErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()
	if _, err := exchangeCode(context.Background(), srv.Client(), srv.URL, "c", "x", "v", "r"); err == nil ||
		!strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err = %v, want invalid_grant surfaced", err)
	}
}

func TestRefreshGrant(t *testing.T) {
	var form url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		form = r.PostForm
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT2","refresh_token":"RT2","expires_in":3600}`))
	}))
	defer srv.Close()

	ts, err := refreshGrant(context.Background(), srv.Client(), srv.URL, "runcode-desktop", "RT1")
	if err != nil {
		t.Fatal(err)
	}
	if ts.AccessToken != "AT2" || ts.RefreshToken != "RT2" {
		t.Fatalf("tokenSet = %+v", ts)
	}
	if form.Get("grant_type") != "refresh_token" || form.Get("refresh_token") != "RT1" {
		t.Fatalf("form = %v", form)
	}
}

func TestCallbackServerDeliversCode(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=THE-CODE&state=%s", cs.Port, url.QueryEscape(state)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	select {
	case r := <-cs.Result:
		if r.Err != nil || r.Code != "THE-CODE" {
			t.Fatalf("result = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result delivered")
	}
}

func TestCallbackServerRejectsWrongState(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=X&state=forged.%d", cs.Port, cs.Port))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", resp.StatusCode)
	}
	select {
	case r := <-cs.Result:
		t.Fatalf("must not deliver on forged state, got %+v", r)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestCallbackServerDeliversProviderError(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?error=access_denied&state=%s", cs.Port, url.QueryEscape(state)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case r := <-cs.Result:
		if r.Err == nil || !strings.Contains(r.Err.Error(), "access_denied") {
			t.Fatalf("result = %+v, want access_denied error", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result delivered")
	}
}

func TestCallbackServerConcurrentDuplicatesOneShot(t *testing.T) {
	cs, err := startCallbackServer()
	if err != nil {
		t.Fatal(err)
	}
	defer cs.Close()
	state, _ := genState(cs.Port)
	cs.ExpectState(state)

	const n = 8
	statuses := make(chan int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/callback?code=C&state=%s", cs.Port, url.QueryEscape(state)))
			if err != nil {
				statuses <- -1
				return
			}
			resp.Body.Close()
			statuses <- resp.StatusCode
		}()
	}
	wg.Wait()
	close(statuses)
	ok, bad := 0, 0
	for s := range statuses {
		if s == http.StatusOK {
			ok++
		} else {
			bad++
		}
	}
	if ok != 1 || bad != n-1 {
		t.Fatalf("ok=%d bad=%d, want exactly one 200", ok, bad)
	}
	select {
	case r := <-cs.Result:
		if r.Err != nil || r.Code != "C" {
			t.Fatalf("result = %+v", r)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no result delivered")
	}
	select {
	case r := <-cs.Result:
		t.Fatalf("second result delivered: %+v", r)
	case <-time.After(200 * time.Millisecond):
	}
}
