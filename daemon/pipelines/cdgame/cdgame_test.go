package cdgame_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
