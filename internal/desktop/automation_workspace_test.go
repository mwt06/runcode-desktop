package desktop

import (
	"errors"
	"testing"
)

func TestAutomationWorkspacePickerContract(t *testing.T) {
	path := t.TempDir() + "/中文 目录"
	app := &App{dialog: fakeDialoger{path: path}}

	selected, err := app.PickWorkspaceFolder()
	if err != nil {
		t.Fatalf("pick workspace: %v", err)
	}
	if selected != path {
		t.Fatalf("selected = %q, want %q", selected, path)
	}

	app.dialog = fakeDialoger{}
	cancelled, err := app.PickWorkspaceFolder()
	if err != nil {
		t.Fatalf("cancel workspace picker: %v", err)
	}
	if cancelled != "" {
		t.Fatalf("cancelled picker returned %q, want empty", cancelled)
	}

	app.dialog = fakeDialoger{err: errors.New("cancelled by user")}
	cancelled, err = app.PickWorkspaceFolder()
	if err != nil || cancelled != "" {
		t.Fatalf("native cancellation = %q, %v; want empty without an error", cancelled, err)
	}

	app.dialog = fakeDialoger{err: errors.New("无法打开目录选择器")}
	if _, err := app.PickWorkspaceFolder(); err == nil {
		t.Fatal("real picker failures must remain visible")
	}

	app.dialog = nil
	if _, err := app.PickWorkspaceFolder(); err == nil {
		t.Fatal("unsupported desktop dialog must return an error")
	}
}
