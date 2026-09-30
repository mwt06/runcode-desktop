package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"testing"

	"github.com/wt68/runcode/internal/desktop"
)

func TestPrintBuildInfo(t *testing.T) {
	var out bytes.Buffer
	handled, err := printBuildInfo([]string{"desktop", "--build-info"}, &out)
	if !handled || err != nil {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
	var got map[string]string
	if err := json.Unmarshal(out.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"name": brandTitle, "bundleID": brandID, "version": desktop.AppVersion(), "product": desktop.AppProduct()}
	if len(got) != len(want) {
		t.Fatalf("unexpected fields: %v", got)
	}
	for key, value := range want {
		if got[key] != value {
			t.Fatalf("%s = %q, want %q", key, got[key], value)
		}
	}
	for _, args := range [][]string{nil, {"desktop"}, {"desktop", "other"}, {"desktop", "--build-info", "other"}} {
		out.Reset()
		handled, err := printBuildInfo(args, &out)
		if handled || err != nil || out.Len() != 0 {
			t.Fatalf("unexpected handling of %v: handled=%v err=%v", args, handled, err)
		}
	}
}

type failedBuildInfoWriter struct{ err error }

func (w failedBuildInfoWriter) Write([]byte) (int, error) { return 0, w.err }

func TestPrintBuildInfoWriteFailure(t *testing.T) {
	failure := errors.New("output closed")
	handled, err := printBuildInfo([]string{"desktop", "--build-info"}, failedBuildInfoWriter{failure})
	if !handled || !errors.Is(err, failure) {
		t.Fatalf("handled=%v err=%v", handled, err)
	}
}
