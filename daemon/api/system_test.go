package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/api"
	"github.com/jumpingmushroom/DiscEcho/daemon/settings"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestGetSystemHost_OK(t *testing.T) {
	h := apitestServer(t)
	libDir := t.TempDir()
	h.Settings = &settings.Settings{
		LibraryRoot:   libDir,
		LibraryMovies: libDir,
		DataPath:      "/path/that/definitely/does/not/exist/abcxyz",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/host", nil)
	w := httptest.NewRecorder()
	h.GetSystemHost(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var info api.HostInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.CPUCount != runtime.NumCPU() {
		t.Errorf("cpu_count: got %d want %d", info.CPUCount, runtime.NumCPU())
	}
	// Library path exists (TempDir) → at least one disk row.
	// Data path doesn't exist → silently skipped, no 500.
	if len(info.Disks) == 0 {
		t.Error("expected at least the library disk entry")
	}
	for _, d := range info.Disks {
		if d.Path == "/path/that/definitely/does/not/exist/abcxyz" {
			t.Errorf("missing path should be skipped, got %+v", d)
		}
	}
}

func TestGetSystemHost_NilSettings_DefaultsAreSafe(t *testing.T) {
	h := apitestServer(t)
	h.Settings = nil

	req := httptest.NewRequest(http.MethodGet, "/api/system/host", nil)
	w := httptest.NewRecorder()
	h.GetSystemHost(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
}

func TestGetSystemIntegrations_TMDBUnconfigured(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		TMDBKey:              "",
		TMDBLang:             "en-US",
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "/nonexistent/apprise-binary",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var info api.IntegrationsInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	if info.TMDB.Configured {
		t.Error("TMDB should be unconfigured when key is empty")
	}
	if info.TMDB.Language != "en-US" {
		t.Errorf("language: %q", info.TMDB.Language)
	}
	if info.MusicBrainz.UserAgent != "DiscEcho/test" {
		t.Errorf("user-agent: %q", info.MusicBrainz.UserAgent)
	}
	if info.Apprise.Bin != "/nonexistent/apprise-binary" {
		t.Errorf("apprise bin: %q", info.Apprise.Bin)
	}
	// Bogus binary → version omitted, no 500.
	if info.Apprise.Version != "" {
		t.Errorf("apprise version unexpectedly set: %q", info.Apprise.Version)
	}
}

func TestGetSystemIntegrations_ItemsList(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		TMDBKey:              "real-key",
		TMDBLang:             "en-US",
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "/nonexistent/apprise-binary",
		RedumperBin:          "/nonexistent/redumper-binary",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var info api.IntegrationsInfo
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	if len(info.Items) != 3 {
		t.Fatalf("Items len = %d, want 3", len(info.Items))
	}
	// Game discs is no longer a flat item — it has its own GameDiscs block.
	want := []string{"MusicBrainz", "Apprise", "GPU transcoding"}
	for i, name := range want {
		if info.Items[i].Name != name {
			t.Errorf("Items[%d].Name = %q, want %q", i, info.Items[i].Name, name)
		}
	}
	// MusicBrainz always connected.
	if info.Items[0].Status != "connected" {
		t.Errorf("MusicBrainz status = %q, want connected", info.Items[0].Status)
	}
	// Bogus redumper bin → error surfaced in the Game discs block.
	if info.GameDiscs == nil || !strings.HasPrefix(info.GameDiscs.Status, "error:") {
		t.Errorf("GameDiscs status = %+v, want error: prefix", info.GameDiscs)
	}
	// Apprise: no URLs configured (empty notifications list).
	if info.Items[1].Status != "no URLs configured" {
		t.Errorf("Apprise status = %q, want no URLs configured", info.Items[1].Status)
	}
}

func TestGetSystemIntegrations_MusicBrainzConnected(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "apprise",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var info api.IntegrationsInfo
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	// MusicBrainz is now Items[0] (TMDB moved to /api/integrations).
	if info.Items[0].Name != "MusicBrainz" {
		t.Errorf("Items[0].Name = %q, want MusicBrainz", info.Items[0].Name)
	}
	if info.Items[0].Status != "connected" {
		t.Errorf("MusicBrainz status = %q, want connected", info.Items[0].Status)
	}
}

func TestGetSystemIntegrations_LibraryRoots(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		LibraryRoot:   "/library",
		LibraryMovies: "/library/movies",
		LibraryTV:     "/library/tv",
		LibraryMusic:  "/srv/audio",
		LibraryGames:  "/library/games",
		LibraryData:   "/library/data",
		AppriseBin:    "apprise",
	}
	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var info api.IntegrationsInfo
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	for k, want := range map[string]string{
		"movies": "/library/movies",
		"tv":     "/library/tv",
		"music":  "/srv/audio",
		"games":  "/library/games",
		"data":   "/library/data",
	} {
		if got := info.LibraryRoots[k]; got != want {
			t.Errorf("library_roots[%q] = %q, want %q", k, got, want)
		}
	}
}

func TestGetSystemIntegrations_TMDBConfigured(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		TMDBKey:              "secret-key",
		TMDBLang:             "fr-FR",
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "apprise",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var info api.IntegrationsInfo
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	if !info.TMDB.Configured {
		t.Error("TMDB should be configured")
	}
	// Body must not contain the secret key.
	if strings.Contains(w.Body.String(), "secret-key") {
		t.Errorf("response leaks TMDB key: %s", w.Body.String())
	}
}

func TestGetSystemIntegrations_GPU_Connected(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "apprise",
	}
	h.NVENCAvailable = true

	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var info api.IntegrationsInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}
	row := findIntegrationItem(info.Items, "GPU transcoding")
	if row == nil {
		t.Fatal(`"GPU transcoding" row missing`)
	}
	if row.Status != "connected" {
		t.Errorf("status: got %q, want connected", row.Status)
	}
	if !strings.Contains(row.Detail, "NVENC") {
		t.Errorf("detail: got %q, want it to mention NVENC", row.Detail)
	}
}

