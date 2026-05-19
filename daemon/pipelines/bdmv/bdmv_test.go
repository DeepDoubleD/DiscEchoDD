package bdmv_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/identify"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/bdmv"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines/testutil"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// Compile-time assertion that BDMV satisfies SplittableHandler — the
// orchestrator's type-assert depends on it, so a future signature
// drift here is caught by the build, not at runtime.
var _ pipelines.SplittableHandler = (*bdmv.Handler)(nil)

// fakeProber returns a fixed DVDInfo (BDMV reuses the existing DVD
// prober for volume-label reading).
type fakeProber struct {
	info *identify.DVDInfo
	err  error
}

func (f *fakeProber) Probe(_ context.Context, _ string) (*identify.DVDInfo, error) {
	return f.info, f.err
}

// fakeTMDB returns canned candidates.
type fakeTMDB struct {
	cands []state.Candidate
	err   error
}

func (f *fakeTMDB) SearchMovie(_ context.Context, _ string) ([]state.Candidate, error) {
	return f.cands, f.err
}
func (f *fakeTMDB) SearchTV(_ context.Context, _ string) ([]state.Candidate, error) {
	return f.cands, f.err
}
func (f *fakeTMDB) SearchBoth(_ context.Context, _ string) ([]state.Candidate, error) {
	return f.cands, f.err
}
func (f *fakeTMDB) MovieRuntime(_ context.Context, _ int) (int, error) { return 0, nil }
func (f *fakeTMDB) SeasonEpisodes(_ context.Context, _, _ int) ([]identify.EpisodeInfo, error) {
	return nil, nil
}
func (f *fakeTMDB) MovieDetails(_ context.Context, _ int) (identify.DiscMetadata, error) {
	return identify.DiscMetadata{}, nil
}
func (f *fakeTMDB) TVDetails(_ context.Context, _ int) (identify.DiscMetadata, error) {
	return identify.DiscMetadata{}, nil
}
func (f *fakeTMDB) Reconfigure(_ identify.TMDBConfig) {}

// fakeMakeMKV satisfies both bdmv.MakeMKVScanner and bdmv.MakeMKVRipper.
type fakeMakeMKV struct {
	scanTitles []tools.MakeMKVTitle
	scanErr    error
	ripErr     error
	// stubName overrides the per-title naming for the single-title
	// tests that pre-date multi-title rip. When empty, the fake writes
	// `title_t<NN>.mkv` with NN derived from the requested title id
	// so multi-title rips produce one file per Rip call.
	stubName string
	// stubBytes lets tests vary the per-Rip file size so extras-mode
	// indexOfLargest picks deterministically and the size-ratio
	// classifier in moveMultiTitle falls into the expected
	// Jellyfin/Emby bucket. Keyed by title id; titles without an entry
	// get a 4-byte stub.
	stubBytes map[int]int
}

func (f *fakeMakeMKV) Scan(_ context.Context, _ string, _ tools.Sink) ([]tools.MakeMKVTitle, error) {
	return f.scanTitles, f.scanErr
}

func (f *fakeMakeMKV) Rip(_ context.Context, _ string, titleID int, outDir string, _ tools.Sink) error {
	if f.ripErr != nil {
		return f.ripErr
	}
	name := f.stubName
	if name == "" {
		name = fmt.Sprintf("title_t%02d.mkv", titleID)
	}
	size := 4
	if f.stubBytes != nil {
		if n, ok := f.stubBytes[titleID]; ok {
			size = n
		}
	}
	return os.WriteFile(filepath.Join(outDir, name), make([]byte, size), 0o644)
}

// fakeHandBrake satisfies tools.Tool. On Run: writes a stub file at
// the --output arg and records the argv for assertion in tests. The
// stub output size mirrors the --input file size when present so
// downstream code that uses transcoded file sizes (e.g. moveOutputs
// extras-bucket size-ratio classifier) sees a realistic ratio. Empty
// or missing --input falls back to 7 bytes ("ENCODED").
type fakeHandBrake struct {
	encodeErr error
	calls     [][]string
}

