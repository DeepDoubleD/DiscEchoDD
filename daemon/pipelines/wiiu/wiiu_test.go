package wiiu_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/testutil"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/wiiu"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type fakeRedumper struct {
	err error
}

func (f *fakeRedumper) Rip(_ context.Context, _ string, outDir, name, mode string, _ tools.Sink) error {
	if f.err != nil {
		return f.err
	}
	if mode != "wiiu" {
		return errors.New("wiiu: expected wiiu mode, got " + mode)
	}
	return os.WriteFile(filepath.Join(outDir, name+".iso"), []byte("ISO"), 0o644)
}

// fakeWuDecrypt writes a marker file into outDir on success, letting
// tests assert the decrypted-folder-tree deliverable without needing a
// real wudecrypt binary.
type fakeWuDecrypt struct {
	err    error
	called bool
}

func (f *fakeWuDecrypt) Decrypt(_ context.Context, _, outDir, _, _ string, _ tools.Sink) error {
	f.called = true
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(filepath.Join(outDir, "meta.xml"), []byte("<meta/>"), 0o644)
}

func newRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Register(tools.NewMockTool("apprise", nil))
	r.Register(tools.NewMockTool("eject", nil))
	return r
}

// wiiuDB builds a minimal RedumpDB with one Wii U entry, mirroring the
// Wii dat's shape (plain title, MD5-keyed only).
func wiiuDB(t *testing.T, title, region, md5sum string) *identify.RedumpDB {
	t.Helper()
	gameName := title + " (" + region + ")"
	romName := gameName + ".iso"
	xml := `<?xml version="1.0"?>` + "\n" +
		`<datafile>` + "\n" +
		`  <game name="` + gameName + `">` + "\n" +
		`    <description>` + gameName + `</description>` + "\n" +
		`    <rom name="` + romName + `" md5="` + md5sum + `"/>` + "\n" +
		`  </game>` + "\n" +
		`</datafile>` + "\n"
	f, err := os.CreateTemp(t.TempDir(), "redump-wiiu-*.dat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(xml); err != nil {
		t.Fatal(err)
	}
	name := f.Name()
	_ = f.Close()
	db, err := identify.LoadRedumpDB(name)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

func TestHandler_DiscType(t *testing.T) {
	h := wiiu.New(wiiu.Deps{})
	if h.DiscType() != state.DiscTypeWIIU {
		t.Errorf("DiscType() = %s, want WIIU", h.DiscType())
	}
}

// TestIdentify_AlwaysNoCandidates covers the package's core design
// fact: a stock read gets nothing usable off a Wii U disc pre-rip
// (still AES-encrypted), so Identify can never do anything pre-rip.
func TestIdentify_AlwaysNoCandidates(t *testing.T) {
	h := wiiu.New(wiiu.Deps{})
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}
	disc, cands, err := h.Identify(context.Background(), drv)
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Errorf("err = %v, want ErrNoCandidates", err)
	}
	if disc == nil || disc.Type != state.DiscTypeWIIU {
		t.Errorf("disc = %+v, want Type=WIIU", disc)
	}
	if disc.TOCHash != "" {
		t.Errorf("TOCHash = %q, want empty (nothing readable pre-rip)", disc.TOCHash)
	}
	if cands != nil {
		t.Errorf("candidates = %v, want nil", cands)
	}
}

func TestPlan_StepShape(t *testing.T) {
	plan := wiiu.New(wiiu.Deps{}).Plan(&state.Disc{}, &state.Profile{})
	if len(plan) != 8 {
		t.Fatalf("want 8 entries, got %d", len(plan))
	}
	skipped := map[state.StepID]bool{}
	for _, sp := range plan {
		if sp.Skip {
			skipped[sp.ID] = true
		}
	}
	if !skipped[state.StepTranscode] {
		t.Errorf("transcode should be skipped")
	}
	if !skipped[state.StepCompress] {
		t.Errorf("compress should be skipped")
	}
}

