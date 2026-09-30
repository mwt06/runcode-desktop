// Package appupdate owns the application update lifecycle, independently of the
// desktop host. Platform installation and transport wiring are injected by it.
package appupdate

import (
	"context"
	"net/http"
	"os"

	"github.com/wt68/runcode/internal/protocol"
)

// InstallRequest is an immutable snapshot of the verified update candidate.
// Privileged installers must reverify private copies against this same SHA256.
type InstallRequest struct {
	File    string
	Version string
	SHA256  string
}

// Options supplies host capabilities. CacheDir must be configured for file operations;
// FetchToFile defaults to the shared downloader. Configure once before sharing Service.
type Options struct {
	Current     string
	Product     string
	Platform    string
	Endpoint    string
	HTTP        *http.Client
	Token       func() string
	CacheDir    func() (string, error)
	FetchToFile func(context.Context, string, string, *os.File, int64, func(int64, int64)) (string, int64, error)
	Install     func(InstallRequest) error
	Reveal      func(string) error
	Quit        func()
	Log         func(string, ...any)
	Emit        func(protocol.UpdateInfo)
	CanInstall  bool
	AutoRestart bool
	InstallHint string
}
