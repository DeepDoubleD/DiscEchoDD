// Package vcd implements pipelines.Handler for Video CD / Super Video CD.
//
// Pipeline shape (6 active steps; transcode AND compress skipped):
//
//	detect → identify → rip (vcdxrip) → move → notify → eject
//
// VCD/SVCD video lives in CD-ROM XA Mode-2-Form-2 sectors, so the on-disc
// .DAT files can't be copied as plain files — vcdxrip extracts the MPEG-1
// (VCD) or MPEG-2 (SVCD) tracks to avseqNN.mpg. The .mpg streams are the
// library artifact as-is; there is no re-encode (VCD is already a
// low-bitrate constant-format MPEG, so transcoding only loses quality).
//
// Identify reads the ISO9660 volume label via `isoinfo -d` and uses it as
// disc.Title (falling back to "vcd-disc-YYYYMMDD-HHMMSS"), then returns
// ErrNoCandidates: VCD has no automatic metadata source, but the disc is
// a movie/episode so the UI offers a manual TMDB search.
package vcd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Ripper extracts the MPEG tracks from a (S)VCD into outDir. *tools.VCDXRip
// satisfies it; tests inject a fake.
type Ripper interface {
	Rip(ctx context.Context, devPath, outDir string, sink tools.Sink) error
}

// LabelProber reads the ISO9660 volume label from a disc device.
type LabelProber interface {
	Probe(ctx context.Context, devPath string) (string, error)
}

// Deps bundles the handler's dependencies.
type Deps struct {
	Ripper         Ripper
	LabelProber    LabelProber
	Tools          *tools.Registry // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject decides whether the rip-end eject tool runs. nil =
	// always eject (legacy default).
	ShouldEject func(ctx context.Context) bool
	// Now is called for the fallback timestamp title. Defaults to time.Now.
	Now func() time.Time
}

// Handler implements pipelines.Handler for VCD/SVCD discs.
type Handler struct{ deps Deps }

// New returns a Handler with the given dependencies.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return state.DiscTypeVCD }

// Identify reads the volume label as disc.Title (timestamp fallback) and
// always returns ErrNoCandidates. A pre-rip identity hash derived from the
// volume label feeds the persistDisc (drive_id, toc_hash) dedup tier.
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypeVCD, DriveID: drv.ID}

	label := ""
	if h.deps.LabelProber != nil {
		var err error
		label, err = h.deps.LabelProber.Probe(ctx, drv.DevPath)
		if err != nil {
			slog.Warn("vcd: volume label probe failed; using timestamp fallback", "dev", drv.DevPath, "err", err)
		}
	}
	trimmedLabel := strings.TrimSpace(label)
	if trimmedLabel == "" {
		disc.Title = "vcd-disc-" + h.deps.Now().UTC().Format("20060102-150405")
	} else {
		disc.Title = trimmedLabel
	}
	disc.TOCHash = preRipIdentityHash(trimmedLabel)

	return disc, nil, pipelines.ErrNoCandidates
}

// preRipIdentityHash returns a stable identifier for a VCD derived from
// its volume label. The "vcd:v1:" prefix namespaces it from other disc
// types' hashes. Unlabelled discs still hash stably; the discDedupWindow
// time bound makes the tiny cross-disc collision chance harmless.
func preRipIdentityHash(label string) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "vcd:v1:%s", label)
	return hex.EncodeToString(h.Sum(nil))
}

// Plan returns the canonical plan with transcode and compress skipped.
func (h *Handler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	skipped := map[state.StepID]bool{
		state.StepTranscode: true,
		state.StepCompress:  true,
	}
	out := make([]pipelines.StepPlan, 0, len(state.CanonicalSteps()))
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skipped[sid]})
	}
	return out
}

// Run extracts the MPEG tracks via vcdxrip and moves them under
// <library>/<Title>/.
func (h *Handler) Run(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	sink.OnStepStart(state.StepDetect)
	sink.OnStepDone(state.StepDetect, nil)
	sink.OnStepStart(state.StepIdentify)
	sink.OnStepDone(state.StepIdentify, nil)

	// rip — vcdxrip writes avseqNN.mpg into a temporary work directory.
	sink.OnStepStart(state.StepRip)
	tmpdir, err := pipelines.CreateWorkDir(h.deps.WorkRoot, "vcd", disc.ID)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return err
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()

	if err := h.deps.LibraryProbe(h.deps.LibraryRoot); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return fmt.Errorf("library probe: %w", err)
	}

	if h.deps.Ripper == nil {
		err := fmt.Errorf("vcd: ripper not configured")
		sink.OnStepFailed(state.StepRip, err)
		return err
	}
	if err := h.deps.Ripper.Rip(ctx, drv.DevPath, tmpdir, pipelines.NewStepSink(sink, state.StepRip)); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return err
	}

	mpgs, err := mpgFiles(tmpdir)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return err
	}
	if len(mpgs) == 0 {
		err := fmt.Errorf("vcd: no .mpg tracks extracted under %s", tmpdir)
		sink.OnStepFailed(state.StepRip, err)
		return err
	}
	var total int64
	for _, p := range mpgs {
		if fi, statErr := os.Stat(p); statErr == nil {
			total += fi.Size()
		}
	}
	disc.SizeBytesRaw = total
	sink.OnStepDone(state.StepRip, map[string]any{"tracks": len(mpgs)})

	// move — land each track under <library>/<Title>/<avseqNN.mpg>.
	sink.OnStepStart(state.StepMove)
	relDir, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, pipelines.OutputFields{
		Title: disc.Title,
	})
	if err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	var moved []string
	for _, src := range mpgs {
		dst := filepath.Join(h.deps.LibraryRoot, relDir, filepath.Base(src))
		if err := pipelines.AtomicMove(src, dst); err != nil {
			sink.OnStepFailed(state.StepMove, err)
			return err
		}
		moved = append(moved, dst)
	}
	sink.OnStepDone(state.StepMove, map[string]any{"paths": moved})

	pipelines.RunNotifyStep(ctx, sink)

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)
	return nil
}

// mpgFiles returns the sorted list of *.mpg files anywhere under root.
// vcdxrip writes them flat in its working dir, but a recursive walk is
// cheap insurance against a build that nests them.
func mpgFiles(root string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(d.Name()), ".mpg") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walk workdir: %w", err)
	}
	sort.Strings(out)
	return out, nil
}