func TestGetSystemIntegrations_GPU_NotDetected(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "apprise",
	}
	h.NVENCAvailable = false

	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var info api.IntegrationsInfo
	_ = json.Unmarshal(w.Body.Bytes(), &info)

	row := findIntegrationItem(info.Items, "GPU transcoding")
	if row == nil {
		t.Fatal(`"GPU transcoding" row missing`)
	}
	if row.Status != "not configured" {
		t.Errorf("status: got %q, want 'not configured'", row.Status)
	}
}

func TestGetSystemIntegrations_GameDiscsSection(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "apprise",
		RedumperBin:          "sh", // on PATH so status reflects dat coverage, not a binary error
		RedumpDataDir:        t.TempDir(),
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d body %s", w.Code, w.Body.String())
	}
	var info api.IntegrationsInfo
	if err := json.Unmarshal(w.Body.Bytes(), &info); err != nil {
		t.Fatal(err)
	}

	gd := info.GameDiscs
	if gd == nil {
		t.Fatal("no GameDiscs block in integrations response")
	}
	// Game discs is no longer a flat ApiRow item.
	if findIntegrationItem(info.Items, "Game discs") != nil {
		t.Error("Game discs should not be a flat item anymore")
	}
	// Empty dat dir → every Redump dat missing → partial.
	if gd.Status != "partial" {
		t.Errorf("GameDiscs status = %q, want partial (empty dat dir)", gd.Status)
	}
	if len(gd.Systems) != 12 {
		t.Fatalf("GameDiscs systems = %d, want 12", len(gd.Systems))
	}
	byName := map[state.DiscType]api.GameDiscSystem{}
	for _, s := range gd.Systems {
		byName[s.System] = s
		if s.RedumpDat != "missing" {
			t.Errorf("%s redump_dat = %q, want missing", s.System, s.RedumpDat)
		}
	}
	// Xbox and the Tier-1 CD consoles have no boot-code index → "na".
	for _, sys := range []state.DiscType{
		state.DiscTypeXBOX,
		state.DiscTypeSegaCD, state.DiscType3DO, state.DiscTypePCFX,
		state.DiscTypeJaguarCD, state.DiscTypeCDi, state.DiscTypePCECD,
		state.DiscTypeNeoCD,
	} {
		if byName[sys].BootCode != "na" {
			t.Errorf("%s boot_code = %q, want na", sys, byName[sys].BootCode)
		}
	}
	// Each row carries its real Redump folder name (the user-facing fix:
	// "PlayStation" lives in psx/, "Dreamcast" in dc/).
	for sys, want := range map[state.DiscType]string{
		state.DiscTypePSX: "psx", state.DiscTypePS2: "ps2", state.DiscTypeSAT: "saturn",
		state.DiscTypeDC: "dc", state.DiscTypeXBOX: "xbox",
		state.DiscTypeSegaCD: "sega-cd", state.DiscType3DO: "3do", state.DiscTypePCFX: "pc-fx",
		state.DiscTypeJaguarCD: "jaguar-cd", state.DiscTypeCDi: "cdi",
		state.DiscTypePCECD: "pc-engine-cd", state.DiscTypeNeoCD: "neo-geo-cd",
	} {
		if byName[sys].Subdir != want {
			t.Errorf("%s subdir = %q, want %q", sys, byName[sys].Subdir, want)
		}
	}
	if gd.DatDir == "" {
		t.Error("GameDiscs dat_dir should be set")
	}
}

func TestGetSystemIntegrations_BootCodeCounts(t *testing.T) {
	h := apitestServer(t)
	h.Settings = &settings.Settings{
		MusicBrainzBaseURL:   "https://musicbrainz.org",
		MusicBrainzUserAgent: "DiscEcho/test",
		AppriseBin:           "apprise",
	}

	req := httptest.NewRequest(http.MethodGet, "/api/system/integrations", nil)
	w := httptest.NewRecorder()
	h.GetSystemIntegrations(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	var info api.IntegrationsInfo
	_ = json.Unmarshal(w.Body.Bytes(), &info)
	// No BootCodeIndex wired → field omitted (nil map → omitempty).
	if len(info.BootCodeCounts) != 0 {
		t.Errorf("BootCodeCounts = %v, want empty when index is nil", info.BootCodeCounts)
	}
}

// findIntegrationItem returns the matching item by Name, or nil.
func findIntegrationItem(items []api.IntegrationStatus, name string) *api.IntegrationStatus {
	for i, it := range items {
		if it.Name == name {
			return &items[i]
		}
	}
	return nil
}
