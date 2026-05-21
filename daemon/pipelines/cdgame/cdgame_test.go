package cdgame_test

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/cdgame"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/testutil"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// stubIdentifier lets tests drive cdgame.Handler.Identify deterministically.
type stubIdentifier struct {
	disc  *state.Disc
	cands []state.Candidate
	err   error
}

func (s stubIdentifier) Identify(_ context.Context, _ *state.Drive) (*state.Disc, []state.Candidate, error) {
	return s.disc, s.cands, s.err
}

func TestHandler_DiscType(t *testing.T) {
	h := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX})
	if got := h.DiscType(); got != state.DiscTypePSX {
		t.Fatalf("DiscType() = %q, want %q", got, state.DiscTypePSX)
	}
}

func TestHandler_Identify_DelegatesToIdentifier(t *testing.T) {
	want := &state.Disc{Type: state.DiscTypeSAT, Title: "Panzer Dragoon"}
	h := cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypeSAT,
		Identifier: stubIdentifier{disc: want, cands: []state.Candidate{{Title: "Panzer Dragoon"}}},
	})
	disc, cands, err := h.Identify(context.Background(), &state.Drive{})
	if err != nil {
		t.Fatalf("Identify() err = %v", err)
	}
	if disc != want {
		t.Fatalf("Identify() disc = %+v, want %+v", disc, want)
	}
	if len(cands) != 1 || cands[0].Title != "Panzer Dragoon" {
		t.Fatalf("Identify() cands = %+v", cands)
	}
}

func TestHandler_Identify_PropagatesErrNoCandidates(t *testing.T) {
	h := cdgame.New(cdgame.Deps{
		DiscType:   state.DiscTypePS2,
		Identifier: stubIdentifier{disc: &state.Disc{}, err: pipelines.ErrNoCandidates},
	})
	_, _, err := h.Identify(context.Background(), &state.Drive{})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Fatalf("Identify() err = %v, want ErrNoCandidates", err)
	}
}

func TestHandler_Plan_TranscodeSkipped(t *testing.T) {
	plan := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX}).Plan(&state.Disc{}, &state.Profile{})
	if len(plan) != len(state.CanonicalSteps()) {
		t.Fatalf("Plan len = %d, want %d", len(plan), len(state.CanonicalSteps()))
	}
	var sawTranscodeSkipped bool
	for _, p := range plan {
		if p.ID == state.StepTranscode {
			sawTranscodeSkipped = p.Skip
		}
	}
	if !sawTranscodeSkipped {
		t.Fatalf("expected transcode step to be skipped")
	}
}

func TestHandler_PlanRip_TranscodeHalfSkipped(t *testing.T) {
	plan := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX}).PlanRip(&state.Disc{}, &state.Profile{})
	skip := map[state.StepID]bool{}
	for _, p := range plan {
		skip[p.ID] = p.Skip
	}
	for _, sid := range []state.StepID{state.StepTranscode, state.StepCompress, state.StepMove, state.StepNotify} {
		if !skip[sid] {
			t.Fatalf("PlanRip: expected %s skipped in rip-half", sid)
		}
	}
	if skip[state.StepRip] {
		t.Fatalf("PlanRip: rip step must not be skipped")
	}
}

func TestHandler_PlanTranscode_TranscodeStepSkipped(t *testing.T) {
	plan := cdgame.New(cdgame.Deps{DiscType: state.DiscTypePSX}).PlanTranscode(&state.Disc{}, &state.Profile{})
	if len(plan) != len(state.CanonicalTranscodeSteps()) {
		t.Fatalf("PlanTranscode len = %d, want %d", len(plan), len(state.CanonicalTranscodeSteps()))
	}
	for _, p := range plan {
		if p.ID == state.StepTranscode && !p.Skip {
			t.Fatalf("PlanTranscode: transcode step must be skipped")
		}
	}
}

// fakeRedumper writes a stub rip.bin + rip.cue into outDir so the
// downstream MD5 / chdman / move steps have files to operate on.
type fakeRedumper struct{ err error }

