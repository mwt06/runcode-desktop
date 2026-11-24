package desktop

import (
	"sync"
	"testing"

	"github.com/wt68/runcode/internal/protocol"
)

func TestPatchRecorderSettingsPreservesUneditedFields(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	base := protocol.RecorderSettings{
		GatewayURL:      "ws://recorder.test/ws",
		GatewayToken:    "token",
		SpeakerName:     "old",
		Lang:            "zh",
		MicDiarize:      true,
		MicDeviceID:     "mic-1",
		SysDeviceID:     "sys-1",
		KeepAudio:       true,
		AutoFullMinutes: false,
		SummaryModel:    "summary",
		Root:            t.TempDir(),
	}
	if err := app.SaveRecorderSettings(base); err != nil {
		t.Fatal(err)
	}
	name := "new"
	if _, err := app.PatchRecorderSettings(protocol.RecorderSettingsPatch{SpeakerName: &name}); err != nil {
		t.Fatal(err)
	}
	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.SpeakerName != "new" || got.Lang != "zh" || got.SummaryModel != "summary" || got.Root != base.Root {
		t.Fatalf("patch lost unedited fields: %+v", got)
	}
}

func TestPatchRecorderSettingsDistinguishesFalseAndEmptyFromOmitted(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	base := protocol.RecorderSettings{
		GatewayURL: "ws://recorder.test/ws", GatewayToken: "token", SpeakerName: "old",
		MicDiarize: true, KeepAudio: true,
	}
	if err := app.SaveRecorderSettings(base); err != nil {
		t.Fatal(err)
	}
	off := false
	empty := ""
	if _, err := app.PatchRecorderSettings(protocol.RecorderSettingsPatch{
		SpeakerName: &empty,
		KeepAudio:   &off,
	}); err != nil {
		t.Fatal(err)
	}
	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.SpeakerName != "" || got.KeepAudio || !got.MicDiarize {
		t.Fatalf("explicit values not applied: %+v", got)
	}
}

func TestPatchRecorderSettingsConcurrentFieldsDoNotOverwrite(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	if err := app.SaveRecorderSettings(protocol.RecorderSettings{GatewayURL: "ws://recorder.test/ws"}); err != nil {
		t.Fatal(err)
	}
	name, lang := "name", "en"
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := app.PatchRecorderSettings(protocol.RecorderSettingsPatch{SpeakerName: &name}); err != nil {
			t.Error(err)
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := app.PatchRecorderSettings(protocol.RecorderSettingsPatch{Lang: &lang}); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.SpeakerName != name || got.Lang != lang {
		t.Fatalf("concurrent patch lost a field: %+v", got)
	}
}

func TestPatchRecorderSettingsRejectsInvalidGatewayURL(t *testing.T) {
	isolateConfigDir(t)
	app := New(&recordingSink{})
	base := protocol.RecorderSettings{GatewayURL: "ws://recorder.test/ws"}
	if err := app.SaveRecorderSettings(base); err != nil {
		t.Fatal(err)
	}
	bad := "http://invalid"
	if _, err := app.PatchRecorderSettings(protocol.RecorderSettingsPatch{GatewayURL: &bad}); err == nil {
		t.Fatal("invalid gateway accepted")
	}
	got, err := app.RecorderSettings()
	if err != nil {
		t.Fatal(err)
	}
	if got.GatewayURL != base.GatewayURL {
		t.Fatalf("failed patch changed gateway: %+v", got)
	}
}
