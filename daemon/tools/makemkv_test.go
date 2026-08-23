package tools_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type captureSink struct {
	progress []float64
	speeds   []string
	etas     []int
	logs     []string
}

func (c *captureSink) Progress(pct float64, speed string, eta int) {
	c.progress = append(c.progress, pct)
	c.speeds = append(c.speeds, speed)
	c.etas = append(c.etas, eta)
}
func (c *captureSink) Log(_ state.LogLevel, format string, args ...any) {
	c.logs = append(c.logs, fmt.Sprintf(format, args...))
}
func (c *captureSink) SubStep(string) {}

func TestMakeMKVParseInfo_BDMV(t *testing.T) {
	b, err := os.ReadFile("testdata/makemkv-info-bdmv.txt")
	if err != nil {
		t.Fatal(err)
	}
	titles, err := tools.ParseMakeMKVInfo(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 2 {
		t.Fatalf("want 2 titles, got %d", len(titles))
	}
	feat := titles[1]
	if feat.ID != 1 {
		t.Errorf("feature title id = %d, want 1", feat.ID)
	}
	if feat.DurationSec != 7002 { // 1:56:42
		t.Errorf("feature duration = %d, want 7002", feat.DurationSec)
	}
	if feat.SourceFile != "00800.mpls" {
		t.Errorf("source file = %q, want 00800.mpls", feat.SourceFile)
	}
	if len(feat.Tracks) < 3 {
		t.Errorf("expected >=3 tracks, got %d", len(feat.Tracks))
	}
	var sawAudio bool
	for _, tr := range feat.Tracks {
		if tr.Type == "Audio" && tr.Lang == "eng" && strings.HasPrefix(tr.Codec, "A_") {
			sawAudio = true
		}
	}
	if !sawAudio {
		t.Errorf("missing eng audio track in %v", feat.Tracks)
	}
}

func TestMakeMKVParseInfo_UHD(t *testing.T) {
	b, err := os.ReadFile("testdata/makemkv-info-uhd.txt")
	if err != nil {
		t.Fatal(err)
	}
	titles, err := tools.ParseMakeMKVInfo(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(titles) != 1 {
		t.Fatalf("want 1 title, got %d", len(titles))
	}
	feat := titles[0]
	if feat.DurationSec != 9968 { // 2:46:08
		t.Errorf("UHD duration = %d, want 9968", feat.DurationSec)
	}
	var sawHEVC, sawAtmos bool
	for _, tr := range feat.Tracks {
		if tr.Codec == "V_MPEGH/ISO/HEVC" {
			sawHEVC = true
		}
		if strings.Contains(tr.Codec, "ATMOS") {
			sawAtmos = true
		}
	}
	if !sawHEVC {
		t.Errorf("missing HEVC stream in %v", feat.Tracks)
	}
	if !sawAtmos {
		t.Errorf("missing Atmos stream in %v", feat.Tracks)
	}
}

func TestMakeMKVParseInfo_Empty(t *testing.T) {
	if _, err := tools.ParseMakeMKVInfo(""); err == nil {
		t.Errorf("want error on empty input")
	}
}

func TestMakeMKVProgressStream_PRGV(t *testing.T) {
	sink := &captureSink{}
	// PRGV format: current,total,max — we drive on `total/max` so the
	// per-title progress is monotonic against the file the user is
	// waiting on. `current` resets per sub-operation; using it produced
	// progress=0 in v0.26.5 even while a 3.6 GB title was being
	// written.
	in := bytes.NewBufferString(strings.Join([]string{
		`PRGV:1024,0,65536`,
		`PRGV:1024,32768,65536`,
		`PRGV:1024,65536,65536`,
	}, "\n"))
	tools.ParseMakeMKVProgressStream(in, sink)
	if len(sink.progress) != 3 {
		t.Fatalf("want 3 progress updates, got %d", len(sink.progress))
	}
	if sink.progress[0] != 0 {
		t.Errorf("first progress = %f, want 0", sink.progress[0])
	}
	if sink.progress[1] != 50 {
		t.Errorf("mid progress = %f, want 50", sink.progress[1])
	}
	if sink.progress[2] != 100 {
		t.Errorf("last progress = %f, want 100", sink.progress[2])
	}
}

func TestMakeMKVProgressStream_ETA(t *testing.T) {
	// Feed a deterministic clock: t0 at the first sample, then +60s for
	// the second (25%) and +120s for the third (50%). At 25% over 60s
	// the rate is 25 pct / 60s; 75 remaining pct → 180s ETA. At 50%
	// over 120s the rate is 50/120; 50 remaining → 120s ETA.
	t0 := time.Unix(1_700_000_000, 0)
	clockCalls := 0
	steps := []time.Duration{0, 60 * time.Second, 120 * time.Second}
	restore := tools.SetMakeMKVNowForTest(func() time.Time {
		idx := clockCalls
		if idx >= len(steps) {
			idx = len(steps) - 1
		}
		clockCalls++
		return t0.Add(steps[idx])
	})
	defer restore()

	sink := &captureSink{}
	in := bytes.NewBufferString(strings.Join([]string{
		`PRGV:1024,0,65536`,
		`PRGV:1024,16384,65536`,
		`PRGV:1024,32768,65536`,
	}, "\n"))
	tools.ParseMakeMKVProgressStream(in, sink)
	if len(sink.etas) != 3 {
		t.Fatalf("want 3 ETA values, got %d", len(sink.etas))
	}
	if sink.etas[0] != 0 {
		t.Errorf("first ETA = %d, want 0 (no rate yet)", sink.etas[0])
	}
	if sink.etas[1] != 180 {
		t.Errorf("ETA at 25%% after 60s = %d, want 180", sink.etas[1])
	}
	if sink.etas[2] != 120 {
		t.Errorf("ETA at 50%% after 120s = %d, want 120", sink.etas[2])
	}
}

func TestParseMakeMKVMessage(t *testing.T) {
	cases := []struct {
		payload string
		want    string
	}{
		{`1005,0,1,"MakeMKV v1.17.5 linux(x64-release) started","%1","..."`, "MakeMKV v1.17.5 linux(x64-release) started"},
		{`3007,0,0,"Using direct disc access mode"`, "Using direct disc access mode"},
		{`5010,4,1,"Failed to open disc","%1","reason"`, "Failed to open disc"},
		// Malformed: no quoted message
		{`9999,0,0`, ""},
		// Malformed: too few fields
		{`9999`, ""},
	}
	for _, tc := range cases {
		got := tools.ExportedParseMakeMKVMessage(tc.payload)
		if got != tc.want {
			t.Errorf("parseMakeMKVMessage(%q) = %q, want %q", tc.payload, got, tc.want)
		}
	}
}

func TestMakeMKVProgressStream_LogsPRGCAsLog(t *testing.T) {
	sink := &captureSink{}
	in := bytes.NewBufferString(`PRGC:5018,0,"Saving to MKV file"` + "\n")
	tools.ParseMakeMKVProgressStream(in, sink)
	if len(sink.logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(sink.logs))
	}
	if !strings.Contains(sink.logs[0], "Saving to MKV file") {
		t.Errorf("log missing operation label, got %q", sink.logs[0])
	}
}

// TestMakeMKVProgressStream_LogsMSGAsLog reproduces a live diagnostic
// gap: MSG: lines (e.g. mid-rip disc-read/BD+/AACS errors) were being
// dropped on the floor during Rip() because ParseMakeMKVProgressStream
// only matched PRGV/PRGC prefixes, unlike Scan()'s parser. A rip that
// died mid-stream left nothing in the job log but "Saving to MKV
// file" and silence — the real error was there on stdout the whole
// time, just never forwarded.
func TestMakeMKVProgressStream_LogsMSGAsLog(t *testing.T) {
	sink := &captureSink{}
	in := bytes.NewBufferString(`MSG:5055,0,1,"Error 'Scsi error - MEDIUM ERROR:L-EC uncorrectable error' occurred while reading '/dev/sr1' at offset '123456789'","%1"` + "\n")
	tools.ParseMakeMKVProgressStream(in, sink)
	if len(sink.logs) != 1 {
		t.Fatalf("want 1 log, got %d", len(sink.logs))
	}
	if !strings.Contains(sink.logs[0], "MEDIUM ERROR") {
		t.Errorf("log missing MSG text, got %q", sink.logs[0])
	}
}

func TestNewMakeMKV_Defaults(t *testing.T) {
	m := tools.NewMakeMKV("", "")
	if m == nil {
		t.Fatal("nil MakeMKV")
	}
	// Calling Scan with a bin that doesn't exist must error cleanly,
	// not panic. /dev/null as a target alone is not a reliable error
	// trigger — installed makemkvcon binaries exit 0 with a "no
	// usable drives" message — so substitute the bin to force the
	// failure path regardless of host install state.
	m2 := tools.NewMakeMKV("/nonexistent-makemkvcon-binary", "")
	_, err := m2.Scan(context.Background(), "/dev/null", &captureSink{})
	if err == nil {
		t.Errorf("want error when bin is missing")
	}
}
