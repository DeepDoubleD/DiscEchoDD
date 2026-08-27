// Package uhd implements pipelines.Handler for Ultra HD Blu-ray discs.
//
// Pipeline shape (6 active steps; transcode + compress skipped):
//
//	detect → identify → rip (MakeMKV remux) → move → notify → eject
//
// Identify performs a key-file precheck before any TMDB lookup: if
// AACS2KeyDB does not exist, returns ErrAACS2KeyMissing so the
// orchestrator can surface a clear "missing keys" error before any
// disc read happens.
package uhd

import (
	"context"
	"errors"
	"fmt"
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

// ErrAACS2KeyMissing is returned by Identify when the configured
// KEYDB.cfg path is unset or doesn't exist. Wrapped error messages
// include the path so the user knows where to drop the file.
var ErrAACS2KeyMissing = errors.New("uhd: AACS2 key file missing")

// aacs2Guidance is appended to ErrAACS2KeyMissing messages. UHD failures
// surface as the job's error_message (UHD discs pass classify; this is the
// gate), so this is the one place to spell out the full UHD prerequisites
// the user is likely missing.
const aacs2Guidance = "UHD ripping requires: an AACS2 KEYDB.cfg key file (DiscEcho does not ship one), MakeMKV configured with a valid key (Settings → Integrations → MakeMKV), and a UHD-friendly Blu-ray drive with LibreDrive-compatible firmware."

// MakeMKVScanner / MakeMKVRipper are the slice of tools.MakeMKV used
// at scan-time and rip-time respectively. Mirrors the bdmv package.
// sink receives MakeMKV's MSG: lines as info logs during the
// multi-minute info enumeration; tests can pass tools.NopSink{}.
type MakeMKVScanner interface {
	Scan(ctx context.Context, devPath string, sink tools.Sink) ([]tools.MakeMKVTitle, error)
}
type MakeMKVRipper interface {
	Rip(ctx context.Context, devPath string, titleID int, outDir string, sink tools.Sink) error
}

// Deps bundles the handler's dependencies for mock injection.
type Deps struct {
	Prober   identify.DVDProber
	BDProber identify.BDProber // optional: disc-library metadata name, preferred over the volume label when present
	TMDB     identify.TMDBClient
	// MKVSubs pulls text-based subtitle tracks out of the moved output
	// as sidecar files when the profile's extract_text_subtitles option
	// is set. nil disables the feature regardless of profile options.
	MKVSubs             pipelines.MKVSubtitleTool
	MakeMKVScanner      MakeMKVScanner
	MakeMKVRipper       MakeMKVRipper
	Tools               *tools.Registry // looked up: apprise, eject
	LibraryRoot         string
	LibraryTV           string // used instead of LibraryRoot when the profile's content_type is "tv"
	LibraryKidsCartoons string // overrides LibraryRoot/LibraryTV when the disc-level category is "kids_cartoons"
	LibraryAnime        string // overrides LibraryRoot/LibraryTV when the disc-level category is "anime"
	WorkRoot            string
	LibraryProbe        func(string) error
	URLsForTrigger      func(ctx context.Context, trigger string) []string
	SubsLang            string
	AACS2KeyDB          string // path to KEYDB.cfg; checked at Identify-time
	// ShouldEject gates the rip-end eject step; nil = always eject.
	ShouldEject func(ctx context.Context) bool
}

// Handler implements pipelines.Handler for UHD.
type Handler struct{ deps Deps }

// New constructs the handler.
func New(d Deps) *Handler {
	if d.LibraryProbe == nil {
		d.LibraryProbe = pipelines.ProbeWritable
	}
	return &Handler{deps: d}
}

// DiscType returns UHD.
func (h *Handler) DiscType() state.DiscType { return state.DiscTypeUHD }

// Identify runs the AACS2 key-file precheck, then volume-label probe,
// then TMDB lookup. Errors:
//
//   - ErrAACS2KeyMissing if AACS2KeyDB is unset or doesn't exist
//   - ErrNoCandidates if the volume label doesn't normalise or TMDB returns 0
func (h *Handler) Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error) {
	if h.deps.AACS2KeyDB == "" {
		return nil, nil, fmt.Errorf("%w: KEYDB.cfg path not configured. %s", ErrAACS2KeyMissing, aacs2Guidance)
	}
	if _, err := os.Stat(h.deps.AACS2KeyDB); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil, fmt.Errorf("%w: no file at %s. %s", ErrAACS2KeyMissing, h.deps.AACS2KeyDB, aacs2Guidance)
		}
		return nil, nil, fmt.Errorf("uhd: stat key file: %w", err)
	}
	if h.deps.Prober == nil {
		return nil, nil, errors.New("uhd: prober not configured")
	}
	if h.deps.TMDB == nil {
		return nil, nil, errors.New("uhd: TMDB client not configured")
	}

	info, err := h.deps.Prober.Probe(ctx, drv.DevPath)
	if err != nil {
		return nil, nil, fmt.Errorf("uhd probe: %w", err)
	}
	disc := &state.Disc{Type: state.DiscTypeUHD, DriveID: drv.ID}

	label := identify.BDSearchLabel(ctx, h.deps.BDProber, drv.DevPath, info.VolumeLabel)
	q := identify.NormaliseDVDLabel(label)
	if q == "" {
		return disc, nil, pipelines.ErrNoCandidates
	}
	// "movie": same reasoning as BDMV -- a single UHD disc is
	// essentially never a full TV season release.
	cands, err := h.deps.TMDB.SearchBoth(ctx, q, "movie")
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