// TestRun_PostRipMD5Identify is the coverage for the only
// identification path this format has: RunTranscode hashes the
// ripped (still-encrypted) dump and matches it against the Redump dat
// by MD5, filling disc.Title in place.
func TestRun_PostRipMD5Identify(t *testing.T) {
	libRoot := t.TempDir()
	workRoot := t.TempDir()

	// fakeRedumper always writes the literal bytes "ISO"; its MD5 is
	// precomputed so the dat entry actually matches at lookup time.
	const isoMD5 = "5b512ee8a59deb284ad0a6a035ba10b1"
	db := wiiuDB(t, "Super Mario 3D World", "USA", isoMD5)

	h := wiiu.New(wiiu.Deps{
		Redumper:    &fakeRedumper{},
		RedumpDB:    db,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    workRoot,
	})
	prof := &state.Profile{
		ID:                 "p-wiiu",
		Name:               "WIIU-ISO",
		OutputPathTemplate: "Wii U/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso",
	}
	// Unidentified going in, exactly like a real classify -> manual
	// override -> start flow leaves it (see discflow.go's fallback).
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeWIIU}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(libRoot, "Wii U", "Super Mario 3D World (USA)", "Super Mario 3D World (USA).iso")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file at %s (post-rip MD5 identify should have filled the title): %v", want, err)
	}
	if disc.Title != "Super Mario 3D World" {
		t.Errorf("disc.Title = %q, want Super Mario 3D World", disc.Title)
	}

	starts := sink.StepSequence()
	wantOrder := []state.StepID{
		state.StepDetect, state.StepIdentify, state.StepRip, state.StepEject,
		state.StepMove, state.StepNotify,
	}
	if len(starts) != len(wantOrder) {
		t.Fatalf("started %d steps, want %d: %v", len(starts), len(wantOrder), starts)
	}
	for i := range wantOrder {
		if starts[i] != wantOrder[i] {
			t.Errorf("step %d = %s, want %s", i, starts[i], wantOrder[i])
		}
	}
}

