// Package saturn implements pipelines.Handler for Sega Saturn game discs.
//
// Pipeline shape (7 active steps; transcode skipped):
//
//	detect → identify → rip (redumper) → compress (chdman) → move → notify → eject
//
// Identify reads Saturn IP.BIN off sector 0 of the disc, then tries Redump
// dat (tier 1) and BootCodeIndex (tier 2). ErrNoCandidates surfaces when both miss.
package saturn

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

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

// Deps bundles the handler's dependencies.
type Deps struct {
	Redumper       RedumperRipper
	CHDMan         CHDManCompressor
	SaturnProber   identify.SaturnProber
	RedumpDB       *identify.RedumpDB
	BootCodeIndex  *identify.BootCodeIndex // Tier-2 fallback when Redump dat lacks bracketed product numbers
	Tools          *tools.Registry         // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler for Sega Saturn.
type Handler struct{ deps Deps }

func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return state.DiscTypeSAT }

// Identify reads Saturn IP.BIN, then tries two tiers of lookup:
// tier 1 — Redump dat; tier 2 — BootCodeIndex (Libretro).
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypeSAT, DriveID: drv.ID}

	if h.deps.SaturnProber == nil {
		return nil, nil, errors.New("saturn: SaturnProber not configured")
	}
	info, err := h.deps.SaturnProber.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("saturn: IP.BIN probe: %w", err)
	}
	if info == nil || info.ProductNumber == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}

	code := strings.ToUpper(strings.TrimSpace(info.ProductNumber))
	if code == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}

	// Tier 1: Redump dat.
	if h.deps.RedumpDB != nil {
		if entry := h.deps.RedumpDB.LookupByBootCode(code); entry != nil {
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = "Redump"
			disc.MetadataID = entry.BootCode
			disc.Candidates = []state.Candidate{{
				Source: "Redump", Title: entry.Title, Year: entry.Year,
				Region: entry.Region, Confidence: 100,
			}}
			return disc, disc.Candidates, nil
		}
	}

	// Tier 2: BootCodeIndex (Libretro).
	if h.deps.BootCodeIndex != nil {
		if entry := h.deps.BootCodeIndex.Lookup(state.DiscTypeSAT, code); entry != nil {
			region := entry.Region
			if region == "" {
				region = identify.InferRegion(code)
			}
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = h.deps.BootCodeIndex.Source(state.DiscTypeSAT)
			disc.MetadataID = code
			disc.Candidates = []state.Candidate{{
				Source: disc.MetadataProvider, Title: entry.Title, Year: entry.Year,
				Region: region, Confidence: 90,
			}}
			return disc, disc.Candidates, nil
		}
	}

	slog.Info("sat: no Redump or BootCodeIndex match", "dev", drv.DevPath, "product", code)
	return disc, nil, pipelines.ErrNoCandidates
}

// Plan returns the 7-active-step plan; transcode is skipped. Used by
// the monolithic Run fallback.
func (h *Handler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	skipped := map[state.StepID]bool{state.StepTranscode: true}
	out := make([]pipelines.StepPlan, 0, len(state.CanonicalSteps()))
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skipped[sid]})
	}
	return out
}

// PlanRip — rip-half: detect, identify, rip, eject. Transcode-half
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

// PlanTranscode — transcode-half: Saturn has no transcode (skip),
// runs compress (chdman), move, notify.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: sid == state.StepTranscode})
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

// RunRip executes the drive-bound half.
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
		err := errors.New("saturn: redumper or chdman not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir rip: %w", err)
	}
	if err := h.deps.Redumper.Rip(ctx, drv.DevPath, ripDir, spoolName, "cd", pipelines.NewStepSink(sink, state.StepRip)); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("redumper: %w", err)
	}
	binPath := filepath.Join(ripDir, spoolName+".bin")
	sink.OnStepDone(state.StepRip, map[string]any{"file": binPath})

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return pipelines.RipResult{SpoolPath: spoolDir}, nil
}

// RunTranscode executes the compute-bound half: MD5-verify, chdman, move, notify.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	ripDir := filepath.Join(result.SpoolPath, "rip")
	binPath := filepath.Join(ripDir, spoolName+".bin")
	cuePath := filepath.Join(ripDir, spoolName+".cue")

	sink.OnStepStart(state.StepCompress)
	if h.deps.RedumpDB != nil && disc.MetadataID != "" {
		if entry := h.deps.RedumpDB.LookupByBootCode(disc.MetadataID); entry != nil && entry.MD5 != "" {
			got, err := pipelines.MD5File(binPath)
			if err != nil {
				slog.Warn("saturn: md5 verify failed (couldn't hash)", "err", err)
			} else if got != entry.MD5 {
				slog.Warn("saturn: md5 mismatch", "want", entry.MD5, "got", got)
			} else {
				slog.Info("saturn: md5 verify ok", "md5", got)
			}
		}
	}
	chdPath := filepath.Join(result.SpoolPath, spoolName+".chd")
	if err := h.deps.CHDMan.CreateCHD(ctx, cuePath, chdPath, pipelines.NewStepSink(sink, state.StepCompress)); err != nil {
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

	pipelines.RunNotifyStep(ctx, sink, pipelines.NotifyDeps{
		Tools:          h.deps.Tools,
		URLsForTrigger: h.deps.URLsForTrigger,
		LibraryRoot:    h.deps.LibraryRoot,
	}, disc)
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "sat", discID)
}