// Plan returns the 6-active-step plan; transcode + compress skipped.
// Used by the monolithic Run fallback. Split-path callers use PlanRip
// + PlanTranscode.
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

// PlanRip — full 8-step canonical list with the transcode-half marked
// Skip. Active rip-half steps: detect, identify, rip, eject.
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

// PlanTranscode — UHD has no transcode or compress (MakeMKV output IS
// the artifact, HDR/DV/lossless preserved), so both are marked Skip.
// Active transcode-half steps: move, notify.
func (h *Handler) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		skip := sid == state.StepTranscode || sid == state.StepCompress
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skip})
	}
	return out
}

// Run is the monolithic fallback path. Allocates a tmpdir as spool,
// runs RunRip + RunTranscode, cleans up.
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

// RunRip executes the drive-bound half: detect, identify, MakeMKV
// scan + decrypt → spoolDir/rip/*.mkv, eject. When disc.metadata_json
// carries selected_title_ids (user came through the picker), rips
// exactly those titles; otherwise falls back to pickLongestTitle
// (legacy single-title behaviour). Drive freed on return.
func (h *Handler) RunRip(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, spoolDir string, sink pipelines.EventSink) (pipelines.RipResult, error) {
	sink.OnStepStart(state.StepDetect)
	sink.OnStepDone(state.StepDetect, nil)
	sink.OnStepStart(state.StepIdentify)
	sink.OnStepDone(state.StepIdentify, nil)

	sink.OnStepStart(state.StepRip)
	if err := h.deps.LibraryProbe(pipelines.LibraryRootFor(h.deps.LibraryRoot, h.deps.LibraryTV, h.deps.LibraryKidsCartoons, h.deps.LibraryAnime, disc, prof)); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("library probe: %w", err)
	}
	if h.deps.MakeMKVScanner == nil || h.deps.MakeMKVRipper == nil {
		err := errors.New("uhd: MakeMKV not configured")
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	ripStepSink := pipelines.NewStepSink(sink, state.StepRip)
	ripStepSink.SubStep("scan")
	sink.OnLog(state.LogLevelInfo, "MakeMKV: scanning %s (UHD)", drv.DevPath)
	titles, err := h.deps.MakeMKVScanner.Scan(ctx, drv.DevPath, ripStepSink)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, fmt.Errorf("makemkv scan: %w", err)
	}
	ripStepSink.SubStep("read_raw_data")
	picked, err := selectTitles(titles, prof, disc)
	if err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	if len(picked) > 1 {
		sink.OnLog(state.LogLevelInfo, "MakeMKV: scan complete, ripping %d titles", len(picked))
	} else {
		sink.OnLog(state.LogLevelInfo, "MakeMKV: scan complete, picked title %d (%s)",
			picked[0].ID, pipelines.HumanDuration(time.Duration(picked[0].DurationSec)*time.Second))
	}
	ripDir := filepath.Join(spoolDir, "rip")
	if err := os.MkdirAll(ripDir, 0o755); err != nil {
		sink.OnStepFailed(state.StepRip, err)
		return pipelines.RipResult{}, err
	}
	var neededBytes int64
	for _, t := range picked {
		neededBytes += t.SizeBytes
	}
	if err := pipelines.CheckSpoolSpace(ripDir, neededBytes); err != nil {
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
	sink.OnStepDone(state.StepRip, map[string]any{"title_id": picked[0].ID, "duration_sec": picked[0].DurationSec, "rip_file_count": len(rippedFiles)})

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

// RunTranscode executes the compute-bound half. UHD has no transcode
// or compress steps — the MakeMKV remux IS the deliverable — so the
// pipeline is just move (one file per ripped title) + notify.
func (h *Handler) RunTranscode(ctx context.Context, result pipelines.RipResult, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	rippedFiles, err := listMKVIn(filepath.Join(result.SpoolPath, "rip"))
	if err != nil {
		sink.OnStepStart(state.StepMove)
		sink.OnStepFailed(state.StepMove, err)
		return err
	}
	if len(rippedFiles) == 0 {
		err := fmt.Errorf("no .mkv rip output under %s/rip", result.SpoolPath)
		sink.OnStepStart(state.StepMove)
		sink.OnStepFailed(state.StepMove, err)
		return err
	}

	sink.OnStepStart(state.StepMove)
	moved, err := h.moveMultiTitle(rippedFiles, disc, prof)
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

// selectTitles returns the user-picked titles when present; falls
// back to a single-element slice with the longest qualifying title.
func selectTitles(titles []tools.MakeMKVTitle, prof *state.Profile, disc *state.Disc) ([]tools.MakeMKVTitle, error) {
	if ids := pipelines.SelectedTitleIDsFromDisc(disc); len(ids) > 0 {
		byID := make(map[int]tools.MakeMKVTitle, len(titles))
		for _, t := range titles {
			byID[t.ID] = t
		}
		picked := make([]tools.MakeMKVTitle, 0, len(ids))
		for _, id := range ids {
			if t, ok := byID[id]; ok {
				picked = append(picked, t)
			}
		}
		if len(picked) == 0 {
			return nil, fmt.Errorf("none of selected title IDs %v found in MakeMKV scan", ids)
		}
		return picked, nil
	}
	t, err := pickLongestTitle(titles, prof)
	if err != nil {
		return nil, err
	}
	return []tools.MakeMKVTitle{t}, nil
}

// listMKVIn lists every .mkv file at the top level of dir, sorted
// lexically so multi-title rips have predictable output naming.
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

// moveMultiTitle atomic-moves each ripped MakeMKV output into the
// library. When the picker recorded per-title TMDB episode mappings
// (selected_title_episodes), those drive EpisodeNumber + EpisodeTitle
// in the rendered template. Otherwise EpisodeNumber falls back to
// the 1-based title index and EpisodeTitle is empty. Colliding
// rendered paths get a `-titleNN` suffix to disambiguate.
func (h *Handler) moveMultiTitle(srcs []string, disc *state.Disc, prof *state.Profile) ([]string, error) {
	titleIDs := pipelines.SelectedTitleIDsFromDisc(disc)
	epMap := pipelines.SelectedEpisodeMapFromDisc(disc)
	season := pipelines.SelectedSeasonFromDisc(disc)

	rendered := make([]string, len(srcs))
	for i := range srcs {
		fields := pipelines.OutputFields{
			Title:         disc.Title,
			Year:          disc.Year,
			Show:          disc.Title,
			Season:        season,
			EpisodeNumber: i + 1,
		}
		if i < len(titleIDs) {
			if ea, ok := epMap[titleIDs[i]]; ok {
				if ea.Episode > 0 {
					fields.EpisodeNumber = ea.Episode
				}
				fields.EpisodeTitle = ea.EpisodeTitle
			}
		}
		rel, err := pipelines.RenderOutputPath(prof.OutputPathTemplate, fields)
		if err != nil {
			return nil, fmt.Errorf("render template: %w", err)
		}
		rendered[i] = rel
	}
	counts := map[string]int{}
	for _, r := range rendered {
		counts[r]++
	}
	for i, r := range rendered {
		if counts[r] > 1 {
			ext := filepath.Ext(r)
			rendered[i] = strings.TrimSuffix(r, ext) + fmt.Sprintf("-title%02d", i+1) + ext
		}
	}
	root := pipelines.LibraryRootFor(h.deps.LibraryRoot, h.deps.LibraryTV, h.deps.LibraryKidsCartoons, h.deps.LibraryAnime, disc, prof)
	moved := make([]string, 0, len(srcs))
	for i, src := range srcs {
		dst := filepath.Join(root, rendered[i])
		if err := pipelines.AtomicMove(src, dst); err != nil {
			return moved, err
		}
		moved = append(moved, dst)
	}
	return moved, nil
}

func (h *Handler) createWorkDir(discID string) (string, error) {
	return pipelines.CreateWorkDir(h.deps.WorkRoot, "uhd", discID)
}

// ScanTitles implements pipelines.TitleScanner — same flow as BDMV.
// Brief, drive-bound, doesn't eject.
func (h *Handler) ScanTitles(ctx context.Context, drv *state.Drive, _ *state.Disc, _ *state.Profile, sink pipelines.EventSink) ([]pipelines.TitleInfo, error) {
	sink.OnStepStart(state.StepIdentify)
	defer sink.OnStepDone(state.StepIdentify, nil)

	if h.deps.MakeMKVScanner == nil {
		err := errors.New("uhd: MakeMKV not configured")
		sink.OnStepFailed(state.StepIdentify, err)
		return nil, err
	}
	sink.OnLog(state.LogLevelInfo, "MakeMKV: scanning %s for titles (UHD)", drv.DevPath)
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

func pickLongestTitle(titles []tools.MakeMKVTitle, prof *state.Profile) (tools.MakeMKVTitle, error) {
	minSec := 0
	if prof != nil && prof.Options != nil {
		switch n := prof.Options["min_title_seconds"].(type) {
		case int:
			minSec = n
		case float64:
			minSec = int(n)
		}
	}
	var best tools.MakeMKVTitle
	found := false
	for _, t := range titles {
		if t.DurationSec < minSec {
			continue
		}
		if !found || t.DurationSec > best.DurationSec {
			best = t
			found = true
		}
	}
	if !found {
		return tools.MakeMKVTitle{}, fmt.Errorf("no title with duration >= %ds", minSec)
	}
	return best, nil
}
