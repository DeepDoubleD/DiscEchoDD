// Package dvdvideo implements pipelines.Handler for DVD-Video discs.
package dvdvideo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// DVDMirror mirrors a CSS-protected DVD-Video disc's VIDEO_TS to a
// local directory using dvdbackup + libdvdcss. Returns the path to
// the produced `<VOLUME_LABEL>/` directory (one level above
// VIDEO_TS/), which HandBrake's `--input` can read directly.
type DVDMirror interface {
	Mirror(ctx context.Context, devPath, outDir string, sink tools.Sink) (string, error)
}

// HandBrakeScanner enumerates titles in a local VIDEO_TS tree. The
// transcode step uses it (after the rip step has produced the local
// copy) to discover title durations for movie / series selection.
type HandBrakeScanner interface {
	Scan(ctx context.Context, source string) ([]tools.HandBrakeTitle, error)
}

// MakeMKVScanner is the slice of tools.MakeMKV used at scan-time. Used
// by profiles whose engine is "MakeMKV" or "MakeMKV+HandBrake" — the
// HandBrake-engine path continues to scan via dvdbackup'd VIDEO_TS.
// sink receives MakeMKV's MSG: lines as info logs during the
// multi-minute info enumeration; tests can pass tools.NopSink{}.
type MakeMKVScanner interface {
	Scan(ctx context.Context, devPath string, sink tools.Sink) ([]tools.MakeMKVTitle, error)
}

// MakeMKVRipper is the slice of tools.MakeMKV used at rip-time. Same
// engine constraint as MakeMKVScanner above.
type MakeMKVRipper interface {
	Rip(ctx context.Context, devPath string, titleID int, outDir string, sink tools.Sink) error
}

// Deps bundles the handler's dependencies for mock injection.
// MetadataStore is the thin slice of *state.Store the pipeline needs
// to update disc.metadata_json mid-run (e.g. to persist the scan title
// list for the pane's Files tab). Nil-safe; the handler skips the
// write when this is unset.
type MetadataStore interface {
	UpdateDiscMetadataBlob(ctx context.Context, id string, blob string) error
}

type Deps struct {
	Prober           identify.DVDProber
	TMDB             identify.TMDBClient
	DVDBackup        DVDMirror
	HandBrakeScanner HandBrakeScanner
	MakeMKVScanner   MakeMKVScanner
	MakeMKVRipper    MakeMKVRipper
	Tools            *tools.Registry
	LibraryRoot      string
	WorkRoot         string
	LibraryProbe     func(string) error
	URLsForTrigger   func(ctx context.Context, trigger string) []string
	SubsLang         string        // e.g. "eng"; empty → no --subtitle-lang-list flag
	MetadataStore    MetadataStore // optional; pipeline persists scan title list when set
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool

	// MinEncodedBytesPerSecond is the lower-bound bytes-per-second the
	// encoded output must hit for the transcode step to be considered
	// successful. 0 → use the package default (≈ 750 kbps). A negative
	// value disables the check (used by tests with stub encoders that
	// don't write real-sized output).
	MinEncodedBytesPerSecond int

	// NVENCAvailable signals that NVIDIA NVENC is usable on the host.
	// When true and the profile requests an nvenc_* video_codec, the
	// transcode step passes the hardware encoder to HandBrake.
	// When false, NVENC profile values fall back to the matching
	// software encoder (x264/x265) with a WARN log.
	NVENCAvailable bool
}

// Handler implements pipelines.Handler for DVD-Video.
type Handler struct {
	deps Deps
}

// New constructs the handler.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

// DiscType returns DVD.
func (h *Handler) DiscType() state.DiscType { return state.DiscTypeDVD }

// Identify reads the DVD volume label and queries TMDB.
//
//   - Garbage label → ErrNoCandidates
//   - TMDB returns 0 → ErrNoCandidates (UI prompts manual)
//   - Otherwise → Disc with title+year+TMDB id from highest-confidence candidate
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	if h.deps.Prober == nil {
		return nil, nil, errors.New("dvdvideo: prober not configured")
	}
	if h.deps.TMDB == nil {
		return nil, nil, errors.New("dvdvideo: TMDB client not configured")
	}

	info, err := h.deps.Prober.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("dvd probe: %w", err)
	}
	disc := &state.Disc{
		Type:    state.DiscTypeDVD,
		DriveID: drv.ID,
	}
	q := identify.NormaliseDVDLabel(info.VolumeLabel)
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

// Plan returns the 8-step plan; only `compress` is skipped for DVD.
// Used by the monolithic Run fallback.
func (h *Handler) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	skipped := map[state.StepID]bool{state.StepCompress: true}
	out := make([]pipelines.StepPlan, 0, 8)
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

// PlanTranscode — transcode-half: DVD runs HandBrake (transcode),
// skips compress, runs move + notify.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		out = append(out, pipelines.StepPlan{ID: sid, Skip: sid == state.StepCompress})
	}
	return out
}

// Run is the monolithic fallback path: allocates a tmpdir as spool,
// runs RunRip + RunTranscode in sequence, cleans up.
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

