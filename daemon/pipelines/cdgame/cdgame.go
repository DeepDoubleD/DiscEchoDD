// Package cdgame implements pipelines.Handler + SplittableHandler for the
// family of disc-based game consoles that rip via redumper (CD → .bin/.cue,
// DVD → .iso) and compress to .chd via chdman: PlayStation 1, PlayStation 2,
// Sega Saturn, and (in a later milestone) other CD/DVD consoles. The only
// per-system difference is identification, injected as an Identifier.
//
// Pipeline shape (7 active steps; transcode skipped):
//
//	detect → identify → rip (redumper) → compress (chdman) → move → notify → eject
package cdgame

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// RedumperRipper is the slice of *tools.Redumper used at rip-time.
type RedumperRipper interface {
	Rip(ctx context.Context, devPath, outDir, name, mode string, sink tools.Sink) error
}

// CHDManCompressor is the slice of *tools.CHDMan used at compress-time.
type CHDManCompressor interface {
	CreateCHD(ctx context.Context, input, output string, sink tools.Sink) error
}

// Identifier is the per-system pre-rip identify step. It is the only part
// of the CD-game pipeline that varies between consoles.
type Identifier interface {
	Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error)
}

// RipFormat selects the redumper mode and output file layout used by RunRip
// and RunTranscode.  CD-format discs (PSX, Saturn) produce bin+cue; DVD-format
// discs (PS2) produce a single iso.
type RipFormat int

const (
	// RipFormatCD is the default; redumper "cd" mode → rip.bin + rip.cue.
	RipFormatCD RipFormat = iota
	// RipFormatDVD uses redumper "dvd" mode → rip.iso.
	RipFormatDVD
)

// Deps bundles the handler's dependencies.
type Deps struct {
	DiscType   state.DiscType // the disc type this handler serves
	WorkPrefix string         // work-dir prefix, e.g. "psx" / "sat" / "ps2"
	Identifier Identifier     // per-system pre-rip identify
	// RipFormat controls redumper mode and spool file layout; defaults to
	// RipFormatCD (bin+cue) when zero.
	RipFormat RipFormat

	// PostRipIdentify makes RunTranscode identify the disc post-rip via a
	// Redump MD5 dat lookup (filling title/year) instead of boot-code
	// MD5-verify. For CD consoles with no pre-rip boot code (Sega CD, 3DO,
	// PC-FX, ...); psx/ps2/saturn leave this false.
	PostRipIdentify bool

	Redumper RedumperRipper
	CHDMan   CHDManCompressor
	RedumpDB *identify.RedumpDB // post-rip MD5 verify (boot-code keyed)
	Tools    *tools.Registry    // looked up: apprise, eject

	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler and pipelines.SplittableHandler for
// the CD-game family.
type Handler struct{ deps Deps }

// New builds a Handler, defaulting LibraryProbe to pipelines.ProbeWritable.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return h.deps.DiscType }

// Identify delegates to the injected per-system Identifier.
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	return h.deps.Identifier.Identify(ctx, drv)
}

// Plan returns the 7-active-step plan; transcode is skipped. Used by the
// monolithic Run fallback.
func (h *Handler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	skipped := map[state.StepID]bool{state.StepTranscode: true}
	out := make([]pipelines.StepPlan, 0, len(state.CanonicalSteps()))
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skipped[sid]})
	}
	return out
}

// PlanRip — rip-half: detect, identify, rip, eject active; transcode-half
// marked Skip.
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

// PlanTranscode — transcode-half: CD-game discs have no transcode (skip),
// runs compress (chdman), move, notify.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: sid == state.StepTranscode})
	}
	return out
}

// spoolName is the filename prefix used inside the spool's rip/ subdir.
// Stable across discs — the per-job spool dir is already unique.
const spoolName = "rip"

// Run is the monolithic fallback path. Allocates a tmpdir as spool, runs
// RunRip + RunTranscode in sequence, cleans up.
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

// RunRip executes the drive-bound half: detect, identify, redumper rip →
// spoolDir/rip/rip.bin + rip.cue, eject.
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
	if h.deps.Redumper == nil || h.deps.CHDMan == nil {
		err := fmt.Errorf("cdgame: redumper or chdman not configured (type=%s)", h.deps.DiscType)
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir rip: %w", err)
	}
	ripMode := "cd"
	spoolFile := spoolName + ".bin"
	if h.deps.RipFormat == RipFormatDVD {
		ripMode = "dvd"
		spoolFile = spoolName + ".iso"
	}
	if err := h.deps.Redumper.Rip(ctx, drv.DevPath, ripDir, spoolName, ripMode, pipelines.NewStepSink(sink, state.StepRip)); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("redumper: %w", err)
	}
	sink.OnStepDone(state.StepRip, map[string]any{"file": filepath.Join(ripDir, spoolFile)})

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return pipelines.RipResult{SpoolPath: spoolDir}, nil
}

