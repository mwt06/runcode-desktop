package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appwire "github.com/wt68/runcode/internal/protocol"
)

func sampleLedger(status string) imageGenerationLedger {
	return imageGenerationLedger{
		GenerationID: "gen_1", SessionID: "sess_1", TurnID: "turn_1", QuestionID: "q_1", ToolUseID: "tu_1",
		ScopeID: strings.Repeat("a", 64), Model: "image-only-url", Size: "2048x2048", Status: status,
	}
}

func TestImageGenerationStoreAccountingIndependent(t *testing.T) {
	var fx struct {
		Cases []struct {
			Name   string `json:"name"`
			Ledger struct {
				Status           string  `json:"status"`
				AccountingStatus *string `json:"accountingStatus"`
			} `json:"ledger"`
			Expect struct {
				AccountingStatus string `json:"accountingStatus"`
			} `json:"expect"`
		} `json:"cases"`
	}
	readImageGenerationFixture(t, "accounting-observations.json", &fx)
	if len(fx.Cases) == 0 {
		t.Fatal("no fixture cases")
	}
	for _, c := range fx.Cases {
		t.Run(c.Name, func(t *testing.T) {
			ws := t.TempDir()
			rec := sampleLedger(c.Ledger.Status)
			if c.Ledger.AccountingStatus != nil {
				rec.AccountingStatus = *c.Ledger.AccountingStatus
			}
			result := appwire.ImageGenerationResult{Version: 1, GenerationID: rec.GenerationID, Status: c.Ledger.Status, Model: rec.Model, Size: rec.Size, Images: []appwire.ImageGenerationArtifact{}}
			if c.Ledger.Status == "succeeded" {
				result.Images = []appwire.ImageGenerationArtifact{{Path: "生成图片/gen_1/image.png", MIMEType: "image/png", Bytes: 1, Width: 1, Height: 1}}
			}
			rec.Result = &result
			if err := writeImageGenerationLedger(ws, rec); err != nil {
				t.Fatal(err)
			}
			got, err := readImageGenerationLedger(ws, rec.SessionID, rec.GenerationID)
			if err != nil {
				t.Fatal(err)
			}
			if eff := got.effectiveResult(); eff.AccountingStatus != c.Expect.AccountingStatus {
				t.Fatalf("accounting = %q, want %q", eff.AccountingStatus, c.Expect.AccountingStatus)
			}
		})
	}
}

func TestImageGenerationStoreFinalizeKeepsObservation(t *testing.T) {
	rec := sampleLedger("submitted")
	rec.AccountingStatus = "observed_mismatch"
	done := rec.finalized(appwire.ImageGenerationResult{Status: "succeeded", AccountingStatus: "verified", RequestID: "brg-1"})
	if done.AccountingStatus != "observed_mismatch" || done.Result.AccountingStatus != "observed_mismatch" {
		t.Fatalf("file status overwrote the observation: %+v", done)
	}
	if done.Status != "succeeded" || done.RequestID != "brg-1" || done.Result.RequestID != "brg-1" {
		t.Fatalf("finalized = %+v", done)
	}
	fresh := sampleLedger("submitted").finalized(appwire.ImageGenerationResult{Status: "succeeded", AccountingStatus: "verified"})
	if fresh.AccountingStatus != "unverified" {
		t.Fatalf("automatic result promoted accounting to %q", fresh.AccountingStatus)
	}
}

