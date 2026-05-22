package vcd_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/testutil"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/vcd"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type fakeLabelProber struct {
	label string
	err   error
}

func (f *fakeLabelProber) Probe(_ context.Context, _ string) (string, error) {
	return f.label, f.err
}

// fakeRipper writes the given filenames (as small files) into outDir to
// emulate vcdxrip's avseqNN.mpg output.
type fakeRipper struct {
	files  []string
	err    error
	called bool
}

func (f *fakeRipper) Rip(_ context.Context, _, outDir string, _ tools.Sink) error {
	if f.err != nil {
		return f.err
	}
	f.called = true
	for _, name := range f.files {
		if err := os.WriteFile(filepath.Join(outDir, name), []byte("mpeg"), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func newRegistry() *tools.Registry {
	r := tools.NewRegistry()
	r.Register(tools.NewMockTool("apprise", nil))
	r.Register(tools.NewMockTool("eject", nil))
	return r
}

func fixedNow() time.Time { return time.Date(2024, 3, 15, 10, 30, 45, 0, time.UTC) }

func TestHandler_DiscType(t *testing.T) {
	if vcd.New(vcd.Deps{}).DiscType() != state.DiscTypeVCD {
		t.Fatalf("disc type mismatch")
	}
}

func TestIdentify_LabelPresent(t *testing.T) {
	h := vcd.New(vcd.Deps{LabelProber: &fakeLabelProber{label: "MOVIE_VCD"}})
	disc, cands, err := h.Identify(context.Background(), &state.Drive{ID: "d1", DevPath: "/dev/sr0"})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Fatalf("want ErrNoCandidates, got %v", err)
	}
	if disc.Title != "MOVIE_VCD" {
		t.Errorf("Title = %q, want MOVIE_VCD", disc.Title)
	}
	if len(cands) != 0 {
		t.Errorf("want 0 candidates, got %d", len(cands))
	}
	if disc.TOCHash == "" {
		t.Error("expected pre-rip TOCHash")
	}
}

func TestIdentify_LabelEmpty_FallbackTimestamp(t *testing.T) {
	h := vcd.New(vcd.Deps{LabelProber: &fakeLabelProber{label: ""}, Now: fixedNow})
	disc, _, _ := h.Identify(context.Background(), &state.Drive{ID: "d1", DevPath: "/dev/sr0"})
	if matched, _ := regexp.MatchString(`^vcd-disc-\d{8}-\d{6}$`, disc.Title); !matched {
		t.Errorf("fallback title %q does not match pattern", disc.Title)
	}
	if disc.Title != "vcd-disc-20240315-103045" {
		t.Errorf("Title = %q", disc.Title)
	}
}

func TestPlan_SkipsTranscodeAndCompress(t *testing.T) {
	plan := vcd.New(vcd.Deps{}).Plan(&state.Disc{}, &state.Profile{})
	skipped := map[state.StepID]bool{}
	for _, sp := range plan {
		if sp.Skip {
			skipped[sp.ID] = true
		}
	}
	if !skipped[state.StepTranscode] || !skipped[state.StepCompress] {
		t.Errorf("transcode and compress must be skipped: %v", skipped)
	}
}

func TestRun_HappyPath_MovesAllTracks(t *testing.T) {
	libRoot := t.TempDir()
	rip := &fakeRipper{files: []string{"avseq01.mpg", "avseq02.mpg"}}
	h := vcd.New(vcd.Deps{
		Ripper:      rip,
		LabelProber: &fakeLabelProber{label: "MY_FILM"},
		Tools:       newRegistry(),
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p-vcd", Name: "VCD", OutputPathTemplate: "{{.Title}}"}
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeVCD, Title: "MY_FILM"}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}
	if !rip.called {
		t.Error("ripper not called")
	}
	for _, name := range []string{"avseq01.mpg", "avseq02.mpg"} {
		if _, err := os.Stat(filepath.Join(libRoot, "MY_FILM", name)); err != nil {
			t.Errorf("expected %s in library: %v", name, err)
		}
	}
	if disc.SizeBytesRaw != int64(len("mpeg")*2) {
		t.Errorf("SizeBytesRaw = %d, want %d", disc.SizeBytesRaw, len("mpeg")*2)
	}
}

func TestRun_NoTracksExtracted_Fails(t *testing.T) {
	h := vcd.New(vcd.Deps{
		Ripper:      &fakeRipper{files: nil}, // rip "succeeds" but produces nothing
		LabelProber: &fakeLabelProber{label: "EMPTY"},
		Tools:       newRegistry(),
		LibraryRoot: t.TempDir(),
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "VCD", OutputPathTemplate: "{{.Title}}"}
	disc := &state.Disc{ID: "disc-2", Type: state.DiscTypeVCD, Title: "EMPTY"}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	err := h.Run(context.Background(), drv, disc, prof, testutil.NewRecordingSink())
	if err == nil || !strings.Contains(err.Error(), "no .mpg tracks") {
		t.Errorf("want no-tracks error, got %v", err)
	}
}

func TestRun_RipperFailure(t *testing.T) {
	h := vcd.New(vcd.Deps{
		Ripper:      &fakeRipper{err: errors.New("read error")},
		LabelProber: &fakeLabelProber{label: "X"},
		Tools:       newRegistry(),
		LibraryRoot: t.TempDir(),
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{ID: "p", Name: "VCD", OutputPathTemplate: "{{.Title}}"}
	disc := &state.Disc{ID: "disc-3", Type: state.DiscTypeVCD, Title: "X"}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	err := h.Run(context.Background(), drv, disc, prof, testutil.NewRecordingSink())
	if err == nil || !strings.Contains(err.Error(), "read error") {
		t.Errorf("want rip error, got %v", err)
	}
}