func (f *fakeHandBrake) Name() string { return "handbrake" }
func (f *fakeHandBrake) Run(_ context.Context, args []string, _ map[string]string,
	_ string, _ tools.Sink) error {
	f.calls = append(f.calls, append([]string(nil), args...))
	if f.encodeErr != nil {
		return f.encodeErr
	}
	var inputPath, outputPath string
	for i, a := range args {
		switch a {
		case "--input":
			if i+1 < len(args) {
				inputPath = args[i+1]
			}
		case "--output":
			if i+1 < len(args) {
				outputPath = args[i+1]
			}
		}
	}
	if outputPath == "" {
		return nil
	}
	size := int64(len("ENCODED"))
	if inputPath != "" {
		if fi, err := os.Stat(inputPath); err == nil && fi.Size() > 0 {
			size = fi.Size()
		}
	}
	return os.WriteFile(outputPath, make([]byte, size), 0o644)
}

func newRegistry() (*tools.Registry, *tools.MockTool, *tools.MockTool, *fakeHandBrake) {
	hb := &fakeHandBrake{}
	apprise := tools.NewMockTool("apprise", nil)
	eject := tools.NewMockTool("eject", nil)
	r := tools.NewRegistry()
	r.Register(hb)
	r.Register(apprise)
	r.Register(eject)
	return r, apprise, eject, hb
}

func TestBDMVHandler_DiscType(t *testing.T) {
	h := bdmv.New(bdmv.Deps{})
	if got := h.DiscType(); got != state.DiscTypeBDMV {
		t.Errorf("disc type = %s, want BDMV", got)
	}
}

func TestBDMVHandler_Identify_HappyPath(t *testing.T) {
	h := bdmv.New(bdmv.Deps{
		Prober: &fakeProber{info: &identify.DVDInfo{VolumeLabel: "ARRIVAL"}},
		TMDB: &fakeTMDB{cands: []state.Candidate{
			{Source: "TMDB", Title: "Arrival", Year: 2016, Confidence: 95, TMDBID: 329865, MediaType: "movie"},
		}},
	})
	disc, cands, err := h.Identify(context.Background(), &state.Drive{ID: "d1", DevPath: "/dev/sr0"})
	if err != nil {
		t.Fatal(err)
	}
	if disc.Title != "Arrival" || disc.Year != 2016 {
		t.Errorf("unexpected disc: %+v", disc)
	}
	if len(cands) != 1 {
		t.Errorf("want 1 candidate, got %d", len(cands))
	}
	if disc.Type != state.DiscTypeBDMV {
		t.Errorf("disc.Type = %s, want BDMV", disc.Type)
	}
}

func TestBDMVHandler_Identify_NoCandidates(t *testing.T) {
	h := bdmv.New(bdmv.Deps{
		// "BLURAY" normalises to "" via NormaliseDVDLabel — the existing
		// junk filter rejects too-short / generic labels.
		Prober: &fakeProber{info: &identify.DVDInfo{VolumeLabel: "BLURAY"}},
		TMDB:   &fakeTMDB{cands: nil},
	})
	_, _, err := h.Identify(context.Background(), &state.Drive{ID: "d1"})
	if !errors.Is(err, pipelines.ErrNoCandidates) {
		t.Errorf("want ErrNoCandidates, got %v", err)
	}
}

func TestBDMVHandler_Plan_CompressSkipped(t *testing.T) {
	h := bdmv.New(bdmv.Deps{})
	plan := h.Plan(&state.Disc{}, &state.Profile{})
	if len(plan) != 8 {
		t.Fatalf("want 8 steps, got %d", len(plan))
	}
	for _, sp := range plan {
		if sp.ID == state.StepCompress && !sp.Skip {
			t.Errorf("compress should be skipped")
		}
		if sp.ID == state.StepTranscode && sp.Skip {
			t.Errorf("transcode should NOT be skipped (BDMV transcodes via HandBrake)")
		}
	}
}

