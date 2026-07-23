package desktop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
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

func TestTokenEmptyAccessWithRefreshTriggersRefresh(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT2","refresh_token":"RT2","expires_in":3600}`))
	}))
	defer srv.Close()
	tm := newTokenManager(srv.URL, "c", srv.Client(), nil)
	tm.setInMemory(tokenSet{AccessToken: "", RefreshToken: "RT1", Expiry: time.Now().Add(time.Hour)})
	tok, err := tm.Token()
	if err != nil || tok != "AT2" {
		t.Fatalf("tok=%q err=%v, want refreshed AT2 (never empty with nil error)", tok, err)
	}
}

func TestTokenConcurrentCallersSingleRefresh(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		atomic.AddInt32(&calls, 1)
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"AT2","refresh_token":"RT2","expires_in":3600}`))
	}))
	defer srv.Close()
	tm := newTokenManager(srv.URL, "c", srv.Client(), nil)
	tm.setInMemory(tokenSet{AccessToken: "AT1", RefreshToken: "RT1", Expiry: time.Now().Add(10 * time.Second)})

	const n = 5
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tok, err := tm.Token()
			if err != nil || tok != "AT2" {
				errs <- fmt.Errorf("tok=%q err=%v", tok, err)
				return
			}
			errs <- nil
		}()
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		if e != nil {
			t.Fatal(e)
		}
	}
	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1 (one-time refresh token must not be double-spent)", got)
	}
}
