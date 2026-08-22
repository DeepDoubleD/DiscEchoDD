// Package bdmv implements pipelines.Handler for Blu-ray (BDMV) discs.
//
// Pipeline shape (7 active steps; compress skipped):
//
//	detect → identify → rip (MakeMKV) → transcode (HandBrake)
//	    → move → notify → eject
//
// MakeMKV decrypts AACS and demuxes the chosen title to .mkv;
// HandBrake transcodes that .mkv to the profile's preset; the result
// atomic-moves into the library.
package bdmv

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// MakeMKVScanner is the slice of tools.MakeMKV used at scan-time. sink
// receives MakeMKV's MSG: lines as info logs during the multi-minute
// info enumeration; tests can pass tools.NopSink{}.
type MakeMKVScanner interface {
	Scan(ctx context.Context, devPath string, sink tools.Sink) ([]tools.MakeMKVTitle, error)
}

// MakeMKVRipper is the slice of tools.MakeMKV used at rip-time.
type MakeMKVRipper interface {
	Rip(ctx context.Context, devPath string, titleID int, outDir string, sink tools.Sink) error
}

// Deps bundles the handler's dependencies for mock injection.
type Deps struct {
	Prober         identify.DVDProber // re-used for volume-label reading
	BDProber       identify.BDProber  // optional: disc-library metadata name, preferred over the volume label when present
	TMDB           identify.TMDBClient
	// MKVSubs pulls text-based subtitle tracks out of the moved output
	// as sidecar files when the profile's extract_text_subtitles option
	// is set. nil disables the feature regardless of profile options.
	MKVSubs pipelines.MKVSubtitleTool
	MakeMKVScanner MakeMKVScanner
	MakeMKVRipper  MakeMKVRipper
	Tools          *tools.Registry // looked up: handbrake, apprise, eject
	LibraryRoot    string
	WorkRoot       string
	LibraryProbe   func(string) error
	URLsForTrigger func(ctx context.Context, trigger string) []string
	SubsLang       string
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool

	// NVENCAvailable signals that NVIDIA NVENC is usable on the host.
	// When true and the profile requests an nvenc_* video_codec, the
	// transcode step passes the hardware encoder to HandBrake.
	// When false, NVENC profile values fall back to the closest
	// software encoder. BDMV's pipeline has always emitted 10-bit
	// HEVC, so software h264/h265 results are promoted to x265_10bit
	// to preserve bit-depth.
	NVENCAvailable bool
}

// Handler implements pipelines.Handler for BDMV.
type Handler struct{ deps Deps }

// New constructs the handler.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

// DiscType returns BDMV.
func (h *Handler) DiscType() state.DiscType { return state.DiscTypeBDMV }

// Identify reads the volume label and queries TMDB.
//
//   - Junk label → ErrNoCandidates
//   - TMDB returns 0 → ErrNoCandidates
//   - Otherwise → Disc with title+year+TMDB id from highest-confidence match
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	if h.deps.Prober == nil {
		return nil, nil, errors.New("bdmv: prober not configured")
	}
	if h.deps.TMDB == nil {
		return nil, nil, errors.New("bdmv: TMDB client not configured")
	}

	info, err := h.deps.Prober.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("bd probe: %w", err)
	}
	disc := &state.Disc{Type: state.DiscTypeBDMV, DriveID: drv.ID}

	label := identify.BDSearchLabel(ctx, h.deps.BDProber, drv.DevPath, info.VolumeLabel)
	q := identify.NormaliseDVDLabel(label)
	if q == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}
	cands, err := h.deps.TMDB.SearchBoth(ctx, q)
	if err != nil {
		return nil, nil, fmt.Errorf("tmdb search: %w", err)
	}
	if len(cands) == 0 {
		return disc, nil, pipelines.ErrNoCandidates
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].Confidence > cands[j].Confidence })
	top := cands[0]
	disc.Title = top.Title
	disc.Year = top.Year
	disc.MetadataProvider = top.Source
	disc.MetadataID = strconv.Itoa(top.TMDBID)
	disc.Candidates = cands
	return disc, cands, nil
}

