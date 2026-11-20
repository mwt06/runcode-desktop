package desktop

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	appwire "github.com/wt68/runcode/internal/protocol"
)

type catalogFixture struct {
	Response json.RawMessage `json:"response"`
	Expect   struct {
		ChatModels            []string `json:"chatModels"`
		ImageGenerationModels []string `json:"imageGenerationModels"`
		SelectableImageModels []string `json:"selectableImageModels"`
	} `json:"expect"`
}

func readImageGenerationFixture(t *testing.T, name string, out any) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "image_generation", name))
	if err != nil {
		t.Fatal(err)
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	return raw
}

func loadCatalogFixture(t *testing.T) catalogFixture {
	t.Helper()
	var fx catalogFixture
	readImageGenerationFixture(t, "catalog.json", &fx)
	return fx
}

func modelIDs(models []PassportModel) []string {
	out := make([]string, 0, len(models))
	for _, m := range models {
		out = append(out, m.ID)
	}
	return out
}

func TestImageGenerationCatalogSplitsTextAndImagePurposes(t *testing.T) {
	fx := loadCatalogFixture(t)
	models, err := decodePassportModels(fx.Response)
	if err != nil {
		t.Fatal(err)
	}
	var chat, image []string
	for _, m := range models {
		if isChatModel(m) {
			chat = append(chat, m.ID)
		}
		if isImageGenerationModel(m) {
			image = append(image, m.ID)
		}
	}
	if !slices.Equal(chat, fx.Expect.ChatModels) {
		t.Fatalf("chat models = %v, want %v", chat, fx.Expect.ChatModels)
	}
	if !slices.Equal(image, fx.Expect.ImageGenerationModels) {
		t.Fatalf("image models = %v, want %v", image, fx.Expect.ImageGenerationModels)
	}
}

func TestImageGenerationCatalogDecodesProfileWithoutGuessing(t *testing.T) {
	fx := loadCatalogFixture(t)
	models, err := decodePassportModels(fx.Response)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]PassportModel{}
	for _, m := range models {
		byID[m.ID] = m
	}
	url := byID["image-only-url"]
	if url.ImageGeneration == nil || !url.ImageGeneration.Available || url.ImageGeneration.DefaultSize != "2048x2048" ||
		!slices.Equal(url.ImageGeneration.SupportedSizes, []string{"2048x2048"}) || !slices.Equal(url.ImageGeneration.ResponseFormats, []string{"url"}) {
		t.Fatalf("profile not decoded: %+v", url.ImageGeneration)
	}
	// A name that sounds like an image model and an image-input flag are not image output.
	for _, id := range []string{"seedream-name-only", "chat-explicit", "chat-legacy"} {
		if isImageGenerationModel(byID[id]) {
			t.Fatalf("%s guessed as image generation model", id)
		}
	}
	if byID["chat-explicit"].SupportsImages == nil || !*byID["chat-explicit"].SupportsImages {
		t.Fatal("supports_images no longer decoded")
	}
}

func TestImageGenerationCatalogCandidatesMarkUnusableProfiles(t *testing.T) {
	isolateConfigDir(t)
	fx := loadCatalogFixture(t)
	models, err := decodePassportModels(fx.Response)
	if err != nil {
		t.Fatal(err)
	}
	candidates := imageGenerationCandidates(models, "tenant-a")
	var all, selectable []string
	for _, c := range candidates {
		all = append(all, c.Model.Name)
		if c.Model != platformModelRef("tenant-a", c.Model.Name) {
			t.Fatalf("candidate %s not pinned to bridge/tenant: %+v", c.Model.Name, c.Model)
		}
		if c.Profile.Available {
			selectable = append(selectable, c.Model.Name)
		} else if strings.TrimSpace(c.Profile.UnavailableReason) == "" {
			t.Fatalf("unavailable candidate %s has no reason", c.Model.Name)
		}
	}
	if !slices.Equal(all, fx.Expect.ImageGenerationModels) {
		t.Fatalf("candidates = %v, want %v", all, fx.Expect.ImageGenerationModels)
	}
	if !slices.Equal(selectable, fx.Expect.SelectableImageModels) {
		t.Fatalf("selectable = %v, want %v", selectable, fx.Expect.SelectableImageModels)
	}
}

