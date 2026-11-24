//go:build !windows && !kylin

package main

import (
	"github.com/wailsapp/wails/v3/pkg/application"
	"github.com/wt68/runcode/internal/desktop"
)

func installAutomationTray(
	_ *application.App,
	_ *application.WebviewWindow,
	_ *desktop.WindowLifecycle,
	_ string,
	_ func(),
) {
}
