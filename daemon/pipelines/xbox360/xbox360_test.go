package xbox360_test

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
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/xbox360"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// fakeXbox360Prober stubs xbox360.Xbox360Prober.
type fakeXbox360Prober struct {
	info *identify.Xbox360Info
	err  error
}

func (f *fakeXbox360Prober) Probe(_ context.Context, _ string) (*identify.Xbox360Info, error) {
	return f.info, f.err
}

type fakeRedumper struct {
	err error
}

func (f *fakeRedumper) Rip(_ context.Context, _ string, outDir, name, mode string, _ tools.Sink) error {
	if f.err != nil {
		return f.err
	}
	if mode != "xbox360" {
		return errors.New("xbox360: expected xbox360 mode, got " + mode)
	}
	return os.WriteFile(filepath.Join(outDir, name+".iso"), []byte("ISO"), 0o644)
}

func newRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Register(tools.NewMockTool("apprise", nil))
	r.Register(tools.NewMockTool("eject", nil))
	return r
}

// xbox360DB builds a minimal RedumpDB with one Xbox 360 entry. The ROM
// name embeds the 8-digit hex title ID in bracket notation, same
// convention as original Xbox's Redump dats.
func xbox360DB(t *testing.T, titleID uint32, title, region, md5sum string) *identify.RedumpDB {
	t.Helper()
	gameName := title + " (" + region + ")"
	romName := title + " (" + region + ") [" + toHex8(titleID) + "].iso"
	xml := `<?xml version="1.0"?>` + "\n" +
		`<datafile>` + "\n" +
		`  <game name="` + gameName + `">` + "\n" +
		`    <description>` + gameName + `</description>` + "\n" +
		`    <rom name="` + romName + `" md5="` + md5sum + `"/>` + "\n" +
		`  </game>` + "\n" +
		`</datafile>` + "\n"
	f, err := os.CreateTemp(t.TempDir(), "redump-xbox360-*.dat")
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

func toHex8(v uint32) string {
	const hex = "0123456789ABCDEF"
	buf := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		buf[i] = hex[v&0xF]
		v >>= 4
	}
	return string(buf)
}

func TestHandler_DiscType(t *testing.T) {
	h := xbox360.New(xbox360.Deps{})
	if h.DiscType() != state.DiscTypeXBOX360 {
		t.Fatalf("disc type: %q", h.DiscType())
	}
}

func TestIdentify_RedumpHit(t *testing.T) {
	db := xbox360DB(t, 0x4D5307E6, "Halo 3", "USA", "abc")
	h := xbox360.New(xbox360.Deps{
		Xbox360Prober: &fakeXbox360Prober{info: &identify.Xbox360Info{TitleID: 0x4D5307E6}},
		RedumpDB:      db,
	})
	disc, cands, err := h.Identify(context.Background(), &state.Drive{ID: "d1", DevPath: "/dev/sr0"})
	if err != nil {
		t.Fatal(err)
	}
	if disc.Title != "Halo 3" {
		t.Errorf("Title = %q, want %q", disc.Title, "Halo 3")
	}
	if disc.MetadataProvider != "Redump" {
		t.Errorf("MetadataProvider = %q, want Redump", disc.MetadataProvider)
	}
	if disc.MetadataID != "4D5307E6" {
		t.Errorf("MetadataID = %q, want 4D5307E6", disc.MetadataID)
	}
	if disc.Type != state.DiscTypeXBOX360 {
		t.Errorf("disc.Type = %s, want XBOX360", disc.Type)
	}
	if len(cands) != 1 {
		t.Fatalf("want 1 candidate, got %d", len(cands))
	}
	if cands[0].Region != "USA" {
		t.Errorf("Region = %q, want USA", cands[0].Region)
	}
}

func TestIdentify_NoRedumpDB(t *testing.T) {
	h := xbox360.New(xbox360.Deps{
		Xbox360Prober: &fakeXbox360Prober{info: &identify.Xbox360Info{TitleID: 0x4D5307E6}},
		RedumpDB:      nil,
	})
	_, _, err := h.Identify(context.Background(), &state.Drive{ID: "d1"})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Errorf("want ErrNoCandidates, got %v", err)
	}
}

func TestIdentify_RedumpMiss(t *testing.T) {
	db := xbox360DB(t, 0x4D5307E6, "Halo 3", "USA", "abc")
	h := xbox360.New(xbox360.Deps{
		Xbox360Prober: &fakeXbox360Prober{info: &identify.Xbox360Info{TitleID: 0xDEADBEEF}},
		RedumpDB:      db,
	})
	_, _, err := h.Identify(context.Background(), &state.Drive{ID: "d1"})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Errorf("want ErrNoCandidates, got %v", err)
	}
}

func TestIdentify_ProbeError(t *testing.T) {
	h := xbox360.New(xbox360.Deps{
		Xbox360Prober: &fakeXbox360Prober{err: identify.ErrNotXbox360},
	})
	_, _, err := h.Identify(context.Background(), &state.Drive{ID: "d1"})
	if err == nil {
		t.Fatal("want error when the probe fails")
	}
}

func TestPlan_StepShape(t *testing.T) {
	plan := xbox360.New(xbox360.Deps{}).Plan(&state.Disc{}, &state.Profile{})
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
		t.Errorf("compress should be skipped (no chdman for Xbox 360)")
	}
}

func TestRun_HappyPath(t *testing.T) {
	libRoot := t.TempDir()
	workRoot := t.TempDir()

	h := xbox360.New(xbox360.Deps{
		Redumper:    &fakeRedumper{},
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    workRoot,
	})
	prof := &state.Profile{
		ID:                 "p-xbox360",
		Name:               "Xbox360-ISO",
		OutputPathTemplate: "{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).iso",
	}
	disc := &state.Disc{
		ID:    "disc-1",
		Type:  state.DiscTypeXBOX360,
		Title: "Halo 3",
		Year:  2007,
	}
	disc.Candidates = []state.Candidate{{Source: "Redump", Title: "Halo 3", Region: "USA", Confidence: 100}}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}

	// Output is an ISO, not CHD or BIN/CUE. fakeRedumper.Rip already
	// asserts mode=="xbox360" (which is what triggers --dvd-raw in the
	// real tools.Redumper), so a successful write here proves the
	// pipeline requested the right mode.
	want := filepath.Join(libRoot, "Halo 3 (USA)", "Halo 3 (USA).iso")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file at %s: %v", want, err)
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
	for _, st := range starts {
		if st == state.StepTranscode || st == state.StepCompress {
			t.Errorf("step %s should not have started for Xbox 360", st)
		}
	}
}

func TestRun_RipFailure(t *testing.T) {
	h := xbox360.New(xbox360.Deps{
		Redumper:    &fakeRedumper{err: errors.New("disc unreadable")},
		Tools:       newRegistry(),
		LibraryRoot: t.TempDir(),
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "Xbox360-ISO", OutputPathTemplate: "{{.Title}}.iso"}
	disc := &state.Disc{ID: "disc-2", Type: state.DiscTypeXBOX360, Title: "X"}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	err := h.Run(context.Background(), drv, disc, prof, sink)
	if err == nil || !strings.Contains(err.Error(), "disc unreadable") {
		t.Errorf("want rip error, got %v", err)
	}
}

// Compile-time guards.
var _ = pipelines.ErrNoCandidates
var _ pipelines.SplittableHandler = (*xbox360.Handler)(nil)
