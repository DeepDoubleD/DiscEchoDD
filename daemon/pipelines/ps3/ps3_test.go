package ps3_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/ps3"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/testutil"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Note: RunRip/Run aren't covered here -- unlike every other pipeline's
// RedumperRipper-style interface, PS3's mount/unmount lifecycle
// (mountReadOnly) shells out to the real mount/umount binaries rather
// than going through an injectable dependency, so exercising it needs
// an actual block device. This is deliberate scope for tonight: the
// user has no PS3 disc to validate against yet ("build the bones,
// we'll test asap"). What IS unit-testable without real hardware --
// DiscType, the step-plan shapes, Identify's graceful nil-Dumper
// degradation, and RunTranscode's directory-move logic -- is covered
// below.

type fakeDumper struct {
	detectResult *tools.PS3DetectResult
	detectErr    error
}

func (f *fakeDumper) Detect(_ context.Context, _ string, _ tools.Sink) (*tools.PS3DetectResult, error) {
	return f.detectResult, f.detectErr
}
func (f *fakeDumper) Dump(_ context.Context, _, _, _ string, _ tools.Sink) (*tools.PS3DumpResult, error) {
	return nil, errors.New("not used in these tests")
}

func TestHandler_DiscType(t *testing.T) {
	h := ps3.New(ps3.Deps{})
	if h.DiscType() != state.DiscTypePS3 {
		t.Errorf("DiscType() = %s, want PS3", h.DiscType())
	}
}

// TestIdentify_NoDumperConfigured covers the graceful-degradation path
// that doesn't touch mount/umount at all (checked before mounting).
func TestIdentify_NoDumperConfigured(t *testing.T) {
	h := ps3.New(ps3.Deps{})
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}
	disc, cands, err := h.Identify(context.Background(), drv)
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Errorf("err = %v, want ErrNoCandidates", err)
	}
	if disc == nil || disc.Type != state.DiscTypePS3 {
		t.Errorf("disc = %+v, want Type=PS3", disc)
	}
	if cands != nil {
		t.Errorf("candidates = %v, want nil", cands)
	}
}

func TestPlan_StepShape(t *testing.T) {
	plan := ps3.New(ps3.Deps{}).Plan(&state.Disc{}, &state.Profile{})
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

func TestPlanRip_TranscodeHalfSkipped(t *testing.T) {
	plan := ps3.New(ps3.Deps{}).PlanRip(&state.Disc{}, &state.Profile{})
	skipped := map[state.StepID]bool{}
	for _, sp := range plan {
		if sp.Skip {
			skipped[sp.ID] = true
		}
	}
	for _, id := range []state.StepID{state.StepTranscode, state.StepCompress, state.StepMove, state.StepNotify} {
		if !skipped[id] {
			t.Errorf("PlanRip: step %s should be skipped", id)
		}
	}
	for _, id := range []state.StepID{state.StepDetect, state.StepIdentify, state.StepRip, state.StepEject} {
		if skipped[id] {
			t.Errorf("PlanRip: step %s should NOT be skipped", id)
		}
	}
}

// TestRunTranscode_MovesDecryptedFolder covers the one thing genuinely
// different about PS3 versus every other pipeline: the rip output is
// a decrypted DIRECTORY tree (PS3_GAME/, PS3_UPDATE/, ...), not a
// single .iso/.chd file, since ps3dumper-cli copies+decrypts files
// individually rather than producing a monolithic disc image.
func TestRunTranscode_MovesDecryptedFolder(t *testing.T) {
	libRoot := t.TempDir()
	spoolRoot := t.TempDir()

	// Simulate what RunRip's ps3dumper-cli dump would have left behind:
	// spoolRoot/rip/<ProductCode>/PS3_GAME/USRDIR/EBOOT.BIN
	ripDir := filepath.Join(spoolRoot, "rip")
	discRoot := filepath.Join(ripDir, "BLUS30109")
	if err := os.MkdirAll(filepath.Join(discRoot, "PS3_GAME", "USRDIR"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(discRoot, "PS3_GAME", "USRDIR", "EBOOT.BIN"), []byte("fake"), 0o644); err != nil {
		t.Fatal(err)
	}

	h := ps3.New(ps3.Deps{
		Dumper:      &fakeDumper{},
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
	})
	prof := &state.Profile{ID: "p-ps3", Name: "PS3-DECRYPTED", OutputPathTemplate: "PS3/{{.Title}}"}
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypePS3, Title: "Kingdom Hearts"}

	sink := testutil.NewRecordingSink()
	if err := h.RunTranscode(context.Background(), pipelines.RipResult{SpoolPath: spoolRoot}, disc, prof, sink); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(libRoot, "PS3", "Kingdom Hearts", "PS3_GAME", "USRDIR", "EBOOT.BIN")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file at %s: %v", want, err)
	}
	if _, err := os.Stat(discRoot); !os.IsNotExist(err) {
		t.Errorf("source dir %s should have been moved, not copied", discRoot)
	}
}

// TestRunTranscode_MultipleTopLevelDirs_Fails guards against silently
// moving the wrong thing (or nothing) if ps3dumper-cli's output naming
// ever changes shape.
func TestRunTranscode_MultipleTopLevelDirs_Fails(t *testing.T) {
	spoolRoot := t.TempDir()
	ripDir := filepath.Join(spoolRoot, "rip")
	if err := os.MkdirAll(filepath.Join(ripDir, "a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(ripDir, "b"), 0o755); err != nil {
		t.Fatal(err)
	}

	h := ps3.New(ps3.Deps{Dumper: &fakeDumper{}, Tools: newRegistry(), LibraryRoot: t.TempDir()})
	prof := &state.Profile{ID: "p", Name: "PS3-DECRYPTED", OutputPathTemplate: "PS3/{{.Title}}"}
	disc := &state.Disc{ID: "disc-2", Type: state.DiscTypePS3, Title: "X"}

	sink := testutil.NewRecordingSink()
	if err := h.RunTranscode(context.Background(), pipelines.RipResult{SpoolPath: spoolRoot}, disc, prof, sink); err == nil {
		t.Error("want error when rip dir doesn't contain exactly one output dir")
	}
}

func newRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Register(tools.NewMockTool("apprise", nil))
	r.Register(tools.NewMockTool("eject", nil))
	return r
}