// RunRip executes the drive-bound half: detect, identify, rip, eject.
// The rip step's tool chain depends on the profile's engine:
//
//   - "HandBrake" (or unset, the legacy default) — dvdbackup mirrors
//     VIDEO_TS into spoolDir/rip/<VOLUME_LABEL>/. The transcode step
//     later runs HandBrake against the local mirror, so the drive is
//     free for the next disc as soon as eject completes.
//   - "MakeMKV" / "MakeMKV+HandBrake" — MakeMKV scans the disc for
//     titles, picks the relevant ones (movie/series + extras), rips
//     each title to a .mkv in spoolDir/rip/. Skips the dvdbackup
//     mirror entirely; gains MakeMKV's decryption coverage for newer
//     CSS variants and (for the passthrough engine) lets the move
//     step ship MPEG-2-in-MKV straight to the library.
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

	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("create rip dir: %w", err)
	}

	var (
		result pipelines.RipResult
		err    error
	)
	if engineUsesMakeMKV(prof) {
		result, err = h.runRipMakeMKV(ctx, drv, disc, prof, spoolDir, ripDir, sink)
	} else {
		result, err = h.runRipDVDBackup(ctx, drv, spoolDir, ripDir, sink)
	}
	if err != nil {
		return pipelines.RipResult{}, err
	}

	pipelines.RunEjectStep(ctx, sink, pipelines.EjectDeps{
		Tools:       h.deps.Tools,
		ShouldEject: h.deps.ShouldEject,
	}, drv)

	return result, nil
}

// engineUsesMakeMKV reports whether the profile's engine string asks
// for MakeMKV-driven ripping. Empty engine falls back to dvdbackup
// (legacy default).
func engineUsesMakeMKV(prof *state.Profile) bool {
	if prof == nil {
		return false
	}
	switch prof.Engine {
	case "MakeMKV", "MakeMKV+HandBrake":
		return true
	}
	return false
}