func TestImageGenerationCatalogProfileValidation(t *testing.T) {
	cases := []struct {
		name    string
		profile appwire.ImageGenerationProfile
		ok      bool
	}{
		{"valid", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"1024x1024"}, DefaultSize: "1024x1024", ResponseFormats: []string{"url"}}, true},
		{"canonical star syntax", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"1024*1024"}, DefaultSize: "1024*1024", ResponseFormats: []string{"url"}}, true},
		{"default not supported", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"1024x1024"}, DefaultSize: "2048x2048", ResponseFormats: []string{"url"}}, false},
		{"no formats", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"1024x1024"}, DefaultSize: "1024x1024"}, false},
		{"unknown format", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"1024x1024"}, DefaultSize: "1024x1024", ResponseFormats: []string{"stream"}}, false},
		{"malformed size", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"big"}, DefaultSize: "big", ResponseFormats: []string{"url"}}, false},
		{"over pixel limit", appwire.ImageGenerationProfile{Available: true, SupportedSizes: []string{"8192x8192"}, DefaultSize: "8192x8192", ResponseFormats: []string{"url"}}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := normalizeImageGenerationProfile(&tc.profile)
			if got.Available != tc.ok {
				t.Fatalf("available = %v, want %v (%+v)", got.Available, tc.ok, got)
			}
		})
	}
}

func TestImageGenerationCatalogSnapshotIsIndependentOfCacheEviction(t *testing.T) {
	models := []PassportModel{{
		ID:           "image-only",
		Capabilities: []string{capabilityImageGeneration},
		ImageGeneration: &ImageGenerationProfile{
			Available: true, SupportedSizes: []string{"1024*1024"}, DefaultSize: "1024*1024", ResponseFormats: []string{"url"},
		},
	}}
	snapshot := modelCatalogFromModels(models)
	if _, ok := snapshot.imageGeneration["image-only"]; !ok {
		t.Fatal("fresh snapshot lost image model")
	}
	if got := snapshot.imageGeneration["image-only"].ImageGeneration.SupportedSizes; !slices.Equal(got, []string{"1024*1024"}) {
		t.Fatalf("snapshot sizes = %v", got)
	}
}

func serveCatalog(t *testing.T, status int, body []byte) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1/models") {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("RUNCODE_BRIDGE_BASE_URL", srv.URL)
}

func TestImageGenerationCatalogKeepsImageOnlyModelsOutOfTextCatalogs(t *testing.T) {
	isolateConfigDir(t)
	fx := loadCatalogFixture(t)
	serveCatalog(t, http.StatusOK, fx.Response)
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	models, err := app.PassportModels("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if got := modelIDs(models); !slices.Equal(got, fx.Expect.ChatModels) {
		t.Fatalf("PassportModels = %v, want chat-only %v", got, fx.Expect.ChatModels)
	}
	catalog, err := app.platformModelCatalog(t.Context(), platformModelRef("tenant-a", ""), app.visionAccountIdentity())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := catalog.models["image-only-url"]; ok {
		t.Fatal("image-only model reachable by text-purpose catalog lookups")
	}
	if _, ok := catalog.imageGeneration["image-only-url"]; !ok {
		t.Fatal("image catalog lost the image-only model")
	}
}

func TestImageGenerationCatalogErrorIsNotEmpty(t *testing.T) {
	isolateConfigDir(t)
	serveCatalog(t, http.StatusBadGateway, []byte(`bridge down`))
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	if _, err := app.ListImageGenerationModels("tenant-a"); err == nil {
		t.Fatal("catalog failure reported as an empty catalog")
	}
}

func TestImageGenerationCatalogEmptyIsExplicit(t *testing.T) {
	isolateConfigDir(t)
	serveCatalog(t, http.StatusOK, []byte(`{"data":[{"id":"chat-only","owned_by":"p"}]}`))
	app := New(&recordingSink{})
	app.tokens.setInMemory(tokenSet{AccessToken: imageGenJWT("user-a"), Expiry: time.Now().Add(time.Hour)})
	catalog, err := app.ListImageGenerationModels("tenant-a")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Models == nil || len(catalog.Models) != 0 || catalog.ScopeID == "" {
		t.Fatalf("empty catalog = %+v", catalog)
	}
}
