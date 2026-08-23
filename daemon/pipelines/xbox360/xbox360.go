// Package xbox360 implements pipelines.Handler for Xbox 360 game discs.
//
// Pipeline shape (6 active steps; transcode AND compress skipped):
//
//	detect → identify → rip (redumper xbox360, --dvd-raw) → move → notify → eject
//
// Identify is a best-effort pre-rip attempt only: it tries reading
// default.xex off the disc via isoinfo (parsing the XEX Execution ID
// header for title ID), but on every real retail disc checked live
// this fails -- default.xex lives behind the security sectors, which
// aren't reachable through the stock optical-drive read isoinfo uses.
// A disc's filesystem listing carries only a decoy DVD-Video layer
// (Microsoft's "insert into an Xbox 360" branding for non-Xbox-360
// drives) plus a /_SYSTEMU folder -- classify.go's RefineDiscType uses
// that folder as the disc-type marker (not this prober; see the
// comment there). So Identify almost always returns ErrNoCandidates,
// and title/year come from RunTranscode's post-rip MD5 lookup against
// the Redump dat instead -- the same pattern the CD-only consoles use
// (pipelines/cdgame's PostRipIdentify), since there's no curated
// embedded boot-code fallback for Xbox 360 either.
//
// Rip requires an OmniDrive-flashed drive (e.g. an ASUS/Pioneer
// BW-16D1HT reflashed to Panasonic UJ-260 firmware) — XGD2/XGD3's
// security sectors aren't reachable through a plain DVD read, which is
// why the redumper invocation adds --dvd-raw on top of what the
// original Xbox pipeline's "xbox" mode does. See
// pipelines.DriveRoleForModel / PreferredDriveRole for the routing
// that keeps these discs off a drive that can't read them.
package xbox360

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Xbox360Prober reads default.xex from a disc device and returns parsed
// XEX Execution ID info.
type Xbox360Prober interface {
	Probe(ctx context.Context, devPath string) (*identify.Xbox360Info, error)
}

// IsoinfoXbox360Prober implements Xbox360Prober by shelling out to
// isoinfo to extract default.xex bytes without mounting the disc.
type IsoinfoXbox360Prober struct {
	// Bin is the isoinfo binary name. Defaults to "isoinfo".
	Bin string
}

func (p *IsoinfoXbox360Prober) bin() string {
	if p.Bin == "" {
		return "isoinfo"
	}
	return p.Bin
}

// Probe extracts /default.xex from devPath using `isoinfo -i <devPath> -x /default.xex`
// and parses the result with identify.ProbeXEX.
func (p *IsoinfoXbox360Prober) Probe(ctx context.Context, devPath string) (*identify.Xbox360Info, error) {
	out, err := exec.CommandContext(ctx, p.bin(), "-i", devPath, "-x", "/default.xex").Output()
	if err != nil {
		return nil, fmt.Errorf("isoinfo -x /default.xex: %w", err)
	}
	info, err := identify.ProbeXEX(out)
	if err != nil {
		return nil, err
	}
	return info, nil
}

// RedumperRipper is the subset of *tools.Redumper used at rip-time.
type RedumperRipper interface {
	Rip(ctx context.Context, devPath, outDir, name, mode string, sink tools.Sink) error
}