// Plan returns the 7-active-step plan; only compress is skipped. Used
// by the monolithic fallback path. The split-path orchestrator calls
// PlanRip + PlanTranscode instead.
func (h *Handler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	skipped := map[state.StepID]bool{state.StepCompress: true}
	out := make([]pipelines.StepPlan, 0, 8)
	for _, sid := range state.CanonicalSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skipped[sid]})
	}
	return out
}

// PlanRip is the rip-half plan: full 8-step canonical list with
// transcode/compress/move/notify marked Skip so they render as
// 'skipped' on the rip-job row. Active steps: detect, identify, rip,
// eject. The transcode-half steps run on the child transcode job
// materialised by PlanTranscode.
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

// PlanTranscode is the transcode-half plan for the child compute-queue
// job. BDMV doesn't run a compress step, so it's marked Skip. Active
// steps: transcode, move, notify.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: sid == state.StepCompress})
	}
	return out
}

// Run is the monolithic-fallback path. The orchestrator picks it when
// Compute/Spool aren't wired; it allocates a tmpdir for the
// intermediate output, runs RunRip then RunTranscode in sequence, and
// cleans the tmpdir up afterwards. Existing callers (monolithic test
// suite, future audio-CD-style pipelines) keep their semantics.
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

// RunRip is the drive-bound half. Steps: detect, identify, MakeMKV
// rip into spoolDir/rip/, eject. When disc.metadata_json carries
// selected_title_ids (user came through the picker), rips exactly
// those titles; otherwise falls back to pipelines.PickLongestTitle (legacy
// single-title behaviour). Drive freed on return.
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
	if h.deps.MakeMKVScanner == nil || h.deps.MakeMKVRipper == nil {
		err := errors.New("bdmv: MakeMKV not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}

	ripStepSink := pipelines.NewStepSink(sink, state.StepRip)
	ripStepSink.SubStep("scan")
	sink.OnLog(state.LogLevelInfo, "MakeMKV: scanning %s", drv.DevPath)
	titles, err := h.deps.MakeMKVScanner.Scan(ctx, drv.DevPath, ripStepSink)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("makemkv scan: %w", err)
	}
	ripStepSink.SubStep("read_raw_data")

	// Pick titles to rip: user selection wins when present, otherwise
	// fall back to the legacy longest-title heuristic so movie-profile
	// auto-flow keeps working.
	picked, mainID, err := pipelines.SelectMovieTitles(titles, prof, disc)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	switch {
	case mainID > 0:
		sink.OnLog(state.LogLevelInfo, "MakeMKV: scan complete, ripping main feature + %d bonus title(s)", len(picked)-1)
	case len(picked) > 1:
		sink.OnLog(state.LogLevelInfo, "MakeMKV: scan complete, ripping %d titles", len(picked))
	default:
		sink.OnLog(state.LogLevelInfo, "MakeMKV: scan complete, picked title %d (%s)",
			picked[0].ID, pipelines.HumanDuration(time.Duration(picked[0].DurationSec)*time.Second))
	}

	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripStart := time.Now()
	for i, t := range picked {
		titleSink := pipelines.NewMultiTitleSink(ripStepSink, i+1, len(picked), ripStart)
		if err := h.deps.MakeMKVRipper.Rip(ctx, drv.DevPath, t.ID, ripDir, titleSink); err != nil {
			sink.OnStepFailed(state.StepRip, err)
			return pipelines.RipResult{}, fmt.Errorf("makemkv rip title %d: %w", t.ID, err)
		}
	}
	// Collect every .mkv MakeMKV wrote into ripDir. Multi-title rips
	// produce one .mkv per title; MakeMKV's CLI names them per the
	// source-file convention so no collisions even across multiple
	// Rip calls into the same dir.
	rippedFiles, err := listMKVIn(ripDir)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	if len(rippedFiles) == 0 {
		err := fmt.Errorf("no .mkv produced in %s", ripDir)
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	var totalSize int64
	for _, f := range rippedFiles {
		if fi, statErr := os.Stat(f); statErr == nil {
			totalSize += fi.Size()
		}
	}
	sink.OnLog(state.LogLevelInfo, "MakeMKV: rip complete, %d file(s) %s in %s",
		len(rippedFiles), pipelines.HumanBytes(totalSize), pipelines.HumanDuration(time.Since(ripStart)))
	notes := map[string]any{
		"title_id":     picked[0].ID,
		"duration_sec": picked[0].DurationSec,
		"rip_file":     rippedFiles[0],
	}
	if len(rippedFiles) > 1 {
		notes["rip_file_count"] = len(rippedFiles)
	}
	sink.OnStepDone(state.StepRip, notes)

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return pipelines.RipResult{
		SpoolPath: spoolDir,
		Notes: map[string]any{
			"title_id":     picked[0].ID,
			"duration_sec": picked[0].DurationSec,
			"rip_file":     rippedFiles[0],
			"rip_files":    rippedFiles,
		},
	}, nil
}

// listMKVIn lists every .mkv file at the top level of dir. Returns a
// stable lexical order so multi-title rips have predictable output
// naming downstream.
func listMKVIn(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".mkv" {
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(out)
	return out, nil
}

// RunTranscode is the compute-bound half. Steps: HandBrake encode each
// MakeMKV rip output (transcode), skip compress, atomic-move into the
// library (move), fire Apprise (notify). Multi-title rips encode each
// title in turn and append a `-titleNN` suffix to the rendered output
// path when paths would collide (typical for movie templates that
// don't reference {{.EpisodeNumber}}). Spool cleanup is the compute
// worker's responsibility.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	rippedFiles, err := listMKVIn(filepath.Join(result.SpoolPath, "rip"))
	if err != nil {
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return err
	}
	if len(rippedFiles) == 0 {
		err := fmt.Errorf("no .mkv rip output under %s/rip", result.SpoolPath)
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return err
	}

	if h.deps.Tools == nil {
		err := errors.New("bdmv: tools registry not configured")
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return err
	}
	hb, ok := h.deps.Tools.Get("handbrake")
	if !ok {
		err := errors.New("bdmv: handbrake tool not registered")
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return err
	}
	// Select the HandBrake encoder: NVENC if the profile asks for it
	// and the GPU was detected at boot, software otherwise. BDMV's
	// pipeline has always emitted 10-bit HEVC, so promote any software
	// h264/h265 result to x265_10bit (covers empty VideoCodec, explicit
	// x265, and fallback from nvenc_h265). NVENC values pass through;
	// hardware 10-bit NVENC needs an explicit --encoder-profile flag
	// and is tracked as a follow-up.
	encoder, fellBack := pipelines.SelectHandBrakeEncoder(prof, h.deps.NVENCAvailable)
	switch encoder {
	case "x264", "x265":
		encoder = "x265_10bit"
	}

	sink.OnStepStart(state.StepTranscode)
	if fellBack {
		sink.OnLog(state.LogLevelWarn,
			"NVENC requested but unavailable on host; falling back to %s software encoder", encoder)
	}
	transcodedFiles := make([]string, 0, len(rippedFiles))
	for i, rippedFile := range rippedFiles {
		transcodedFile := filepath.Join(result.SpoolPath, fmt.Sprintf("out_%02d.mkv", i+1))
		hbArgs := []string{
			"--input", rippedFile,
			"--output", transcodedFile,
			"--format", "av_mkv",
			"--encoder", encoder,
			"--quality", "19",
			"--all-audio",
			"--markers",
			// BDMV output is always MKV — keep every subtitle track the
			// disc carries rather than filtering to one language.
			"--all-subtitles",
		}
		hbArgs = append(hbArgs, pipelines.ResolutionAndAudioArgs(prof)...)
		sink.OnLog(state.LogLevelInfo, "HandBrake: encoding %s", filepath.Base(rippedFile))
		encStart := time.Now()
		if err := hb.Run(ctx, hbArgs, nil, result.SpoolPath, pipelines.NewStepSink(sink, state.StepTranscode)); err != nil {
			sink.OnStepFailed(state.StepTranscode, err)
			return fmt.Errorf("handbrake encode %s: %w", filepath.Base(rippedFile), err)
		}
		var encSize int64
		if fi, statErr := os.Stat(transcodedFile); statErr == nil {
			encSize = fi.Size()
		}
		sink.OnLog(state.LogLevelInfo, "HandBrake: %s done, %s in %s",
			filepath.Base(rippedFile),
			pipelines.HumanBytes(encSize), pipelines.HumanDuration(time.Since(encStart)))
		transcodedFiles = append(transcodedFiles, transcodedFile)
	}
	sink.OnStepDone(state.StepTranscode, nil)

	// move — atomic rename to library, one file per encoded title.
	sink.OnStepStart(state.StepMove)
	moved, err := pipelines.MoveMovieOutputs(h.deps.LibraryRoot, transcodedFiles, disc, prof)
	if err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return fmt.Errorf("move: %w", err)
	}
	for _, p := range moved {
		sink.OnLog(state.LogLevelInfo, "move: → %s", p)
	}
	if len(moved) == 1 {
		sink.OnStepDone(state.StepMove, map[string]any{"path": moved[0]})
	} else {
		sink.OnStepDone(state.StepMove, map[string]any{"paths": moved})
	}

	if h.deps.MKVSubs != nil && pipelines.ExtractTextSubtitlesFromProfile(prof) {
		for _, p := range moved {
			if _, err := pipelines.ExtractTextSubtitleSidecars(ctx, h.deps.MKVSubs, p, sink); err != nil {
				sink.OnLog(state.LogLevelWarn, "subtitle sidecar: %s: %v", filepath.Base(p), err)
			}
		}
	}

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "bdmv", discID)
}