func (f *fakeRedumper) Rip(_ context.Context, _ string, outDir, name, _ string, _ tools.Sink) error {
	if f.err != nil {
		return f.err
	}
	if err := os.WriteFile(filepath.Join(outDir, name+".bin"), []byte("BINDATA"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(outDir, name+".cue"), []byte("CUE"), 0o644)
}

// fakeCHDMan writes a stub .chd at output so the move step has a file.
type fakeCHDMan struct{ err error }

func (f *fakeCHDMan) CreateCHD(_ context.Context, _ string, output string, _ tools.Sink) error {
	if f.err != nil {
		return f.err
	}
	return os.WriteFile(output, []byte("CHD"), 0o644)
}

func TestHandler_Run_HappyPath(t *testing.T) {
	lib := t.TempDir()
	work := t.TempDir()
	h := cdgame.New(cdgame.Deps{
		DiscType:    state.DiscTypePSX,
		WorkPrefix:  "psx",
		Identifier:  stubIdentifier{disc: &state.Disc{}},
		Redumper:    &fakeRedumper{},
		CHDMan:      &fakeCHDMan{},
		LibraryRoot: lib,
		WorkRoot:    work,
	})
	disc := &state.Disc{ID: "disc-1", Title: "Some Game", Year: 1999}
	prof := &state.Profile{OutputPathTemplate: "{{.Title}} ({{.Year}})/{{.Title}}.chd"}
	sink := testutil.NewRecordingSink()

	if err := h.Run(context.Background(), &state.Drive{DevPath: "/dev/sr0"}, disc, prof, sink); err != nil {
		t.Fatalf("Run() err = %v", err)
	}
	dst := filepath.Join(lib, "Some Game (1999)", "Some Game.chd")
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("expected output at %s: %v", dst, err)
	}

	// Step ordering: detect → identify → rip → eject (RunRip),
	// then compress → move → notify (RunTranscode).
	starts := sink.StepSequence()
	wantOrder := []state.StepID{
		state.StepDetect, state.StepIdentify, state.StepRip, state.StepEject,
		state.StepCompress, state.StepMove, state.StepNotify,
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
		if st == state.StepTranscode {
			t.Errorf("transcode should not have started for cdgame")
		}
	}
}

func TestHandler_RunRip_RipFailure(t *testing.T) {
	h := cdgame.New(cdgame.Deps{
		DiscType:    state.DiscTypePSX,
		WorkPrefix:  "psx",
		Identifier:  stubIdentifier{disc: &state.Disc{}},
		Redumper:    &fakeRedumper{err: errors.New("disc unreadable")},
		CHDMan:      &fakeCHDMan{},
		LibraryRoot: t.TempDir(),
		WorkRoot:    t.TempDir(),
	})
	sink := testutil.NewRecordingSink()
	err := h.Run(context.Background(), &state.Drive{DevPath: "/dev/sr0"}, &state.Disc{ID: "d"}, &state.Profile{OutputPathTemplate: "{{.Title}}.chd"}, sink)
	if err == nil || !strings.Contains(err.Error(), "disc unreadable") {
		t.Errorf("want rip error containing \"disc unreadable\", got %v", err)
	}
}

