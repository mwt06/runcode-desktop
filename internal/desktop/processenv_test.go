package desktop

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestProcessEnvInheritsAndOverridesOnEveryPlatform(t *testing.T) {
	t.Setenv("RUNCODE_ENV_TEST_KEEP", "keep")
	t.Setenv("RUNCODE_ENV_TEST_REPLACE", "old")
	env := envWith(map[string]string{
		"RUNCODE_ENV_TEST_REPLACE": "new=value",
		"SUDO_ASKPASS":             "/Applications/App.app/Contents/MacOS/App",
		envAskpassSocket:           "/tmp/test.sock",
	})
	values := map[string]string{}
	counts := map[string]int{}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		values[k] = v
		counts[k]++
	}
	if values["RUNCODE_ENV_TEST_KEEP"] != "keep" || values["RUNCODE_ENV_TEST_REPLACE"] != "new=value" {
		t.Fatal("child environment not inherited/overridden")
	}
	if counts["RUNCODE_ENV_TEST_REPLACE"] != 1 || values["SUDO_ASKPASS"] == "" || values[envAskpassSocket] == "" {
		t.Fatal("askpass environment missing or duplicated")
	}
	if os.Getenv("RUNCODE_ENV_TEST_REPLACE") != "old" {
		t.Fatal("mutated process environment")
	}
	if len(envWith(nil)) == 0 {
		t.Fatal("nil extra must still inherit process environment")
	}
}

func TestWindowsProcessEnvKeysAreCaseInsensitive(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows environment semantics")
	}
	t.Setenv("RUNCODE_ENV_TEST_CASE", "old")
	var entries []string
	for _, kv := range envWith(map[string]string{"runcode_env_test_case": "new"}) {
		k, _, _ := strings.Cut(kv, "=")
		if strings.EqualFold(k, "RUNCODE_ENV_TEST_CASE") {
			entries = append(entries, kv)
		}
	}
	if len(entries) != 1 || entries[0] != "runcode_env_test_case=new" {
		t.Fatalf("case override: %v", entries)
	}
}