func TestBDMVHandler_PlanRip_MarksTranscodeHalfSkipped(t *testing.T) {
	h := bdmv.New(bdmv.Deps{})
	plan := h.PlanRip(&state.Disc{}, &state.Profile{})
	if len(plan) != 8 {
		t.Fatalf("PlanRip: want 8 step plans (canonical), got %d", len(plan))
	}
	wantSkipped := map[state.StepID]bool{
		state.StepTranscode: true, state.StepCompress: true,
		state.StepMove: true, state.StepNotify: true,
	}
	for _, sp := range plan {
		if want, got := wantSkipped[sp.ID], sp.Skip; want != got {
			t.Errorf("PlanRip step %s: skip=%v, want %v", sp.ID, got, want)
		}
	}
}

func TestBDMVHandler_PlanTranscode_CompressSkipped(t *testing.T) {
	h := bdmv.New(bdmv.Deps{})
	plan := h.PlanTranscode(&state.Disc{}, &state.Profile{})
	if len(plan) != 4 {
		t.Fatalf("PlanTranscode: want 4 step plans, got %d", len(plan))
	}
	for _, sp := range plan {
		if sp.ID == state.StepCompress && !sp.Skip {
			t.Errorf("PlanTranscode: compress should be skipped (BDMV has no compress step)")
		}
		if sp.ID != state.StepCompress && sp.Skip {
			t.Errorf("PlanTranscode: %s should NOT be skipped", sp.ID)
		}
	}
}

func TestBDMVHandler_RunRip_LeavesRipInSpool(t *testing.T) {
	libRoot := t.TempDir()
	spoolDir := t.TempDir()

	reg, _, _, _ := newRegistry()
	h := bdmv.New(bdmv.Deps{
		MakeMKVScanner: &fakeMakeMKV{scanTitles: []tools.MakeMKVTitle{
			{ID: 1, DurationSec: 7000, SourceFile: "00800.mpls"},
		}},
		MakeMKVRipper: &fakeMakeMKV{stubName: "title_t01.mkv"},
		Tools:         reg,
		LibraryRoot:   libRoot,
		WorkRoot:      t.TempDir(),
	})
	prof := &state.Profile{
		ID: "p-bd", DiscType: state.DiscTypeBDMV, Name: "BD",
		OutputPathTemplate: "{{.Title}}/{{.Title}}.mkv",
		Options:            map[string]any{"min_title_seconds": float64(3600)},
	}
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeBDMV, Title: "Arrival", Year: 2016}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	result, err := h.RunRip(context.Background(), drv, disc, prof, spoolDir, sink)
	if err != nil {
		t.Fatal(err)
	}
	if result.SpoolPath != spoolDir {
		t.Errorf("SpoolPath = %s, want %s", result.SpoolPath, spoolDir)
	}
	// MakeMKV output must live in spool/rip/<file>.mkv for the transcode
	// worker to find on a fresh boot.
	ripped := filepath.Join(spoolDir, "rip", "title_t01.mkv")
	if _, err := os.Stat(ripped); err != nil {
		t.Errorf("rip output not at %s: %v", ripped, err)
	}
	// Library must NOT have the file yet — that's the transcode step's job.
	if entries, _ := os.ReadDir(libRoot); len(entries) != 0 {
		t.Errorf("library should be empty after RunRip, found: %v", entries)
	}

	steps := sink.StepSequence()
	wantOrder := []state.StepID{
		state.StepDetect, state.StepIdentify, state.StepRip, state.StepEject,
	}
	if len(steps) != len(wantOrder) {
		t.Fatalf("RunRip emitted %d steps, want %d: %v", len(steps), len(wantOrder), steps)
	}
	for i, want := range wantOrder {
		if steps[i] != want {
			t.Errorf("RunRip step %d = %s, want %s", i, steps[i], want)
		}
	}
}