// TestRun_Decrypt_WithBothKeys_MovesDecryptedFolder covers the
// happy path: when the user has dropped both common.key and a
// <md5>.key matching the raw dump's own MD5 into KeysDir, RunTranscode
// ships the decrypted folder tree (not the raw .iso) -- mirroring
// PS3's decrypted-only deliverable.
func TestRun_Decrypt_WithBothKeys_MovesDecryptedFolder(t *testing.T) {
	libRoot := t.TempDir()
	keysDir := t.TempDir()

	const isoMD5 = "5b512ee8a59deb284ad0a6a035ba10b1"
	db := wiiuDB(t, "Super Mario 3D World", "USA", isoMD5)
	if err := os.WriteFile(filepath.Join(keysDir, "common.key"), []byte("COMMONKEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, isoMD5+".key"), []byte("DISCKEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	wd := &fakeWuDecrypt{}
	h := wiiu.New(wiiu.Deps{
		Redumper:    &fakeRedumper{},
		RedumpDB:    db,
		WuDecrypt:   wd,
		KeysDir:     keysDir,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{
		ID:                 "p-wiiu",
		Name:               "WIIU-ISO",
		OutputPathTemplate: "Wii U/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso",
	}
	disc := &state.Disc{ID: "disc-4", Type: state.DiscTypeWIIU}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}
	if !wd.called {
		t.Fatal("want WuDecrypt.Decrypt to be called when both keys are present")
	}

	wantDir := filepath.Join(libRoot, "Wii U", "Super Mario 3D World (USA)", "Super Mario 3D World (USA)")
	if _, err := os.Stat(filepath.Join(wantDir, "meta.xml")); err != nil {
		t.Errorf("expected decrypted folder marker at %s: %v", wantDir, err)
	}
	wantISO := wantDir + ".iso"
	if _, err := os.Stat(wantISO); err == nil {
		t.Errorf("raw .iso should not be moved to the library when decrypt succeeds: %s exists", wantISO)
	}
}

// TestRun_Decrypt_MissingDiscKey_FallsBackToRaw covers the common
// real-world case: the user has a common.key but hasn't sourced this
// particular disc's key yet. Must ship the raw dump, not fail the job.
func TestRun_Decrypt_MissingDiscKey_FallsBackToRaw(t *testing.T) {
	libRoot := t.TempDir()
	keysDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(keysDir, "common.key"), []byte("COMMONKEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	wd := &fakeWuDecrypt{}
	h := wiiu.New(wiiu.Deps{
		Redumper:    &fakeRedumper{},
		WuDecrypt:   wd,
		KeysDir:     keysDir,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "WIIU-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-5", Type: state.DiscTypeWIIU}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}
	if wd.called {
		t.Error("want WuDecrypt.Decrypt NOT called without a matching per-disc key")
	}
	if _, err := os.Stat(filepath.Join(libRoot, ".iso")); err != nil {
		t.Errorf("want raw .iso shipped as fallback: %v", err)
	}
}

// TestRun_Decrypt_ToolFailure_FallsBackToRaw covers a wudecrypt
// failure (pre-alpha third-party tool, bad key, corrupt dump, etc.):
// must not fail the whole job, just fall back to the raw dump.
func TestRun_Decrypt_ToolFailure_FallsBackToRaw(t *testing.T) {
	libRoot := t.TempDir()
	keysDir := t.TempDir()
	const isoMD5 = "5b512ee8a59deb284ad0a6a035ba10b1"
	if err := os.WriteFile(filepath.Join(keysDir, "common.key"), []byte("COMMONKEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(keysDir, isoMD5+".key"), []byte("DISCKEY"), 0o600); err != nil {
		t.Fatal(err)
	}

	wd := &fakeWuDecrypt{err: errors.New("bad common key")}
	h := wiiu.New(wiiu.Deps{
		Redumper:    &fakeRedumper{},
		WuDecrypt:   wd,
		KeysDir:     keysDir,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "WIIU-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-6", Type: state.DiscTypeWIIU}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}
	if !wd.called {
		t.Fatal("want WuDecrypt.Decrypt to be attempted")
	}
	if _, err := os.Stat(filepath.Join(libRoot, ".iso")); err != nil {
		t.Errorf("want raw .iso shipped after decrypt failure: %v", err)
	}
}

// TestRun_PostRipMD5Identify_NoMatch covers the miss case: no Redump
// entry for this MD5, so the disc stays unidentified -- must not
// error or panic, just produce a best-effort path.
func TestRun_PostRipMD5Identify_NoMatch(t *testing.T) {
	libRoot := t.TempDir()
	db := wiiuDB(t, "Some Other Game", "USA", "deadbeefdeadbeefdeadbeefdeadbeef")

	h := wiiu.New(wiiu.Deps{
		Redumper:    &fakeRedumper{},
		RedumpDB:    db,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p-wiiu", Name: "WIIU-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-2", Type: state.DiscTypeWIIU}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}
	if disc.Title != "" {
		t.Errorf("disc.Title = %q, want unchanged empty (no MD5 match)", disc.Title)
	}
}

func TestRun_RipFailure(t *testing.T) {
	h := wiiu.New(wiiu.Deps{
		Redumper:    &fakeRedumper{err: errors.New("disc unreadable")},
		Tools:       newRegistry(),
		LibraryRoot: t.TempDir(),
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "WIIU-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-3", Type: state.DiscTypeWIIU, Title: "X"}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	err := h.Run(context.Background(), drv, disc, prof, sink)
	if err == nil || !strings.Contains(err.Error(), "disc unreadable") {
		t.Errorf("want rip error, got %v", err)
	}
}

// Compile-time guard.
var _ = pipelines.ErrNoCandidates
