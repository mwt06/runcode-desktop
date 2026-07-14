package desktop

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestTokenReturnsValidAccessToken(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	tm := newTokenManager("http://unused", "c", http.DefaultClient, nil)
	tm.setInMemory(tokenSet{AccessToken: "AT", RefreshToken: "RT", Expiry: time.Now().Add(time.Hour)})
	tok, err := tm.Token()
	if err != nil || tok != "AT" {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
}

func TestTokenRefreshesNearExpiry(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT2","refresh_token":"RT2","expires_in":3600}`))
	}))
	defer srv.Close()

	tm := newTokenManager(srv.URL, "c", srv.Client(), nil)
	tm.setInMemory(tokenSet{AccessToken: "AT1", RefreshToken: "RT1", Expiry: time.Now().Add(10 * time.Second)}) // <60s
	tok, err := tm.Token()
	if err != nil || tok != "AT2" {
		t.Fatalf("tok=%q err=%v, want refreshed AT2", tok, err)
	}
	if !tm.LoggedIn() {
		t.Fatal("still logged in after refresh")
	}
}

func TestTokenRefreshFailureLogsOut(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant"}`))
	}))
	defer srv.Close()

	loggedOut := false
	tm := newTokenManager(srv.URL, "c", srv.Client(), func() { loggedOut = true })
	tm.setInMemory(tokenSet{AccessToken: "AT1", RefreshToken: "expired", Expiry: time.Now().Add(-time.Minute)})
	if _, err := tm.Token(); err == nil {
		t.Fatal("want error when refresh fails")
	}
	if !loggedOut || tm.LoggedIn() {
		t.Fatalf("loggedOut=%v LoggedIn=%v, want logout on refresh failure", loggedOut, tm.LoggedIn())
	}
}

func TestTokenWithoutLoginErrors(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	tm := newTokenManager("http://unused", "c", http.DefaultClient, nil)
	if _, err := tm.Token(); err == nil {
		t.Fatal("want error when not logged in")
	}
}