// runRipDVDBackup is the legacy rip path: dvdbackup mirrors VIDEO_TS
// into spoolDir/rip/<VOLUME_LABEL>/. Drive-bound; emits step events
// to sink.
func (h *Handler) runRipDVDBackup(ctx context.Context, drv *state.Drive, spoolDir, ripDir string, sink pipelines.EventSink) (pipelines.RipResult, error) {
	if h.deps.DVDBackup == nil {
		err := errors.New("dvdvideo: DVDBackup not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	if h.deps.HandBrakeScanner == nil {
		err := errors.New("dvdvideo: HandBrakeScanner not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	sink.OnLog(state.LogLevelInfo, "dvdbackup: mirroring %s → spool", drv.DevPath)
	mirrorStart := time.Now()
	source, err := h.deps.DVDBackup.Mirror(ctx, drv.DevPath, ripDir, pipelines.NewStepSink(sink, state.StepRip))
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("dvdbackup mirror: %w", err)
	}
	sink.OnLog(state.LogLevelInfo, "dvdbackup: complete in %s",
		pipelines.HumanDuration(time.Since(mirrorStart)))
	sink.OnStepDone(state.StepRip, map[string]any{"source": source})

	return pipelines.RipResult{
		SpoolPath: spoolDir,
		Notes:     map[string]any{"source": source, "shape": "video_ts"},
	}, nil
}

// runRipMakeMKV is the MakeMKV rip path: scan titles, pick the
// relevant ones, rip each to a .mkv under spoolDir/rip/. Used when the
// profile's engine is "MakeMKV" or "MakeMKV+HandBrake". Mirrors the
// shape of bdmv.RunRip.
func (h *Handler) runRipMakeMKV(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, spoolDir, ripDir string, sink pipelines.EventSink) (pipelines.RipResult, error) {
	if h.deps.MakeMKVScanner == nil || h.deps.MakeMKVRipper == nil {
		err := errors.New("dvdvideo: MakeMKV not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripStepSink := pipelines.NewStepSink(sink, state.StepRip)
	// "scan" substep covers MakeMKV's info enumeration — on slim USB
	// drives this can be 5+ minutes before any rip bytes flow. The
	// dashboard reads active_substep and shows "Scanning titles…".
	ripStepSink.SubStep("scan")
	sink.OnLog(state.LogLevelInfo, "MakeMKV: scanning %s", drv.DevPath)
	titles, err := h.deps.MakeMKVScanner.Scan(ctx, drv.DevPath, ripStepSink)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("makemkv scan: %w", err)
	}
	ripStepSink.SubStep("read_raw_data")

	picked, mainID, err := selectDVDMakeMKVTitles(titles, prof, disc)
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

	ripStart := time.Now()
	for i, t := range picked {
		sink.OnLog(state.LogLevelInfo, "MakeMKV: ripping title %d (%s)",
			t.ID, pipelines.HumanDuration(time.Duration(t.DurationSec)*time.Second))
		titleSink := pipelines.NewMultiTitleSink(ripStepSink, i+1, len(picked), ripStart)
		if err := h.deps.MakeMKVRipper.Rip(ctx, drv.DevPath, t.ID, ripDir, titleSink); err != nil {
			sink.OnStepFailed(state.StepRip, err)
			return pipelines.RipResult{}, fmt.Errorf("makemkv rip title %d: %w", t.ID, err)
		}
	}
	rippedFiles, err := listMKVIn(ripDir)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("makemkv rip: list outputs: %w", err)
	}
	if len(rippedFiles) == 0 {
		err := fmt.Errorf("makemkv rip: no .mkv outputs in %s", ripDir)
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	var totalBytes int64
	for _, f := range rippedFiles {
		if fi, statErr := os.Stat(f); statErr == nil {
			totalBytes += fi.Size()
		}
	}
	sink.OnLog(state.LogLevelInfo, "MakeMKV: rip complete, %d file(s) %s in %s",
		len(rippedFiles), pipelines.HumanBytes(totalBytes),
		pipelines.HumanDuration(time.Since(ripStart)))
	sink.OnStepDone(state.StepRip, map[string]any{
		"title_id":  picked[0].ID,
		"rip_files": rippedFiles,
		"shape":     "mkv_files",
	})

	return pipelines.RipResult{
		SpoolPath: spoolDir,
		Notes: map[string]any{
			"title_id":  picked[0].ID,
			"rip_files": rippedFiles,
			"shape":     "mkv_files",
		},
	}, nil
}

// selectDVDMakeMKVTitles picks which MakeMKV-scanned titles to rip
// based on the profile shape. Resolution order:
//
//  1. User picker (disc.metadata_json.selected_title_ids) wins.
//  2. Movie profile → pipelines.SelectMovieTitles (longest + optional
//     extras).
//  3. Series profile → every title >= options.min_title_seconds.
//
// mainID is non-zero only when extras mode kicked in alongside a
// longest-title auto-pick; downstream code uses it to route extras
// into bucket folders.
func selectDVDMakeMKVTitles(titles []tools.MakeMKVTitle, prof *state.Profile, disc *state.Disc) ([]tools.MakeMKVTitle, int, error) {
	if ids := pipelines.SelectedTitleIDsFromDisc(disc); len(ids) > 0 {
		return pipelines.SelectMovieTitles(titles, prof, disc)
	}
	if IsMovieProfile(prof) {
		return pipelines.SelectMovieTitles(titles, prof, disc)
	}
	minSec := pipelines.IntOption(prof, "min_title_seconds", 300)
	out := make([]tools.MakeMKVTitle, 0, len(titles))
	for _, t := range titles {
		if t.DurationSec >= minSec {
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		return nil, 0, fmt.Errorf("no MakeMKV title with duration >= %ds", minSec)
	}
	return out, 0, nil
}

// listMKVIn lists every .mkv file at the top level of dir, in stable
// lexical order. Mirrors bdmv's helper; kept package-private here to
// avoid leaking a generic file-listing primitive into the shared
// pipelines package.
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

// RunTranscode executes the compute-bound half. Branches on the
// profile's engine:
//
//   - "HandBrake" — HandBrake scan + per-title encode of the
//     dvdbackup'd VIDEO_TS mirror → spool/title*.{mkv,mp4}, atomic
//     move, notify.
//   - "MakeMKV+HandBrake" — HandBrake-encode each .mkv rip output to a
//     transcode/ subdir, atomic move, notify.
//   - "MakeMKV" — no transcode (step is emitted as skipped to keep the
//     stepper UIs honest), atomic-move .mkv rip outputs to library,
//     notify.
//
// For the HandBrake path, the source dir is resolved by walking
// spool/rip/ when Notes is empty so a daemon-crash + restart can
// still find the right input (Notes is in-memory only).
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	if engineUsesMakeMKV(prof) {
		return h.runTranscodeMakeMKV(ctx, result, disc, prof, sink)
	}
	return h.runTranscodeHandBrake(ctx, result, disc, prof, sink)
}

// runTranscodeHandBrake is the legacy transcode path: HandBrake scans
// the dvdbackup'd VIDEO_TS source, encodes each picked title to the
// profile's container/codec, atomic-moves outputs to the library.
func (h *Handler) runTranscodeHandBrake(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	source := stringFromNotes(result.Notes, "source")
	if source == "" {
		// Fallback after a daemon-crash restart: walk the rip dir for
		// the single VOLUME_LABEL subdirectory dvdbackup created.
		ripDir := filepath.Join(result.SpoolPath, "rip")
		entries, err := os.ReadDir(ripDir)
		if err != nil {
			sink.OnStepStart(state.StepTranscode)
			sink.OnStepFailed(state.StepTranscode, err)
			return fmt.Errorf("transcode: %w", err)
		}
		for _, e := range entries {
			if e.IsDir() {
				source = filepath.Join(ripDir, e.Name())
				break
			}
		}
		if source == "" {
			err := errors.New("transcode: no dvdbackup output found in spool/rip/")
			sink.OnStepStart(state.StepTranscode)
			sink.OnStepFailed(state.StepTranscode, err)
			return err
		}
	}

	sink.OnStepStart(state.StepTranscode)
	sink.OnLog(state.LogLevelInfo, "HandBrake: scanning titles")
	titles, err := h.deps.HandBrakeScanner.Scan(ctx, source)
	if err != nil {
		sink.OnStepFailed(state.StepTranscode, err)
		return fmt.Errorf("handbrake scan: %w", err)
	}
	logScannedTitles(disc.ID, titles)
	if longest := longestTitle(titles); longest.DurationSeconds > 0 {
		sink.OnLog(state.LogLevelInfo, "HandBrake: scan complete, %d title(s), longest %s",
			len(titles),
			pipelines.HumanDuration(time.Duration(longest.DurationSeconds)*time.Second))
	} else {
		sink.OnLog(state.LogLevelInfo, "HandBrake: scan complete, %d title(s)", len(titles))
	}
	warnOnRuntimeMismatch(disc, titles)
	if h.deps.MetadataStore != nil {
		_ = mergeMetadataField(ctx, h.deps.MetadataStore, disc.ID, disc.MetadataJSON, "dvd_titles", titles)
	}

	whb, ok := h.deps.Tools.Get("handbrake")
	if !ok {
		err := errors.New("dvdvideo: handbrake tool not registered")
		sink.OnStepFailed(state.StepTranscode, err)
		return err
	}
	ext := strings.ToLower(prof.Format)
	if ext != "mp4" && ext != "mkv" {
		ext = "mkv"
	}
	isMovie := IsMovieProfile(prof)

	// User picked specific titles via the picker — honour them and
	// skip the auto-pick heuristic entirely (movie/series mode no
	// longer matters; main-feature is dropped since the user told us
	// what to rip). mainNum is the title number to treat as the main
	// feature; 0 means "no extras flow" so moveOutputs renders every
	// title via the standard template.
	pickedIDs := pipelines.SelectedTitleIDsFromDisc(disc)
	var encodeTitles []tools.HandBrakeTitle
	mainNum := 0
	switch {
	case len(pickedIDs) > 0:
		byNumber := make(map[int]tools.HandBrakeTitle, len(titles))
		for _, t := range titles {
			byNumber[t.Number] = t
		}
		encodeTitles = make([]tools.HandBrakeTitle, 0, len(pickedIDs))
		for _, id := range pickedIDs {
			if t, ok := byNumber[id]; ok {
				encodeTitles = append(encodeTitles, t)
			}
		}
		if len(encodeTitles) == 0 {
			err := fmt.Errorf("none of selected title IDs %v found in HandBrake scan", pickedIDs)
			sink.OnStepFailed(state.StepTranscode, err)
			return err
		}
		// Force per-title mode so the encoder loop emits --title N for
		// each pick rather than --main-feature.
		isMovie = false
	default:
		// Movie profiles delegate title selection to HandBrake's own
		// `--main-feature` flag, which reads the IFO's main-feature bit
		// rather than guessing by duration. Series profiles still need
		// our scan-and-filter logic to enumerate episode titles.
		encodeTitles = selectEncodeTitles(titles, prof)
		if !isMovie && len(encodeTitles) == 0 {
			err := errors.New("no titles to encode")
			sink.OnStepFailed(state.StepTranscode, err)
			return err
		}
		if isMovie {
			// Single encode using --main-feature. encodeTitles is set to
			// the scan's longest title only so the duration-floor check
			// below has a number to compare the output bytes to.
			main := longestTitle(titles)
			encodeTitles = []tools.HandBrakeTitle{main}
			if err := validateMovieTitleSelection(main, prof); err != nil {
				sink.OnStepFailed(state.StepTranscode, err)
				return err
			}
			// When the profile opts into extras, fold every other title
			// in the [min_extra_seconds, main_duration × extras_max_ratio]
			// duration band into the encode set. Main feature stays at
			// index 0; extras follow in scan order so the per-title
			// loop encodes them in turn. mainNum unlocks the extras
			// layout in moveOutputs and forces the encoder to use
			// --title N instead of --main-feature for the rest.
			if pipelines.IncludeExtrasFromProfile(prof) {
				extras := selectExtras(titles, main,
					pipelines.MinExtraSecondsFromProfile(prof),
					pipelines.ExtrasMaxRatioFromProfile(prof))
				if len(extras) > 0 {
					encodeTitles = append(encodeTitles, extras...)
					mainNum = main.Number
					sink.OnLog(state.LogLevelInfo, "extras: ripping %d bonus title(s) alongside main feature", len(extras))
				}
			}
		}
	}

	encoder, fellBack := pipelines.SelectHandBrakeEncoder(prof, h.deps.NVENCAvailable)
	if fellBack {
		sink.OnLog(state.LogLevelWarn,
			"NVENC requested but unavailable on host; falling back to %s software encoder", encoder)
	}

	qualityRF := pipelines.IntOption(prof, "quality_rf", 18)
	encoderPreset := pipelines.StringOption(prof, "encoder_preset", "slow")

	transcoded := make([]string, 0, len(encodeTitles))
	for i, t := range encodeTitles {
		titleIdx := i + 1
		out := filepath.Join(result.SpoolPath, fmt.Sprintf("title%02d.%s", t.Number, ext))
		args := []string{
			"--input", source,
			"--output", out,
			"--quality", strconv.Itoa(qualityRF),
			"--encoder", encoder,
			"--encoder-preset", encoderPreset,
			"--all-audio",
			"--markers",
		}
		// --main-feature lets HandBrake pick the title via the IFO bit
		// when we're in pure-movie mode (no extras, no user picks).
		// Extras flow uses --title N explicitly per entry so the
		// encoder doesn't keep re-ripping the main feature.
		if isMovie && mainNum == 0 {
			args = append(args, "--main-feature")
		} else {
			args = append(args, "--title", strconv.Itoa(t.Number))
		}
		if ext == "mkv" {
			args = append(args, "--all-subtitles")
		} else if h.deps.SubsLang != "" {
			args = append(args, "--subtitle-lang-list", h.deps.SubsLang, "--subtitle-forced=auto")
		}
		if ext == "mp4" {
			args = append(args, "--optimize")
		}
		env := map[string]string{
			"HB_TITLE_IDX":      strconv.Itoa(titleIdx),
			"HB_TOTAL_TITLES":   strconv.Itoa(len(encodeTitles)),
			"HB_TITLE_DURATION": strconv.Itoa(t.DurationSeconds),
		}
		stepSink := pipelines.NewStepSink(sink, state.StepTranscode)
		sink.OnLog(state.LogLevelInfo, "HandBrake: encoding title %d → %s", t.Number, filepath.Base(out))
		encStart := time.Now()
		if err := whb.Run(ctx, args, env, result.SpoolPath, stepSink); err != nil {
			sink.OnStepFailed(state.StepTranscode, err)
			return fmt.Errorf("handbrake encode title %d: %w", t.Number, err)
		}
		if err := validateEncodedTitle(out, t.DurationSeconds, h.deps.MinEncodedBytesPerSecond); err != nil {
			sink.OnStepFailed(state.StepTranscode, err)
			return fmt.Errorf("handbrake encode title %d: %w", t.Number, err)
		}
		var encSize int64
		if fi, statErr := os.Stat(out); statErr == nil {
			encSize = fi.Size()
		}
		sink.OnLog(state.LogLevelInfo, "HandBrake: title %d complete, %s in %s",
			t.Number, pipelines.HumanBytes(encSize), pipelines.HumanDuration(time.Since(encStart)))
		transcoded = append(transcoded, out)
	}
	sink.OnStepDone(state.StepTranscode, nil)

	sink.OnStepStart(state.StepMove)
	moved, err := h.moveOutputs(transcoded, encodeTitles, mainNum, disc, prof)
	if err != nil {
		sink.OnStepFailed(state.StepMove, err)
		return fmt.Errorf("move: %w", err)
	}
	for _, p := range moved {
		sink.OnLog(state.LogLevelInfo, "move: → %s", p)
	}
	sink.OnStepDone(state.StepMove, map[string]any{"paths": moved})

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

// runTranscodeMakeMKV handles the post-MakeMKV-rip transcode and move
// steps for the "MakeMKV" (passthrough, no re-encode) and
// "MakeMKV+HandBrake" (re-encode) engines. Both expect the rip step to
// have left one .mkv per ripped title under spoolDir/rip/.
func (h *Handler) runTranscodeMakeMKV(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
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

	var moveSources []string
	switch prof.Engine {
	case "MakeMKV":
		// Passthrough — emit a "skipped" transcode step for the stepper
		// UI and move the rip outputs directly. Mirrors the way audio
		// CDs report the transcode + compress steps as skipped.
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepDone(state.StepTranscode, map[string]any{"skipped": true})
		moveSources = rippedFiles
	case "MakeMKV+HandBrake":
		transcoded, err := h.transcodeMakeMKVRips(ctx, rippedFiles, result.SpoolPath, prof, sink)
		if err != nil {
			return err
		}
		moveSources = transcoded
	default:
		// engineUsesMakeMKV gated us into this function, so an unknown
		// MakeMKV-family engine is a bug, not an input error.
		err := fmt.Errorf("dvdvideo: unexpected engine %q in MakeMKV transcode path", prof.Engine)
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return err
	}

	sink.OnStepStart(state.StepMove)
	moved, err := pipelines.MoveMovieOutputs(h.deps.LibraryRoot, moveSources, disc, prof)
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

	pipelines.RunNotifyStep(ctx, sink)
	return nil
}

// transcodeMakeMKVRips HandBrake-encodes each MakeMKV rip into a
// `out_NN.mkv` file under spoolDir. Used by the MakeMKV+HandBrake
// engine. Returns the encoded paths in the same order as rippedFiles
// so the move step can pair them with the original title selection.
func (h *Handler) transcodeMakeMKVRips(ctx context.Context, rippedFiles []string, spoolDir string, prof *state.Profile, sink pipelines.EventSink) ([]string, error) {
	if h.deps.Tools == nil {
		err := errors.New("dvdvideo: tools registry not configured")
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return nil, err
	}
	hb, ok := h.deps.Tools.Get("handbrake")
	if !ok {
		err := errors.New("dvdvideo: handbrake tool not registered")
		sink.OnStepStart(state.StepTranscode)
		sink.OnStepFailed(state.StepTranscode, err)
		return nil, err
	}
	encoder, fellBack := pipelines.SelectHandBrakeEncoder(prof, h.deps.NVENCAvailable)
	qualityRF := pipelines.IntOption(prof, "quality_rf", 20)
	encoderPreset := pipelines.StringOption(prof, "encoder_preset", "slow")

	sink.OnStepStart(state.StepTranscode)
	if fellBack {
		sink.OnLog(state.LogLevelWarn,
			"NVENC requested but unavailable on host; falling back to %s software encoder", encoder)
	}

	transcoded := make([]string, 0, len(rippedFiles))
	for i, rippedFile := range rippedFiles {
		out := filepath.Join(spoolDir, fmt.Sprintf("out_%02d.mkv", i+1))
		args := []string{
			"--input", rippedFile,
			"--output", out,
			"--format", "av_mkv",
			"--encoder", encoder,
			"--encoder-preset", encoderPreset,
			"--quality", strconv.Itoa(qualityRF),
			"--all-audio",
			"--markers",
			"--all-subtitles",
		}
		sink.OnLog(state.LogLevelInfo, "HandBrake: encoding %s", filepath.Base(rippedFile))
		encStart := time.Now()
		if err := hb.Run(ctx, args, nil, spoolDir, pipelines.NewStepSink(sink, state.StepTranscode)); err != nil {
			sink.OnStepFailed(state.StepTranscode, err)
			return nil, fmt.Errorf("handbrake encode %s: %w", filepath.Base(rippedFile), err)
		}
		var encSize int64
		if fi, statErr := os.Stat(out); statErr == nil {
			encSize = fi.Size()
		}
		sink.OnLog(state.LogLevelInfo, "HandBrake: %s done, %s in %s",
			filepath.Base(rippedFile), pipelines.HumanBytes(encSize),
			pipelines.HumanDuration(time.Since(encStart)))
		transcoded = append(transcoded, out)
	}
	sink.OnStepDone(state.StepTranscode, nil)
	return transcoded, nil
}

// stringFromNotes extracts a string-valued key from a RipResult.Notes
// bag, tolerating an absent or wrong-typed entry.
func stringFromNotes(notes map[string]any, key string) string {
	if notes == nil {
		return ""
	}
	if v, ok := notes[key].(string); ok {
		return v
	}
	return ""
}

// minEncodedBytesPerSecond is our lower-bound on the bytes-per-second
// of a HandBrake x264 quality-20 encode. Real movies hover around
// 200 KB/s (≈ 1.5 Mbps); we use 93 750 (≈ 750 kbps) so the check
// rejects truncated encodes (HandBrake exiting cleanly mid-stream)
// without false-positives on extremely flat content.
const minEncodedBytesPerSecond = 93_750

// minMovieFeatureSeconds is the default floor (20 min) below which we
// refuse to start a movie-profile encode. --main-feature handles the
// happy path, but a disc with no main-feature bit set in the IFO (or
// an incomplete dvdbackup mirror) can still leave the scan's longest
// title at a few minutes — see the Jackass: The Movie regression that
// shipped a 7-min sketch in v0.2.3. Failing here is preferable to
// producing a junk file that passes the downstream byte-size check
// (which only compares against the *encoded* duration, not the
// expected feature duration). Override per profile via
// `min_feature_seconds`; set to 0 to disable.
const minMovieFeatureSeconds = 1200

// validateEncodedTitle errors out when the encoded file is missing, is
// empty, or is below the expected lower-bound for its source duration.
// HandBrakeCLI exits 0 in several end-of-stream failure modes, so we
// can't rely on the exit code alone to know whether the title encoded
// in full.
//
// minBytesPerSecond overrides the package default; 0 → default, < 0 →
// disable the size check (only the empty-file branch applies).
func validateEncodedTitle(path string, durationSeconds, minBytesPerSecond int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("validate encode: %w", err)
	}
	if fi.Size() == 0 {
		return fmt.Errorf("validate encode: empty output at %s", path)
	}
	if durationSeconds <= 0 || minBytesPerSecond < 0 {
		return nil
	}
	if minBytesPerSecond == 0 {
		minBytesPerSecond = minEncodedBytesPerSecond
	}
	minSize := int64(durationSeconds) * int64(minBytesPerSecond)
	if fi.Size() < minSize {
		return fmt.Errorf(
			"validate encode: output %s is %d bytes, expected at least %d for a %ds title (likely truncated)",
			path, fi.Size(), minSize, durationSeconds,
		)
	}
	return nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "dvd", discID)
}

// ScanTitles implements pipelines.TitleScanner: enumerate titles via
// HandBrake direct-against-device (no dvdbackup mirror needed — that
// would add 5+ minutes for a feature that just wants the title list).
// Brief (~10s), drive-bound, doesn't eject.
func (h *Handler) ScanTitles(ctx context.Context, drv *state.Drive, _ *state.Disc, _ *state.Profile, sink pipelines.EventSink) ([]pipelines.TitleInfo, error) {
	sink.OnStepStart(state.StepIdentify)
	defer sink.OnStepDone(state.StepIdentify, nil)

	if h.deps.HandBrakeScanner == nil {
		err := errors.New("dvdvideo: HandBrakeScanner not configured")
		sink.OnStepFailed(state.StepIdentify, err)
		return nil, err
	}
	sink.OnLog(state.LogLevelInfo, "HandBrake: scanning %s for titles", drv.DevPath)
	titles, err := h.deps.HandBrakeScanner.Scan(ctx, drv.DevPath)
	if err != nil {
		sink.OnStepFailed(state.StepIdentify, err)
		return nil, fmt.Errorf("handbrake scan: %w", err)
	}
	out := make([]pipelines.TitleInfo, 0, len(titles))
	for _, t := range titles {
		out = append(out, pipelines.TitleInfo{
			ID:          t.Number,
			DurationSec: t.DurationSeconds,
		})
	}
	return out, nil
}

// selectEncodeTitles picks which titles to encode for **series**
// profiles. Movie profiles bypass this entirely and let HandBrake's
// --main-feature pick from the IFO.
//
// Series (MKV) profile: every title >= options.min_title_seconds
// (default 300).
func selectEncodeTitles(titles []tools.HandBrakeTitle, prof *state.Profile) []tools.HandBrakeTitle {
	if IsMovieProfile(prof) {
		// Movie path is driven by --main-feature; the caller still needs
		// *something* to iterate to drive its outer loop once, so it
		// substitutes longestTitle(titles) directly. Returning nil here
		// makes the no-titles guard in Run fire only for series, which is
		// the correct semantics.
		return nil
	}

	minSec := pipelines.IntOption(prof, "min_title_seconds", 300)
	var out []tools.HandBrakeTitle
	for _, t := range titles {
		if t.DurationSeconds >= minSec {
			out = append(out, t)
		}
	}
	return out
}

// IsMovieProfile decides whether the DVD-Movie title-selection path
// (HandBrake --main-feature) applies, or whether the DVD-Series path
// (enumerate-and-floor) applies. Resolution order:
//
//  1. profile option "dvd_selection_mode" — "main_feature" / "per_title"
//  2. legacy fallback: lowercased Format == "mp4" means movie
//
// The legacy fallback exists for DBs that haven't yet picked up the
// 003_dvd_default_mkv migration (e.g. a freshly-restored backup from
// before that migration shipped).
func IsMovieProfile(prof *state.Profile) bool {
	if mode, ok := prof.Options["dvd_selection_mode"].(string); ok {
		switch mode {
		case "main_feature":
			return true
		case "per_title":
			return false
		}
	}
	return strings.ToLower(prof.Format) == "mp4"
}

// longestTitle returns the title with the largest DurationSeconds, or
// a zero HandBrakeTitle when titles is empty. Used as the validation
// reference (expected duration) for movie-profile encodes that
// HandBrake selected via --main-feature.
func longestTitle(titles []tools.HandBrakeTitle) tools.HandBrakeTitle {
	if len(titles) == 0 {
		return tools.HandBrakeTitle{}
	}
	best := titles[0]
	for _, t := range titles[1:] {
		if t.DurationSeconds > best.DurationSeconds {
			best = t
		}
	}
	return best
}

// validateMovieTitleSelection rejects movie-profile encodes when the
// longest scanned title is below the configured feature floor. The
// longest scanned title is also what `validateEncodedTitle` later
// compares the output bytes against, so a too-short pick here means
// the byte-size check would also be using a too-short reference and
// would pass on a junk encode. Returns nil when the profile sets
// `min_feature_seconds=0`.
func validateMovieTitleSelection(picked tools.HandBrakeTitle, prof *state.Profile) error {
	floor := pipelines.IntOption(prof, "min_feature_seconds", minMovieFeatureSeconds)
	if floor <= 0 {
		return nil
	}
	if picked.DurationSeconds < floor {
		return fmt.Errorf(
			"longest scanned title is %ds, below movie feature floor of %ds — disc likely has no play-all title or the mirror is incomplete; set profile option min_feature_seconds=0 to override",
			picked.DurationSeconds, floor,
		)
	}
	return nil
}

// logScannedTitles emits one INFO line per title HandBrake's scan
// returned. Cheap, but invaluable when a future "wrong title got
// picked" regression needs to be diagnosed from `docker logs`.
func logScannedTitles(discID string, titles []tools.HandBrakeTitle) {
	for _, t := range titles {
		slog.Info("scanned title",
			"disc", discID, "title", t.Number, "duration_sec", t.DurationSeconds)
	}
}

// warnOnRuntimeMismatch logs a WARN when the longest scanned title
// diverges by more than 50 % from the disc's TMDB-reported runtime.
// Doesn't fail — DVDs legitimately differ from theatrical runtimes
// (director's cuts, regional edits) — but a 5× gap is a red flag
// that the rip captured the wrong content (e.g. an outtakes reel
// instead of the feature).
func warnOnRuntimeMismatch(disc *state.Disc, titles []tools.HandBrakeTitle) {
	if disc == nil || disc.RuntimeSeconds <= 0 {
		return
	}
	longest := longestTitle(titles)
	if longest.DurationSeconds <= 0 {
		return
	}
	expected := float64(disc.RuntimeSeconds)
	got := float64(longest.DurationSeconds)
	ratio := got / expected
	if ratio < 0.5 || ratio > 1.5 {
		slog.Warn("duration mismatch",
			"disc", disc.ID,
			"expected_sec", disc.RuntimeSeconds,
			"scanned_longest_sec", longest.DurationSeconds,
			"ratio", fmt.Sprintf("%.2f", ratio),
		)
	}
}

func (h *Handler) moveOutputs(transcoded []string, encodeTitles []tools.HandBrakeTitle,
	mainNum int, disc *state.Disc, prof *state.Profile) ([]string, error) {
	// Picker-recorded season overrides the per-profile default — TV
	// box-set users pick the season per disc, not per profile.
	season := pipelines.IntOption(prof, "season", 1)
	if s := pipelines.SelectedSeasonFromDisc(disc); s > 0 {
		season = s
	}
	epMap := pipelines.SelectedEpisodeMapFromDisc(disc)

	// In the extras flow (mainNum > 0) we need the rendered main
	// path first to derive `<mainDir>/<bucket-folder>/` for the bonus
	// files. Pre-resolve it here so the first extra in the loop knows
	// where to land. extraCounters counts the bonus title order
	// per-bucket so each Jellyfin/Emby subfolder reads 01, 02, … with
	// no gaps when the disc mixes trailers and featurettes.
	var mainDir string
	extraCounters := map[string]int{}
	if mainNum > 0 {
		mainFields := pipelines.OutputFields{
			Title: disc.Title,
			Year:  disc.Year,
			Show:  disc.Title,
		}
		mainRel, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, mainFields)
		if err != nil {
			return nil, fmt.Errorf("render main template: %w", err)
		}
		mainDir = filepath.Dir(mainRel)
	}

	var moved []string
	for episodeIdx, src := range transcoded {
		// We want the file extension that came out of HandBrake, not the
		// profile's extension — they always agree today, but stay
		// defensive in case a profile flips formats mid-job.
		ext := strings.TrimPrefix(filepath.Ext(src), ".")
		isExtra := mainNum > 0 && episodeIdx < len(encodeTitles) &&
			encodeTitles[episodeIdx].Number != mainNum

		var rel string
		if isExtra {
			bucket := pipelines.ClassifyExtraByDuration(encodeTitles[episodeIdx].DurationSeconds)
			extraCounters[bucket.Folder]++
			rel = filepath.Join(mainDir, bucket.Folder,
				fmt.Sprintf("%s %02d.%s", bucket.Label, extraCounters[bucket.Folder], ext))
		} else {
			fields := pipelines.OutputFields{
				Title:         disc.Title,
				Year:          disc.Year,
				Show:          disc.Title,
				Season:        season,
				EpisodeNumber: episodeIdx + 1,
			}
			// When the user mapped this title to a TMDB episode, the
			// picker's choice wins over the title-index fallback.
			if episodeIdx < len(encodeTitles) {
				if ea, ok := epMap[encodeTitles[episodeIdx].Number]; ok {
					if ea.Episode > 0 {
						fields.EpisodeNumber = ea.Episode
					}
					fields.EpisodeTitle = ea.EpisodeTitle
				}
			}
			r, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, fields)
			if err != nil {
				return moved, fmt.Errorf("render template: %w", err)
			}
			rel = r
			if filepath.Ext(rel) == "" {
				rel += "." + ext
			}
		}
		dst := filepath.Join(h.deps.LibraryRoot, rel)
		if err := pipelines.AtomicMove(src, dst); err != nil {
			return moved, err
		}
		moved = append(moved, dst)
	}
	return moved, nil
}