// makeRedumpDB writes a minimal Redump dat file containing one entry keyed by
// the MD5 of binData and loads it into a *identify.RedumpDB. Mirrors the
// dcDB helper in dreamcast_test.go.
func makeRedumpDB(t *testing.T, bootCode, title, region string, year int, binData []byte) *identify.RedumpDB {
	t.Helper()
	sum := md5.Sum(binData)
	md5hex := hex.EncodeToString(sum[:])

	gameName := fmt.Sprintf("%s (%s) (%d)", title, region, year)
	romName := fmt.Sprintf("%s (%s) (%d) [%s].bin", title, region, year, bootCode)
	xml := `<?xml version="1.0"?>` + "\n" +
		`<datafile>` + "\n" +
		`  <game name="` + gameName + `">` + "\n" +
		`    <description>` + gameName + `</description>` + "\n" +
		`    <rom name="` + romName + `" md5="` + md5hex + `"/>` + "\n" +
		`  </game>` + "\n" +
		`</datafile>` + "\n"
	f, err := os.CreateTemp(t.TempDir(), "redump-*.dat")
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

// TestHandler_RunTranscode_MD5Identify verifies that when PostRipIdentify is
// true the compress step hashes the ripped .bin, hits the Redump dat, and
// fills disc.Title/Year/MetadataProvider/MetadataID/Candidates in place.
func TestHandler_RunTranscode_MD5Identify(t *testing.T) {
	// fakeRedumper writes "BINDATA" to rip.bin; MD5 = 9258e2e3e92d88a2c680770b886d1163
	binData := []byte("BINDATA")
	db := makeRedumpDB(t, "T-123456N", "Sonic CD", "USA", 1993, binData)

	lib := t.TempDir()
	work := t.TempDir()
	h := cdgame.New(cdgame.Deps{
		DiscType:        state.DiscTypePSX, // type is arbitrary for this test
		WorkPrefix:      "cdgame",
		PostRipIdentify: true,
		Identifier:      stubIdentifier{disc: &state.Disc{}},
		Redumper:        &fakeRedumper{},
		CHDMan:          &fakeCHDMan{},
		RedumpDB:        db,
		LibraryRoot:     lib,
		WorkRoot:        work,
	})

	disc := &state.Disc{ID: "disc-segacd-1", Title: "Unknown Disc", Year: 0}
	prof := &state.Profile{OutputPathTemplate: "{{.Title}} ({{.Year}})/{{.Title}}.chd"}
	sink := testutil.NewRecordingSink()

	if err := h.Run(context.Background(), &state.Drive{DevPath: "/dev/sr0"}, disc, prof, sink); err != nil {
		t.Fatalf("Run() err = %v", err)
	}

	if disc.Title != "Sonic CD" {
		t.Errorf("disc.Title = %q, want %q", disc.Title, "Sonic CD")
	}
	if disc.Year != 1993 {
		t.Errorf("disc.Year = %d, want 1993", disc.Year)
	}
	if disc.MetadataProvider != "Redump" {
		t.Errorf("disc.MetadataProvider = %q, want Redump", disc.MetadataProvider)
	}
	if disc.MetadataID != "T-123456N" {
		t.Errorf("disc.MetadataID = %q, want T-123456N", disc.MetadataID)
	}
	if len(disc.Candidates) == 0 || disc.Candidates[0].Source != "Redump" {
		t.Errorf("disc.Candidates = %+v, want one Redump candidate", disc.Candidates)
	}
	if len(disc.Candidates) > 0 && disc.Candidates[0].Region != "USA" {
		t.Errorf("disc.Candidates[0].Region = %q, want USA", disc.Candidates[0].Region)
	}

	// Output file should be at the path rendered with the identified title/year.
	dst := filepath.Join(lib, "Sonic CD (1993)", "Sonic CD.chd")
	if _, err := os.Stat(dst); err != nil {
		t.Errorf("expected output at %s: %v", dst, err)
	}
}

// TestNoBootIdentifier_ReturnsErrNoCandidates verifies the pre-rip identifier
// for boot-code-less CD consoles: returns a typed disc, no candidates, and
// ErrNoCandidates.
func TestNoBootIdentifier_ReturnsErrNoCandidates(t *testing.T) {
	id := cdgame.NoBootIdentifier{DiscType: state.DiscTypePSX}
	disc, cands, err := id.Identify(context.Background(), &state.Drive{ID: "x"})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Fatalf("err = %v, want ErrNoCandidates", err)
	}
	if disc == nil {
		t.Fatal("disc is nil")
	}
	if disc.Type != state.DiscTypePSX {
		t.Errorf("disc.Type = %q, want PSX", disc.Type)
	}
	if disc.DriveID != "x" {
		t.Errorf("disc.DriveID = %q, want x", disc.DriveID)
	}
	if len(cands) != 0 {
		t.Errorf("cands = %+v, want nil", cands)
	}
}
