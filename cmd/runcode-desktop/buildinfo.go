package main

import (
	"encoding/json"
	"io"

	"github.com/wt68/runcode/internal/desktop"
)

// printBuildInfo reports the values the app actually uses, without opening a
// window or touching user settings. Go's -trimpath omits -ldflags from build
// metadata, so packaging cannot verify injected identity through go version -m.
func printBuildInfo(args []string, out io.Writer) (bool, error) {
	if len(args) != 2 || args[1] != "--build-info" {
		return false, nil
	}
	info := struct {
		Name     string `json:"name"`
		BundleID string `json:"bundleID"`
		Version  string `json:"version"`
		Product  string `json:"product"`
	}{brandTitle, brandID, desktop.AppVersion(), desktop.AppProduct()}
	return true, json.NewEncoder(out).Encode(info)
}