func TestBDMVHandler_Run_HappyPath(t *testing.T) {
	libRoot := t.TempDir()
	workRoot := t.TempDir()

	reg, _, _, _ := newRegistry()
	h := bdmv.New(bdmv.Deps{
		MakeMKVScanner: &fakeMakeMKV{scanTitles: []tools.MakeMKVTitle{
			{ID: 0, DurationSec: 30, SourceFile: "00000.mpls"},
			{ID: 1, DurationSec: 7000, SourceFile: "00800.mpls"},
		}},
		MakeMKVRipper: &fakeMakeMKV{stubName: "title_t01.mkv"},
		Tools:         reg,
		LibraryRoot:   libRoot,
		WorkRoot:      workRoot,
	})
	prof := &state.Profile{
		ID:                 "p-bd",
		DiscType:           state.DiscTypeBDMV,
		Name:               "BD-1080p",
		Preset:             "x265 RF 19 10-bit",
		OutputPathTemplate: "{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv",
		Options:            map[string]any{"min_title_seconds": float64(3600)},
	}
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeBDMV, Title: "Arrival", Year: 2016}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}

	want := filepath.Join(libRoot, "Arrival (2016)", "Arrival (2016).mkv")
	if _, err := os.Stat(want); err != nil {
		t.Errorf("expected file at %s: %v", want, err)
	}

	// Step order after the rip/transcode split: eject moves to the end
	// of RunRip (drive-freed boundary), notify stays at the end of
	// RunTranscode (file-in-library boundary). The monolithic Run path
	// emits both halves back-to-back, so the recording sink sees the
	// new combined order.
	starts := sink.StepSequence()
	wantOrder := []state.StepID{
		state.StepDetect, state.StepIdentify, state.StepRip, state.StepEject,
		state.StepTranscode, state.StepMove, state.StepNotify,
	}
	if len(starts) != len(wantOrder) {
		t.Fatalf("started %d steps, want %d: %v", len(starts), len(wantOrder), starts)
	}
	for i := range wantOrder {
		if starts[i] != wantOrder[i] {
			t.Errorf("step %d = %s, want %s", i, starts[i], wantOrder[i])
		}
	}
}

func TestBDMVHandler_Run_NoTitleAboveMin(t *testing.T) {
	reg, _, _, _ := newRegistry()
	h := bdmv.New(bdmv.Deps{
		MakeMKVScanner: &fakeMakeMKV{scanTitles: []tools.MakeMKVTitle{
			{ID: 0, DurationSec: 30}, // all titles below 1 hour
		}},
		MakeMKVRipper: &fakeMakeMKV{},
		Tools:         reg,
		LibraryRoot:   t.TempDir(),
		WorkRoot:      t.TempDir(),
	})
	prof := &state.Profile{
		ID:      "p-bd",
		Name:    "BD-1080p",
		Preset:  "x265 RF 19 10-bit",
		Options: map[string]any{"min_title_seconds": float64(3600)},
	}
	disc := &state.Disc{ID: "disc-2", Type: state.DiscTypeBDMV, Title: "X", Year: 2020}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}
	sink := testutil.NewRecordingSink()
	err := h.Run(context.Background(), drv, disc, prof, sink)
	if err == nil || !strings.Contains(err.Error(), "no title") {
		t.Errorf("want 'no title' error, got %v", err)
	}
}

