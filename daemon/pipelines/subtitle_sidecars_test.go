package pipelines_test

import (
	"context"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// fakeMKVSubs is a scripted pipelines.MKVSubtitleTool.
type fakeMKVSubs struct {
	tracks      []tools.MKVTrack
	tracksErr   error
	extractErr  map[int]error // trackID -> error, when set
	extractions []int         // trackIDs actually extracted, in order
}

func (f *fakeMKVSubs) Tracks(_ context.Context, _ string) ([]tools.MKVTrack, error) {
	return f.tracks, f.tracksErr
}

func (f *fakeMKVSubs) ExtractTrack(_ context.Context, _ string, trackID int, _ string) error {
	f.extractions = append(f.extractions, trackID)
	if f.extractErr != nil {
		if err, ok := f.extractErr[trackID]; ok {
			return err
		}
	}
	return nil
}

// noopSink discards every event -- these tests only care about return
// values and the fake's recorded calls.
type noopSink struct{}

func (noopSink) OnStepStart(state.StepID)                      {}
func (noopSink) OnProgress(state.StepID, float64, string, int) {}
func (noopSink) OnLog(state.LogLevel, string, ...any)          {}
func (noopSink) OnSubStep(string)                              {}
func (noopSink) OnStepDone(state.StepID, map[string]any)       {}
func (noopSink) OnStepFailed(state.StepID, error)              {}
func (noopSink) JobID() string                                 { return "" }

func TestExtractTextSubtitleSidecars_OnlyTextTracks(t *testing.T) {
	mkv := &fakeMKVSubs{tracks: []tools.MKVTrack{
		{ID: 0, Type: "video", CodecID: "V_MPEG4/ISO/AVC"},
		{ID: 1, Type: "audio", CodecID: "A_AC3", Language: "jpn"},
		{ID: 2, Type: "subtitles", CodecID: "S_TEXT/ASS", Language: "eng"},
		{ID: 3, Type: "subtitles", CodecID: "S_HDMV/PGS", Language: "eng"},
	}}
	sidecars, err := pipelines.ExtractTextSubtitleSidecars(context.Background(), mkv, "/library/Movie (2005).mkv", noopSink{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sidecars) != 1 || sidecars[0] != "/library/Movie (2005).eng.ass" {
		t.Errorf("sidecars = %v, want [\"/library/Movie (2005).eng.ass\"]", sidecars)
	}
	if len(mkv.extractions) != 1 || mkv.extractions[0] != 2 {
		t.Errorf("extractions = %v, want [2] (only the text track)", mkv.extractions)
	}
}

func TestExtractTextSubtitleSidecars_DuplicateLanguageGetsSuffix(t *testing.T) {
	mkv := &fakeMKVSubs{tracks: []tools.MKVTrack{
		{ID: 0, Type: "subtitles", CodecID: "S_TEXT/UTF8", Language: "eng"},
		{ID: 1, Type: "subtitles", CodecID: "S_TEXT/ASS", Language: "eng"},
	}}
	sidecars, err := pipelines.ExtractTextSubtitleSidecars(context.Background(), mkv, "/library/Show S01E01.mkv", noopSink{})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/library/Show S01E01.eng.srt", "/library/Show S01E01.eng.2.ass"}
	if len(sidecars) != len(want) {
		t.Fatalf("sidecars = %v, want %v", sidecars, want)
	}
	for i := range want {
		if sidecars[i] != want[i] {
			t.Errorf("sidecar %d: got %q, want %q", i, sidecars[i], want[i])
		}
	}
}

func TestExtractTextSubtitleSidecars_OneTrackFailureSkipsNotFails(t *testing.T) {
	mkv := &fakeMKVSubs{
		tracks: []tools.MKVTrack{
			{ID: 0, Type: "subtitles", CodecID: "S_TEXT/UTF8", Language: "eng"},
			{ID: 1, Type: "subtitles", CodecID: "S_TEXT/UTF8", Language: "jpn"},
		},
		extractErr: map[int]error{0: context.DeadlineExceeded},
	}
	sidecars, err := pipelines.ExtractTextSubtitleSidecars(context.Background(), mkv, "/library/Anime.mkv", noopSink{})
	if err != nil {
		t.Fatalf("a single track's extraction failure must not fail the whole call: %v", err)
	}
	if len(sidecars) != 1 || sidecars[0] != "/library/Anime.jpn.srt" {
		t.Errorf("sidecars = %v, want only the jpn track to have succeeded", sidecars)
	}
}

func TestExtractTextSubtitleSidecars_IdentifyFailurePropagates(t *testing.T) {
	mkv := &fakeMKVSubs{tracksErr: context.DeadlineExceeded}
	_, err := pipelines.ExtractTextSubtitleSidecars(context.Background(), mkv, "/library/Movie.mkv", noopSink{})
	if err == nil {
		t.Error("want error when track identification itself fails")
	}
}

func TestExtractTextSubtitlesFromProfile(t *testing.T) {
	if pipelines.ExtractTextSubtitlesFromProfile(&state.Profile{}) {
		t.Error("default should be false")
	}
	prof := &state.Profile{Options: map[string]any{"extract_text_subtitles": true}}
	if !pipelines.ExtractTextSubtitlesFromProfile(prof) {
		t.Error("want true when option is set")
	}
}