func TestImageGenerationStoreSubmissionDispositionRoundTripsAndStaysConservative(t *testing.T) {
	ws := t.TempDir()
	rec := sampleLedger("submitted")
	rec.SubmissionStatus = "attempted"
	if err := writeImageGenerationLedger(ws, rec); err != nil {
		t.Fatal(err)
	}
	got, err := readImageGenerationLedger(ws, rec.SessionID, rec.GenerationID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SubmissionStatus != "attempted" {
		t.Fatalf("submission status = %q, want attempted", got.SubmissionStatus)
	}
	done := got.finalized(appwire.ImageGenerationResult{Status: "failed", SubmissionStatus: "not_submitted"})
	if done.SubmissionStatus != "attempted" || done.effectiveResult().SubmissionStatus != "attempted" {
		t.Fatalf("finalization downgraded attempted submission: %+v", done)
	}

	// 旧账本没有字段，或字段被破坏，都只能按 unknown 解释；不能从 submitted 推断网络状态。
	for _, raw := range []string{
		`{"version":1,"generationId":"gen_1","sessionId":"sess_1","turnId":"turn_1","status":"submitted"}`,
		`{"version":1,"generationId":"gen_1","sessionId":"sess_1","turnId":"turn_1","status":"submitted","submissionStatus":"bogus"}`,
	} {
		legacy := sampleLedger("submitted")
		path, err := imageGenerationLedgerPath(legacy.SessionID, legacy.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeRootedFileAtomic(ws, path, []byte(raw)); err != nil {
			t.Fatal(err)
		}
		decoded, err := readImageGenerationLedger(ws, legacy.SessionID, legacy.GenerationID)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.SubmissionStatus != "unknown" || decoded.effectiveResult().SubmissionStatus != "unknown" {
			t.Fatalf("legacy submission status = %q, result = %q", decoded.SubmissionStatus, decoded.effectiveResult().SubmissionStatus)
		}
	}
}

func TestImageGenerationStoreRecoversSubmittedAsUnknown(t *testing.T) {
	var fx struct {
		Cases []struct {
			Name   string `json:"name"`
			Ledger *struct {
				Status string `json:"status"`
			} `json:"ledger"`
			Expect struct {
				Status string `json:"status"`
				Code   string `json:"code"`
			} `json:"expect"`
		} `json:"cases"`
	}
	readImageGenerationFixture(t, "uncertain-outcomes.json", &fx)
	found := false
	for _, c := range fx.Cases {
		if c.Ledger == nil {
			continue
		}
		found = true
		ws := t.TempDir()
		if err := writeImageGenerationLedger(ws, sampleLedger(c.Ledger.Status)); err != nil {
			t.Fatal(err)
		}
		got, err := readImageGenerationLedger(ws, "sess_1", "gen_1")
		if err != nil {
			t.Fatal(err)
		}
		eff := got.effectiveResult()
		if eff.Status != c.Expect.Status || eff.Code != c.Expect.Code || len(eff.Images) != 0 || !strings.Contains(eff.Message, "可能已生成") {
			t.Fatalf("%s recovered as %+v", c.Name, eff)
		}
	}
	if !found {
		t.Fatal("fixture has no ledger case")
	}
}

func TestImageGenerationStoreTurnReservationIsExclusive(t *testing.T) {
	ws := t.TempDir()
	if err := reserveImageGenerationTurn(ws, "sess_1", "turn_1", "gen_1"); err != nil {
		t.Fatal(err)
	}
	// A second tool instance or a restarted process sees the same durable marker.
	if err := reserveImageGenerationTurn(ws, "sess_1", "turn_1", "gen_2"); !errors.Is(err, errImageGenerationTurnSpent) {
		t.Fatalf("second reservation = %v", err)
	}
	if err := reserveImageGenerationTurn(ws, "sess_1", "turn_2", "gen_3"); err != nil {
		t.Fatalf("new turn denied: %v", err)
	}
	if err := reserveImageGenerationTurn(ws, "sess_1", "../turn", "gen_4"); err == nil {
		t.Fatal("invalid turn id accepted")
	}
}

func TestImageGenerationStoreRejectsDamageAndSecrets(t *testing.T) {
	ws := t.TempDir()
	rec := sampleLedger("succeeded")
	rec.Result = &appwire.ImageGenerationResult{Version: 1, Status: "succeeded", Images: []appwire.ImageGenerationArtifact{{Path: "生成图片/gen_1/image.png"}}}
	if err := writeImageGenerationLedger(ws, rec); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(ws, ".runcode", "image-generation", "requests", "sess_1", "gen_1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"b64", "base64", "http", "Bearer", "token"} {
		if strings.Contains(strings.ToLower(string(raw)), strings.ToLower(bad)) {
			t.Fatalf("ledger contains %q: %s", bad, raw)
		}
	}
	if err := os.WriteFile(filepath.Join(ws, ".runcode", "image-generation", "requests", "sess_1", "gen_1.json"), []byte(`{"version":1,`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := readImageGenerationLedger(ws, "sess_1", "gen_1"); err == nil {
		t.Fatal("damaged ledger read as valid")
	}
	list, err := listImageGenerationLedgers(ws, "sess_1")
	if err == nil {
		t.Fatalf("damaged ledger silently skipped: %+v", list)
	}
	if empty, err := listImageGenerationLedgers(t.TempDir(), "sess_1"); err != nil || len(empty) != 0 {
		t.Fatalf("missing ledger dir = %v %v", empty, err)
	}
}