// BDMV output is always an archival MKV — the HandBrake encode must
// keep every subtitle track on the disc, ignoring any configured
// SubsLang language filter.
func TestBDMVHandler_Run_KeepsAllSubtitles(t *testing.T) {
	reg, _, _, hb := newRegistry()
	h := bdmv.New(bdmv.Deps{
		MakeMKVScanner: &fakeMakeMKV{scanTitles: []tools.MakeMKVTitle{
			{ID: 1, DurationSec: 7000, SourceFile: "00800.mpls"},
		}},
		MakeMKVRipper: &fakeMakeMKV{stubName: "title_t01.mkv"},
		Tools:         reg,
		LibraryRoot:   t.TempDir(),
		WorkRoot:      t.TempDir(),
		SubsLang:      "eng", // must be ignored — BDMV output is archival MKV
	})
	prof := &state.Profile{
		ID: "p-bd", DiscType: state.DiscTypeBDMV, Name: "BD-1080p",
		Preset:             "x265 RF 19 10-bit",
		OutputPathTemplate: "{{.Title}}.mkv",
		Options:            map[string]any{"min_title_seconds": float64(3600)},
	}
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeBDMV, Title: "Arrival", Year: 2016}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	if err := h.Run(context.Background(), drv, disc, prof, testutil.NewRecordingSink()); err != nil {
		t.Fatal(err)
	}
	if len(hb.calls) != 1 {
		t.Fatalf("want 1 HandBrake call, got %d", len(hb.calls))
	}
	var hasAll, hasLangFilter bool
	for _, a := range hb.calls[0] {
		switch a {
		case "--all-subtitles":
			hasAll = true
		case "--subtitle-lang-list":
			hasLangFilter = true
		}
	}
	if !hasAll {
		t.Errorf("BDMV HandBrake args missing --all-subtitles: %v", hb.calls[0])
	}
	if hasLangFilter {
		t.Errorf("BDMV must not language-filter subtitles: %v", hb.calls[0])
	}
}

// bdmvEncoderArg pulls the --encoder value from the most recent fake
// HandBrake invocation. Returns "" if --encoder wasn't passed or no
// calls were recorded.
func bdmvEncoderArg(hb *fakeHandBrake) string {
	if len(hb.calls) == 0 {
		return ""
	}
	args := hb.calls[len(hb.calls)-1]
	for i, a := range args {
		if a == "--encoder" && i+1 < len(args) {
			return args[i+1]
		}
	}
	return ""
}

// runBDMVWithNVENC builds a fresh harness with the given NVENC state
// and profile codec, runs the pipeline, and returns hb for inspection.
func runBDMVWithNVENC(t *testing.T, nvencAvailable bool, videoCodec string) *fakeHandBrake {
	t.Helper()
	reg, _, _, hb := newRegistry()
	h := bdmv.New(bdmv.Deps{
		MakeMKVScanner: &fakeMakeMKV{scanTitles: []tools.MakeMKVTitle{
			{ID: 0, DurationSec: 30, SourceFile: "00000.mpls"},
			{ID: 1, DurationSec: 7000, SourceFile: "00800.mpls"},
		}},
		MakeMKVRipper:  &fakeMakeMKV{stubName: "title_t01.mkv"},
		Tools:          reg,
		LibraryRoot:    t.TempDir(),
		WorkRoot:       t.TempDir(),
		NVENCAvailable: nvencAvailable,
	})
	prof := &state.Profile{
		ID:                 "p-bd",
		DiscType:           state.DiscTypeBDMV,
		Name:               "BD-1080p",
		Preset:             "x265 RF 19 10-bit",
		VideoCodec:         videoCodec,
		OutputPathTemplate: "{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv",
		Options:            map[string]any{"min_title_seconds": float64(3600)},
	}
	disc := &state.Disc{ID: "disc-1", Type: state.DiscTypeBDMV, Title: "Arrival", Year: 2016}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}
	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return hb
}

func TestBDMV_Run_NVENCSelectsHardware(t *testing.T) {
	hb := runBDMVWithNVENC(t, true, "nvenc_h265")
	if got := bdmvEncoderArg(hb); got != "nvenc_h265" {
		t.Errorf("--encoder: got %q, want nvenc_h265", got)
	}
}

func TestBDMV_Run_NVENCFallsBackTo10BitX265(t *testing.T) {
	hb := runBDMVWithNVENC(t, false, "nvenc_h265")
	if got := bdmvEncoderArg(hb); got != "x265_10bit" {
		t.Errorf("--encoder: got %q, want x265_10bit (10-bit fallback)", got)
	}
}

