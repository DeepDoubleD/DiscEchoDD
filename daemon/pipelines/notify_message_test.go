package pipelines

import (
	"strings"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func jobWithMove(out int64, paths []string, ripNotes map[string]any) *state.Job {
	start := time.Now().Add(-90 * time.Second)
	fin := time.Now()
	steps := []state.JobStep{}
	if ripNotes != nil {
		steps = append(steps, state.JobStep{Step: state.StepRip, State: state.JobStepStateDone, Notes: ripNotes})
	}
	moveNotes := map[string]any{}
	if len(paths) == 1 {
		moveNotes["path"] = paths[0]
	} else if len(paths) > 1 {
		arr := make([]any, len(paths))
		for i, p := range paths {
			arr[i] = p
		}
		moveNotes["paths"] = arr
	}
	steps = append(steps, state.JobStep{Step: state.StepMove, State: state.JobStepStateDone, Notes: moveNotes})
	return &state.Job{OutputBytes: out, StartedAt: &start, FinishedAt: &fin, Steps: steps}
}

func TestBuildSuccess_Movie(t *testing.T) {
	disc := &state.Disc{
		Type: state.DiscTypeUHD, Title: "Jackass Volume Three", Year: 2010,
		MetadataJSON: `{"media_type":"movie","director":"Jeff Tremaine","poster_url":"https://img/p.jpg"}`,
	}
	prof := &state.Profile{Engine: "MakeMKV", VideoCodec: "copy", Container: "MKV"}
	job := jobWithMove(28_400_000_000, []string{"/library/movies/Jackass Volume Three (2010)/x.mkv"}, map[string]any{"rip_file_count": float64(1)})

	msg := BuildSuccessNotification(disc, job, prof)
	if msg.Title != "DiscEcho: Jackass Volume Three ripped" {
		t.Errorf("title: %q", msg.Title)
	}
	if msg.Trigger != "done" {
		t.Errorf("trigger: %q", msg.Trigger)
	}
	for _, want := range []string{"Jackass Volume Three (2010)", "4K UHD Blu-ray · MakeMKV", "Jeff Tremaine", "1 title", "Size:", "Duration:"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body missing %q\n%s", want, msg.Body)
		}
	}
	if len(msg.Attachments) != 1 || msg.Attachments[0] != "https://img/p.jpg" {
		t.Errorf("attachment: %v", msg.Attachments)
	}
}

func TestBuildSuccess_Series(t *testing.T) {
	disc := &state.Disc{
		Type: state.DiscTypeBDMV, Title: "The Wire",
		MetadataJSON: `{"media_type":"tv","selected_season":1,"selected_title_episodes":{"1":{"episode":1,"title":"A"},"2":{"episode":2,"title":"B"}}}`,
	}
	prof := &state.Profile{Engine: "MakeMKV", Container: "MKV"}
	job := jobWithMove(10_000_000_000, []string{"/library/tv/The Wire/e1.mkv", "/library/tv/The Wire/e2.mkv"}, nil)

	msg := BuildSuccessNotification(disc, job, prof)
	for _, want := range []string{"The Wire — Season 1", "Episodes:", "2 (E01–E02)"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body missing %q\n%s", want, msg.Body)
		}
	}
}

func TestBuildSuccess_AudioCD_AccurateRip(t *testing.T) {
	disc := &state.Disc{
		Type: state.DiscTypeAudioCD, Title: "Kind of Blue",
		Candidates:   []state.Candidate{{Artist: "Miles Davis"}},
		MetadataJSON: `{"label":"Columbia","catalog_number":"CL 1355","cover_url":"https://caa/x.jpg","tracks":[{"number":1},{"number":2}]}`,
	}
	prof := &state.Profile{Engine: "whipper", Container: "FLAC"}
	job := jobWithMove(300_000_000, []string{"/library/music/Miles Davis/Kind of Blue/01.flac"},
		map[string]any{"accuraterip": map[string]any{"status": "verified", "verified_tracks": float64(2), "total_tracks": float64(2)}})

	msg := BuildSuccessNotification(disc, job, prof)
	for _, want := range []string{"Miles Davis — Kind of Blue", "Audio CD · whipper (FLAC)", "Tracks:** 2", "AccurateRip:** verified (2/2)", "Columbia · CL 1355"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body missing %q\n%s", want, msg.Body)
		}
	}
	if len(msg.Attachments) != 1 {
		t.Errorf("expected cover attachment, got %v", msg.Attachments)
	}
}

func TestBuildSuccess_Game_Region(t *testing.T) {
	disc := &state.Disc{
		Type: state.DiscTypePS2, Title: "Gran Turismo 4", Year: 2004,
		Candidates:   []state.Candidate{{Region: "USA"}},
		MetadataJSON: `{"system":"Sony PlayStation 2"}`,
	}
	prof := &state.Profile{Engine: "redumper+chdman", Container: "CHD"}
	job := jobWithMove(4_000_000_000, []string{"/library/games/GT4.chd"}, nil)

	msg := BuildSuccessNotification(disc, job, prof)
	for _, want := range []string{"Gran Turismo 4 (2004)", "System:** Sony PlayStation 2", "Region:** USA", "Format:** CHD"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body missing %q\n%s", want, msg.Body)
		}
	}
}

func TestBuildSuccess_Data(t *testing.T) {
	disc := &state.Disc{Type: state.DiscTypeData, Title: "INSTALL_CD"}
	prof := &state.Profile{Engine: "ddrescue", Container: "ISO"}
	job := jobWithMove(700_000_000, []string{"/library/data/INSTALL_CD.iso"}, nil)
	msg := BuildSuccessNotification(disc, job, prof)
	if !strings.Contains(msg.Body, "archived successfully") || !strings.Contains(msg.Body, "1 file") {
		t.Errorf("data body:\n%s", msg.Body)
	}
}

func TestBuildSuccess_MarkdownEscapeAndNoMeta(t *testing.T) {
	disc := &state.Disc{Type: state.DiscTypeDVD, Title: "[REC]", Year: 2007}
	prof := &state.Profile{Engine: "HandBrake", VideoCodec: "x265", Container: "MKV"}
	job := jobWithMove(0, nil, nil) // no size, no paths, empty metadata
	msg := BuildSuccessNotification(disc, job, prof)
	if !strings.Contains(msg.Body, `\[REC\]`) {
		t.Errorf("title not escaped:\n%s", msg.Body)
	}
	if len(msg.Attachments) != 0 {
		t.Errorf("no attachment expected, got %v", msg.Attachments)
	}
}

func TestBuildFailure(t *testing.T) {
	disc := &state.Disc{Type: state.DiscTypeDVD, Title: "Some Movie", Year: 2001}
	prof := &state.Profile{Engine: "MakeMKV", Container: "MKV"}
	job := &state.Job{
		ErrorMessage: "makemkv scan: exit status 1",
		Steps: []state.JobStep{
			{Step: state.StepRip, State: state.JobStepStateFailed},
		},
	}
	msg := BuildFailureNotification(disc, job, prof)
	if msg.Trigger != "failed" {
		t.Errorf("trigger: %q", msg.Trigger)
	}
	if msg.Title != "DiscEcho: Some Movie — rip failed" {
		t.Errorf("title: %q", msg.Title)
	}
	for _, want := range []string{"failed to rip", "Failed at:** rip", "makemkv scan: exit status 1"} {
		if !strings.Contains(msg.Body, want) {
			t.Errorf("body missing %q\n%s", want, msg.Body)
		}
	}
	if len(msg.Attachments) != 0 {
		t.Errorf("failure should have no attachment")
	}
}
