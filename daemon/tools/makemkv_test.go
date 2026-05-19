package tools_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type captureSink struct {
	progress []float64
	logs     []string
}

func (c *captureSink) Progress(pct float64, _ string, _ int) {
	c.progress = append(c.progress, pct)
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
	in := bytes.NewBufferString(strings.Join([]string{
		`PRGV:0,1024,65536`,
		`PRGV:32768,1024,65536`,
		`PRGV:65536,1024,65536`,
	}, "\n"))
	tools.ParseMakeMKVProgressStream(in, sink)
	if len(sink.progress) != 3 {
		t.Fatalf("want 3 progress updates, got %d", len(sink.progress))
	}
	if sink.progress[0] != 0 {
		t.Errorf("first progress = %f, want 0", sink.progress[0])
	}
	if sink.progress[2] != 100 {
		t.Errorf("last progress = %f, want 100", sink.progress[2])
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