// Deps bundles the handler's dependencies.
type Deps struct {
	Redumper       RedumperRipper
	Xbox360Prober  Xbox360Prober
	RedumpDB       *identify.RedumpDB
	Tools          *tools.Registry // looked up: apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler for Xbox 360.
type Handler struct{ deps Deps }

// New returns a Handler with the given dependencies.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

func (h *Handler) DiscType() state.DiscType { return state.DiscTypeXBOX360 }

// Identify is a best-effort pre-rip attempt: if default.xex happens to
// be reachable via isoinfo (not true for any real disc checked live
// -- see the package doc), its title ID is looked up against the
// Redump dat. Otherwise -- the expected case -- this returns the disc
// typed as XBOX360 with no candidates, and RunTranscode's post-rip MD5
// lookup does the real identification once the ISO exists. A prober
// failure or nil Xbox360Prober is therefore not an error worth
// aborting over: it's the normal path, not a fault.
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	disc := &state.Disc{Type: state.DiscTypeXBOX360, DriveID: drv.ID}

	if h.deps.Xbox360Prober == nil {
		return disc, nil, pipelines.ErrNoCandidates
	}
	info, err := h.deps.Xbox360Prober.Probe(ctx, drv.DevPath)
	if err != nil || info == nil {
		return disc, nil, pipelines.ErrNoCandidates
	}

	// Store the 8-hex-digit title ID so RunTranscode can re-fetch the
	// entry for MD5 verify (mirrors the original Xbox pipeline).
	code := fmt.Sprintf("%08X", info.TitleID)

	if h.deps.RedumpDB != nil {
		if entry := h.deps.RedumpDB.LookupByXboxTitleID(info.TitleID); entry != nil {
			disc.Title = entry.Title
			disc.Year = entry.Year
			disc.MetadataProvider = "Redump"
			disc.MetadataID = code
			disc.Candidates = []state.Candidate{{
				Source: "Redump", Title: entry.Title, Year: entry.Year,
				Region: entry.Region, Confidence: 100,
			}}
			return disc, disc.Candidates, nil
		}
	}

	slog.Info("xbox360: XEX probe succeeded but no Redump title-ID match", "dev", drv.DevPath, "title_id", code)
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

// PlanTranscode — transcode-half: Xbox 360 has no transcode AND no
// compress (the .iso is the deliverable, MD5-verified against Redump
// before the move). Only move + notify are active.
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

// RunRip executes the drive-bound half: detect, identify, redumper
// xbox360 mode (--dvd-raw) → spoolDir/rip/rip.iso, eject.
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
		err := errors.New("xbox360: redumper not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("mkdir rip: %w", err)
	}
	if err := h.deps.Redumper.Rip(ctx, drv.DevPath, ripDir, spoolName, "xbox360", pipelines.NewStepSink(sink, state.StepRip)); err != nil {
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

// RunTranscode executes the compute-bound half: MD5 verify (warn-only;
// non-fatal) then atomic-move to the library, notify. No compress.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	ripDir := filepath.Join(result.SpoolPath, "rip")
	isoPath := filepath.Join(ripDir, spoolName+".iso")

	if disc.MetadataID != "" {
		// The rare bonus case: Identify's pre-rip XEX probe actually
		// succeeded, so the title is already known -- just verify.
		h.md5Verify(isoPath, disc)
	} else {
		// The expected case: no pre-rip title. Hash the finished ISO and
		// look it up by MD5 -- the only identification path that
		// actually works for a real disc (see the package doc). Must
		// run before RenderOutputPath below so {{.Title}} isn't empty.
		h.md5Identify(isoPath, disc)
	}

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
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "xbox360", discID)
}

// md5Verify checks the ripped ISO against the title-ID-keyed Redump
// entry. Used only in the rare case Identify's pre-rip XEX probe
// actually found a title. Logs only — a mismatch does not abort the
// pipeline.
func (h *Handler) md5Verify(path string, disc *state.Disc) {
	if h.deps.RedumpDB == nil || disc.MetadataID == "" {
		return
	}
	titleID, err := strconv.ParseUint(disc.MetadataID, 16, 32)
	if err != nil {
		return
	}
	entry := h.deps.RedumpDB.LookupByXboxTitleID(uint32(titleID))
	if entry == nil || entry.MD5 == "" {
		return
	}
	got, err := pipelines.MD5File(path)
	if err != nil {
		slog.Warn("xbox360: md5 verify failed (couldn't hash)", "err", err)
	} else if got != entry.MD5 {
		slog.Warn("xbox360: md5 mismatch", "want", entry.MD5, "got", got)
	} else {
		slog.Info("xbox360: md5 verify ok", "md5", got)
	}
}

// md5Identify hashes the ripped ISO and looks it up in the Redump dat,
// filling disc title/year/region in place on a hit. This is the
// primary identification path for Xbox 360 -- see the package doc for
// why the pre-rip route essentially never works.
func (h *Handler) md5Identify(path string, disc *state.Disc) {
	if h.deps.RedumpDB == nil {
		slog.Warn("xbox360: no RedumpDB; skipping post-rip identify")
		return
	}
	got, err := pipelines.MD5File(path)
	if err != nil {
		slog.Warn("xbox360: md5 hash failed", "err", err)
		return
	}
	entry := h.deps.RedumpDB.LookupByMD5(got)
	if entry == nil {
		slog.Warn("xbox360: no Redump match", "md5", got)
		return
	}
	slog.Info("xbox360: md5 identify ok", "title", entry.Title, "md5", got)
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
