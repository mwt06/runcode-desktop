package desktop

import "testing"

func TestGetWindowCapabilitiesIndependentOfAutomation(t *testing.T) {
	app := New(nil)
	view, err := app.GetWindowCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if view.Available {
		t.Fatal("expected unavailable before SetWindowController")
	}
	app.SetWindowController(automationWindow{
		closeFn:    func() error { return nil },
		exitFn:     func() error { return nil },
		background: true,
	})
	view, err = app.GetWindowCapabilities()
	if err != nil {
		t.Fatal(err)
	}
	if !view.Available || !view.BackgroundSupported {
		t.Fatalf("%+v", view)
	}
	// Must not require GetAutomationDefaults / task store.
}
