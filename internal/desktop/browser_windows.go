package desktop

import (
	"context"

	"golang.org/x/sys/windows"
)

func openBrowserContext(ctx context.Context, url string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(url)
	if err != nil {
		return err
	}
	// A native URL handoff, not cmd /c start or a hidden rundll32 console.
	return windows.ShellExecute(0, nil, target, nil, nil, windows.SW_SHOWNORMAL)
}