// ScanTitles implements pipelines.TitleScanner: enumerate titles on
// the inserted disc via MakeMKV without ripping. Brief (drive-bound),
// doesn't eject — caller (orchestrator scan-job path) decides whether
// to free the drive or hold it for an immediate follow-on rip.
func (h *Handler) ScanTitles(ctx context.Context, drv *state.Drive, _ *state.Disc, _ *state.Profile, sink pipelines.EventSink) ([]pipelines.TitleInfo, error) {
	sink.OnStepStart(state.StepIdentify)
	defer sink.OnStepDone(state.StepIdentify, nil)

	if h.deps.MakeMKVScanner == nil {
		err := errors.New("bdmv: MakeMKV not configured")
		sink.OnStepFailed(state.StepIdentify, err)
		return nil, err
	}
	sink.OnLog(state.LogLevelInfo, "MakeMKV: scanning %s for titles", drv.DevPath)
	titles, err := h.deps.MakeMKVScanner.Scan(ctx, drv.DevPath, pipelines.NewStepSink(sink, state.StepIdentify))
	if err != nil {
		sink.OnStepFailed(state.StepIdentify, err)
		return nil, fmt.Errorf("makemkv scan: %w", err)
	}
	out := make([]pipelines.TitleInfo, 0, len(titles))
	for _, t := range titles {
		out = append(out, pipelines.TitleInfo{
			ID:           t.ID,
			DurationSec:  t.DurationSec,
			ChapterCount: t.Chapters,
			SourceFile:   t.SourceFile,
			SizeBytes:    t.SizeBytes,
		})
	}
	return out, nil
}
