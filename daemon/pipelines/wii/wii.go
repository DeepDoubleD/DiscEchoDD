// Package wii implements pipelines.Handler for Nintendo Wii game discs.
//
// Pipeline shape (6 active steps; transcode AND compress skipped):
//
//	detect → identify → rip (redumper wii, --dvd-raw) → move → notify → eject
//
// Unlike every other disc type this daemon classifies, a Wii disc gives
// a stock optical drive nothing at all: confirmed live, cd-info fails
// outright with no valid disc-mode line -- not even a TOC, let alone an
// ISO9660 filesystem listing. Nintendo's GC/Wii media deviates from the
// DVD-ROM Book spec a stock drive's firmware expects, unlike Xbox 360's
// XGD discs (which are deliberately also a fully valid, stock-readable
// DVD-Video). So classify.go can never recognise a Wii disc on its own;
// discflow.go instead falls back to a DATA card on a BD/console-role
// drive when classify fails outright, and the user manually overrides
// it to WII via the same dropdown CD32/FM Towns/Pippin already use.
//
// Identify is therefore never able to do anything pre-rip -- it always
// returns ErrNoCandidates immediately, with no TOCHash (nothing is
// readable to derive one from; see the package doc's dedup-fingerprint
// note below). Title comes from RunTranscode's post-rip MD5 lookup
// against the Redump dat, the same pattern pipelines/xbox360 and
// pipelines/cdgame use for formats with no usable pre-rip identifier.
//
// A Wii disc's header (first 1024 bytes) is actually plaintext and
// carries a 6-byte Game ID at offset 0x0 alongside a magic word at
// 0x18 -- Redump's own Wii dat doesn't key by that ID, though (MD5
// only, like Xbox 360's), so this package doesn't bother parsing it;
// MD5 alone is sufficient and matches the established pattern.
package wii

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// RedumperRipper is the subset of *tools.Redumper used at rip-time.
type RedumperRipper interface {
	Rip(ctx context.Context, devPath, outDir, name, mode string, sink tools.Sink) error
}

// Deps bundles the handler's dependencies.
type Deps struct {
	Redumper       RedumperRipper
	RedumpDB       *identify.RedumpDB
	Tools          *tools.Registry // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler for Wii.
type Handler struct{ deps Deps }

// New returns a Handler with the given dependencies.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return state.DiscTypeWII }

// Identify always returns the disc typed as WII with no candidates and
// no TOCHash -- see the package doc. There is nothing pre-rip to probe:
// a stock read gets no TOC and no filesystem listing at all, and the
// raw OmniDrive read redumper needs isn't something this daemon can do
// cheaply outside of an actual rip. RunTranscode's post-rip MD5 lookup
// does the real identification once the ISO exists.
func (h *Handler) Identify(_ context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypeWII, DriveID: drv.ID}
	return disc, nil, pipelines.ErrNoCandidates
}

// Plan returns the 6-active-step plan; both transcode and compress are
// skipped. Used by the monolithic Run fallback.
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

// PlanRip — rip-half: detect, identify, rip, eject. Transcode-half marked Skip.
func (h *Handler) PlanRip(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	transcodeHalf := map[state.StepID]bool{
		state.StepTranscode: true,
		state.StepCompress:  true,
		state.StepMove:      true,
		state.StepNotify:    true,
	}
	out := make([]pipelines.StepPlan, 0, 8)
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: transcodeHalf[sid]})
	}
	return out
}

// PlanTranscode — transcode-half: Wii has no transcode AND no compress
// (the .iso is the deliverable). Only move + notify are active.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		skip := sid == state.StepTranscode || sid == state.StepCompress
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skip})
	}
	return out
}

const spoolName = "rip"

// Run is the monolithic fallback path.
func (h *Handler) Run(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	tmpdir, err := h.createWorkDir(disc.ID)
	if err != nil {
		sink.OnStepStart(state.StepRip)
		sink.OnStepFailed(state.StepRip, err)
		return err
	}
	defer func() { _ = os.RemoveAll(tmpdir) }()
	result, err := h.RunRip(ctx, drv, disc, prof, tmpdir, sink)
	if err != nil {
		return err
	}
	return h.RunTranscode(ctx, result, disc, prof, sink)
}

// RunRip executes the drive-bound half: detect, identify, redumper wii
// mode (--dvd-raw) → spoolDir/rip/rip.iso, eject.
func (h *Handler) RunRip(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, spoolDir string, sink pipelines.EventSink) (pipelines.RipResult, error) {
	sink.OnStepStart(state.StepDetect)
	sink.OnStepDone(state.StepDetect, nil)
	sink.OnStepStart(state.StepIdentify)
	sink.OnStepDone(state.StepIdentify, nil)

	sink.OnStepStart(state.StepRip)
	if err := h.deps.LibraryProbe(h.deps.LibraryRoot); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("library probe: %w", err)
	}
	if h.deps.Redumper == nil {
		err := errors.New("wii: redumper not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir rip: %w", err)
	}
	if err := h.deps.Redumper.Rip(ctx, drv.DevPath, ripDir, spoolName, "wii", pipelines.NewStepSink(sink, state.StepRip)); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("redumper: %w", err)
	}
	isoPath := filepath.Join(ripDir, spoolName+".iso")
	sink.OnStepDone(state.StepRip, map[string]any{"file": isoPath})

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return pipelines.RipResult{SpoolPath: spoolDir}, nil
}

// RunTranscode executes the compute-bound half: post-rip MD5 identify,
// then atomic-move to the library, notify. No compress.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	ripDir := filepath.Join(result.SpoolPath, "rip")
	isoPath := filepath.Join(ripDir, spoolName+".iso")

	h.md5Identify(isoPath, disc)

	sink.OnStepStart(state.StepMove)
	region := ""
	if len(disc.Candidates) > 0 {
		region = disc.Candidates[0].Region
	}
	rel, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, pipelines.OutputFields{
		Title: disc.Title, Year: disc.Year, Region: region,
	})
	if err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	dst := filepath.Join(h.deps.LibraryRoot, rel)
	if err := pipelines.AtomicMove(isoPath, dst); err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	sink.OnStepDone(state.StepMove, map[string]any{"path": dst})

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "wii", discID)
}

// md5Identify hashes the ripped ISO and looks it up in the Redump dat,
// filling disc title/year/region in place on a hit. This is the only
// identification path for Wii -- see the package doc.
func (h *Handler) md5Identify(path string, disc *state.Disc) {
	if h.deps.RedumpDB == nil {
		slog.Warn("wii: no RedumpDB; skipping post-rip identify")
		return
	}
	got, err := pipelines.MD5File(path)
	if err != nil {
		slog.Warn("wii: md5 hash failed", "err", err)
		return
	}
	entry := h.deps.RedumpDB.LookupByMD5(got)
	if entry == nil {
		slog.Warn("wii: no Redump match", "md5", got)
		return
	}
	slog.Info("wii: md5 identify ok", "title", entry.Title, "md5", got)
	disc.Title = entry.Title
	disc.Year = entry.Year
	disc.MetadataProvider = "Redump"
	disc.MetadataID = entry.BootCode
	disc.Candidates = []state.Candidate{{
		Source:     "Redump",
		Title:      entry.Title,
		Year:       entry.Year,
		Region:     entry.Region,
		Confidence: 100,
	}}
}