// selectExtras picks every title in the duration band
// [minSec, mainDur × maxRatio] that isn't the main title itself.
// Returns the matches in the order they appear in titles (scan
// order) so deterministic output ordering survives upstream
// changes in HandBrake's output.
func selectExtras(titles []tools.HandBrakeTitle, main tools.HandBrakeTitle, minSec int, maxRatio float64) []tools.HandBrakeTitle {
	maxSec := int(float64(main.DurationSeconds) * maxRatio)
	out := make([]tools.HandBrakeTitle, 0)
	for _, t := range titles {
		if t.Number == main.Number {
			continue
		}
		if t.DurationSeconds < minSec || t.DurationSeconds > maxSec {
			continue
		}
		out = append(out, t)
	}
	return out
}

// mergeMetadataField reads the current blob, sets one top-level key to
// value, and persists the merged JSON. Failures are non-fatal — the
// rip continues regardless. Used to attach the HandBrake scan title
// list onto disc.metadata_json so the pane's Files tab can render the
// source-disc inventory after the rip completes too.
func mergeMetadataField(ctx context.Context, store MetadataStore, discID, existing string, key string, value any) error {
	merged := map[string]any{}
	if existing != "" && existing != "{}" {
		_ = json.Unmarshal([]byte(existing), &merged)
	}
	merged[key] = value
	body, err := json.Marshal(merged)
	if err != nil {
		return err
	}
	return store.UpdateDiscMetadataBlob(ctx, discID, string(body))
}