func TestBDMV_Run_EmptyCodecPromotedTo10Bit(t *testing.T) {
	// Pre-NVENC behaviour: empty VideoCodec must still produce
	// x265_10bit so legacy DBs and seeded BDMV profiles don't
	// silently downgrade to x264.
	hb := runBDMVWithNVENC(t, false, "")
	if got := bdmvEncoderArg(hb); got != "x265_10bit" {
		t.Errorf("--encoder: got %q, want x265_10bit (empty codec → 10-bit)", got)
	}
}

func TestBDMV_Run_NVENCH264HardwarePassthrough(t *testing.T) {
	// nvenc_h264 with GPU must NOT be promoted to anything — user
	// explicitly chose 8-bit hardware.
	hb := runBDMVWithNVENC(t, true, "nvenc_h264")
	if got := bdmvEncoderArg(hb); got != "nvenc_h264" {
		t.Errorf("--encoder: got %q, want nvenc_h264", got)
	}
}

// TestBDMV_Run_Extras_RipsMainPlusExtras drives the include_extras
// profile path through the monolithic Run delegate. Scan returns
// main (7000s), one extras-band title (480s), and a navigation
// loop (30s). The encode set should be main + the extra; the nav
// loop drops below min_extra_seconds. Output: main at top-level,
// extra under the size-ratio-derived Jellyfin/Emby bucket — the
// extra is 10% of main (1_000 / 10_000 bytes) → "featurettes".
func TestBDMV_Run_Extras_RipsMainPlusExtras(t *testing.T) {
	libRoot := t.TempDir()
	reg, _, _, _ := newRegistry()
	h := bdmv.New(bdmv.Deps{
		MakeMKVScanner: &fakeMakeMKV{scanTitles: []tools.MakeMKVTitle{
			{ID: 0, DurationSec: 30, SourceFile: "00000.mpls"},   // nav loop
			{ID: 1, DurationSec: 7000, SourceFile: "00800.mpls"}, // main
			{ID: 2, DurationSec: 480, SourceFile: "00010.mpls"},  // extra
		}},
		MakeMKVRipper: &fakeMakeMKV{stubBytes: map[int]int{
			1: 10_000, // main: largest file → indexOfLargest picks it
			2: 1_000,  // extra: 10% of main → featurettes bucket
		}},
		Tools:       reg,
		LibraryRoot: libRoot,
		WorkRoot:    t.TempDir(),
	})
	prof := &state.Profile{
		ID:                 "p-bd-extras",
		DiscType:           state.DiscTypeBDMV,
		Name:               "BD-1080p + Extras",
		Preset:             "x265 RF 19 10-bit · + extras",
		OutputPathTemplate: "{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv",
		Options: map[string]any{
			"min_title_seconds": float64(3600),
			"include_extras":    true,
			"min_extra_seconds": 60,
			"extras_max_ratio":  90,
		},
	}
	disc := &state.Disc{ID: "disc-bd-ex", Type: state.DiscTypeBDMV, Title: "Arrival", Year: 2016}
	drv := &state.Drive{ID: "d1", DevPath: "/dev/sr0"}

	sink := testutil.NewRecordingSink()
	if err := h.Run(context.Background(), drv, disc, prof, sink); err != nil {
		t.Fatal(err)
	}

	wantMain := filepath.Join(libRoot, "Arrival (2016)", "Arrival (2016).mkv")
	if _, err := os.Stat(wantMain); err != nil {
		t.Errorf("main feature missing at %s: %v", wantMain, err)
	}
	wantExtra := filepath.Join(libRoot, "Arrival (2016)", "featurettes", "Featurette 01.mkv")
	if _, err := os.Stat(wantExtra); err != nil {
		t.Errorf("extra missing at %s: %v", wantExtra, err)
	}
	// Sanity: no navigation loop output should have been moved.
	navUnexpected := filepath.Join(libRoot, "Arrival (2016)", "featurettes", "Featurette 02.mkv")
	if _, err := os.Stat(navUnexpected); err == nil {
		t.Errorf("nav loop should have been dropped; found %s", navUnexpected)
	}

	// Silence unused-import warning on fmt — package-level use suffices.
	_ = fmt.Sprintf
}
