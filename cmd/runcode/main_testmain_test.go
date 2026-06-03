package main

import (
	"os"
	"testing"
)

// TestMain isolates configuration discovery so the cmd tests never read a
// developer's real config: it points the per-user config dir at an empty temp
// directory and runs from that directory, so neither <UserConfigDir>/runcode/
// config.toml nor a project-level runcode.toml on the path can leak in and break
// "requires ..." assertions.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "runcode-test-config")
	if err != nil {
		panic(err)
	}
	// Cover os.UserConfigDir() across platforms.
	os.Setenv("AppData", dir)         // Windows
	os.Setenv("XDG_CONFIG_HOME", dir) // Linux
	os.Setenv("HOME", dir)            // macOS

	orig, _ := os.Getwd()
	_ = os.Chdir(dir)

	code := m.Run()

	_ = os.Chdir(orig)
	os.RemoveAll(dir)
	os.Exit(code)
}
