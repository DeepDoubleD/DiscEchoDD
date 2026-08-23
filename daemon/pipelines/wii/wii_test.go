package wii_test

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
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/wii"
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
	if mode != "wii" {
		return errors.New("wii: expected wii mode, got " + mode)
	}
	return os.WriteFile(filepath.Join(outDir, name+".iso"), []byte("ISO"), 0o644)
}

func newRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Register(tools.NewMockTool("apprise", nil))
	r.Register(tools.NewMockTool("eject", nil))
	return r
}

// wiiDB builds a minimal RedumpDB with one Wii entry, matching the real
// Redump Wii dat's shape confirmed live: plain title, no bracket boot
// code, MD5-keyed only.
func wiiDB(t *testing.T, title, region, md5sum string) *identify.RedumpDB {
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
	f, err := os.CreateTemp(t.TempDir(), "redump-wii-*.dat")
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
	h := wii.New(wii.Deps{})
	if h.DiscType() != state.DiscTypeWII {
		t.Errorf("DiscType() = %s, want WII", h.DiscType())
	}
}

// TestIdentify_AlwaysNoCandidates covers the package's core design
// fact, confirmed live: a stock read gets nothing off a Wii disc at
// all, not even a TOC, so Identify can never do anything pre-rip.
func TestIdentify_AlwaysNoCandidates(t *testing.T) {
	h := wii.New(wii.Deps{})
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}
	disc, cands, err := h.Identify(context.Background(), drv)
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Errorf("err = %v, want ErrNoCandidates", err)
	}
	if disc == nil || disc.Type != state.DiscTypeWII {
		t.Errorf("disc = %+v, want Type=WII", disc)
	}
	if disc.TOCHash != "" {
		t.Errorf("TOCHash = %q, want empty (nothing readable pre-rip)", disc.TOCHash)
	}
	if cands != nil {
		t.Errorf("candidates = %v, want nil", cands)
	}
}

func TestPlan_StepShape(t *testing.T) {
	plan := wii.New(wii.Deps{}).Plan(&state.Disc{}, &state.Profile{})
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

// TestRun_PostRipMD5Identify is the coverage for the only identification
// path this format has: RunTranscode hashes the ripped ISO and matches
// it against the Redump dat by MD5, filling disc.Title in place so the
// output path template (and disc history / IGDB re-enrichment) has
// something to work with.
func TestRun_PostRipMD5Identify(t *testing.T) {
	libRoot := t.TempDir()
	workRoot := t.TempDir()

	// fakeRedumper always writes the literal bytes "ISO"; its MD5 is
	// precomputed so the dat entry actually matches at lookup time.
	const isoMD5 = "5b512ee8a59deb284ad0a6a035ba10b1"
	db := wiiDB(t, "Battleship", "USA", isoMD5)

	h := wii.New(wii.Deps{
		Redumper:    &fakeRedumper{},
		RedumpDB:    db,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    workRoot,
	})
	prof := &state.Profile{
		ID:                 "p-wii",
		Name:               "WII-ISO",
		OutputPathTemplate: "Wii/{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso",
	}
	// Unidentified going in, exactly like a real classify -> manual
	// override -> start flow leaves it (see discflow.go's fallback).
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeWII}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(libRoot, "Wii", "Battleship (USA)", "Battleship (USA).iso")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file at %s (post-rip MD5 identify should have filled the title): %v", want, err)
	}
	if disc.Title != "Battleship" {
		t.Errorf("disc.Title = %q, want Battleship", disc.Title)
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

// TestRun_PostRipMD5Identify_NoMatch covers the miss case: no Redump
// entry for this MD5, so the disc stays unidentified -- must not error
// or panic, just produce a best-effort path.
func TestRun_PostRipMD5Identify_NoMatch(t *testing.T) {
	libRoot := t.TempDir()
	db := wiiDB(t, "Some Other Game", "USA", "deadbeefdeadbeefdeadbeefdeadbeef")

	h := wii.New(wii.Deps{
		Redumper:    &fakeRedumper{},
		RedumpDB:    db,
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p-wii", Name: "WII-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-2", Type: state.DiscTypeWII}
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
	h := wii.New(wii.Deps{
		Redumper:    &fakeRedumper{err: errors.New("disc unreadable")},
		Tools:       newRegistry(),
		LibraryRoot: t.TempDir(),
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "WII-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-3", Type: state.DiscTypeWII, Title: "X"}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	err := h.Run(context.Background(), drv, disc, prof, sink)
	if err == nil || !strings.Contains(err.Error(), "disc unreadable") {
		t.Errorf("want rip error, got %v", err)
	}
}

// Compile-time guard.
var _ = pipelines.ErrNoCandidates
