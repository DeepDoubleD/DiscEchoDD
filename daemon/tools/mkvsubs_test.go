package tools_test

import (
	"context"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

const sampleMkvmergeJSON = `{
  "tracks": [
    {"id": 0, "type": "video", "properties": {"codec_id": "V_MPEG4/ISO/AVC"}},
    {"id": 1, "type": "audio", "properties": {"codec_id": "A_AC3", "language": "jpn"}},
    {"id": 2, "type": "audio", "properties": {"codec_id": "A_AC3", "language": "eng"}},
    {"id": 3, "type": "subtitles", "properties": {"codec_id": "S_TEXT/ASS", "language": "eng"}},
    {"id": 4, "type": "subtitles", "properties": {"codec_id": "S_HDMV/PGS", "language": "eng"}},
    {"id": 5, "type": "subtitles", "properties": {"codec_id": "S_TEXT/UTF8"}}
  ]
}`

func TestParseMKVMergeJSON(t *testing.T) {
	tracks, err := tools.ParseMKVMergeJSON([]byte(sampleMkvmergeJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(tracks) != 6 {
		t.Fatalf("want 6 tracks, got %d", len(tracks))
	}
	// Track 5 has no language property -- must default to "und", not "".
	if tracks[5].Language != "und" {
		t.Errorf("track 5 language: got %q, want %q", tracks[5].Language, "und")
	}
}

func TestMKVTrack_IsTextSubtitle(t *testing.T) {
	tracks, err := tools.ParseMKVMergeJSON([]byte(sampleMkvmergeJSON))
	if err != nil {
		t.Fatal(err)
	}
	want := map[int]bool{
		0: false, // video
		1: false, // audio
		2: false, // audio
		3: true,  // S_TEXT/ASS
		4: false, // S_HDMV/PGS -- bitmap, needs OCR
		5: true,  // S_TEXT/UTF8
	}
	for _, tr := range tracks {
		if got := tr.IsTextSubtitle(); got != want[tr.ID] {
			t.Errorf("track %d (%s): IsTextSubtitle() = %v, want %v", tr.ID, tr.CodecID, got, want[tr.ID])
		}
	}
}

func TestMKVTrack_SidecarExt(t *testing.T) {
	cases := []struct {
		codecID string
		want    string
	}{
		{"S_TEXT/UTF8", "srt"},
		{"S_TEXT/ASS", "ass"},
		{"S_TEXT/SSA", "ass"},
		{"S_HDMV/PGS", "sub"},
	}
	for _, c := range cases {
		got := tools.MKVTrack{CodecID: c.codecID}.SidecarExt()
		if got != c.want {
			t.Errorf("SidecarExt(%s) = %q, want %q", c.codecID, got, c.want)
		}
	}
}

func TestParseMKVMergeJSON_Malformed(t *testing.T) {
	if _, err := tools.ParseMKVMergeJSON([]byte("not json")); err == nil {
		t.Error("want error on malformed JSON")
	}
}

func TestNewMKVToolNix_DefaultBins(t *testing.T) {
	m := tools.NewMKVToolNix("", "")
	// Calling against a nonexistent file should fail cleanly, not panic --
	// confirms the default "mkvmerge"/"mkvextract" names resolve through
	// exec.CommandContext without a nil-bin crash.
	if _, err := m.Tracks(context.Background(), "/nonexistent.mkv"); err == nil {
		t.Error("want error probing a nonexistent file")
	}
}