// RunTranscode executes the compute-bound half: MD5-verify the rip against
// the Redump dat (when identified by boot code), chdman compress to .chd,
// atomic-move to library, notify.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	ripDir := filepath.Join(result.SpoolPath, "rip")

	// CD format: verify bin, pass cue to chdman. DVD format: verify iso,
	// pass iso to chdman (createdvd reads the iso directly).
	var md5TargetPath, chdInputPath string
	if h.deps.RipFormat == RipFormatDVD {
		isoPath := filepath.Join(ripDir, spoolName+".iso")
		md5TargetPath = isoPath
		chdInputPath = isoPath
	} else {
		md5TargetPath = filepath.Join(ripDir, spoolName+".bin")
		chdInputPath = filepath.Join(ripDir, spoolName+".cue")
	}

	sink.OnStepStart(state.StepCompress)
	if h.deps.PostRipIdentify {
		h.md5Identify(md5TargetPath, disc)
	} else {
		h.md5Verify(md5TargetPath, disc)
	}
	chdPath := filepath.Join(result.SpoolPath, spoolName+".chd")
	if err := h.deps.CHDMan.CreateCHD(ctx, chdInputPath, chdPath, pipelines.NewStepSink(sink, state.StepCompress)); err != nil {
		sink.OnStepFailed(state.StepCompress, err)
		return fmt.Errorf("chdman: %w", err)
	}
	sink.OnStepDone(state.StepCompress, map[string]any{"file": chdPath})

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
	if err := pipelines.AtomicMove(chdPath, dst); err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	sink.OnStepDone(state.StepMove, map[string]any{"path": dst})

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, h.deps.WorkPrefix, discID)
}

// md5Verify checks the ripped file against the boot-code-keyed Redump dat
// entry. Used when the disc was pre-identified by boot code (PSX, PS2,
// Saturn). Logs only — a mismatch does not abort the pipeline.
func (h *Handler) md5Verify(path string, disc *state.Disc) {
	if h.deps.RedumpDB != nil && disc.MetadataID != "" {
		if entry := h.deps.RedumpDB.LookupByBootCode(disc.MetadataID); entry != nil && entry.MD5 != "" {
			got, err := pipelines.MD5File(path)
			if err != nil {
				slog.Warn("cdgame: md5 verify failed (couldn't hash)", "type", h.deps.DiscType, "err", err)
			} else if got != entry.MD5 {
				slog.Warn("cdgame: md5 mismatch", "type", h.deps.DiscType, "want", entry.MD5, "got", got)
			} else {
				slog.Info("cdgame: md5 verify ok", "type", h.deps.DiscType, "md5", got)
			}
		}
	}
}

// md5Identify hashes the ripped track and looks it up in the Redump dat,
// filling disc title/year/region in place on a hit. For CD consoles with no
// pre-rip boot code this is the only metadata source.
func (h *Handler) md5Identify(path string, disc *state.Disc) {
	if h.deps.RedumpDB == nil {
		slog.Warn("cdgame: no RedumpDB; skipping post-rip identify", "type", h.deps.DiscType)
		return
	}
	got, err := pipelines.MD5File(path)
	if err != nil {
		slog.Warn("cdgame: md5 hash failed", "type", h.deps.DiscType, "err", err)
		return
	}
	entry := h.deps.RedumpDB.LookupByMD5(got)
	if entry == nil {
		slog.Warn("cdgame: no Redump match", "type", h.deps.DiscType, "md5", got)
		return
	}
	slog.Info("cdgame: md5 identify ok", "type", h.deps.DiscType, "title", entry.Title, "md5", got)
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

// NoBootIdentifier is the pre-rip Identifier for CD consoles with no boot
// code: it sets the disc type + drive and defers naming to post-rip MD5.
type NoBootIdentifier struct{ DiscType state.DiscType }

func (i NoBootIdentifier) Identify(_ context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	return &state.Disc{Type: i.DiscType, DriveID: drv.ID}, nil, pipelines.ErrNoCandidates
}

var (
	_ pipelines.Handler           = (*Handler)(nil)
	_ pipelines.SplittableHandler = (*Handler)(nil)
	_ Identifier                  = NoBootIdentifier{}
)
