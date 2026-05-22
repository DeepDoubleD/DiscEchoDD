package tools_test

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type captureSinkVCDXRip struct {
	progress []float64
	logs     []string
	substeps []string
}

func (c *captureSinkVCDXRip) Progress(pct float64, _ string, _ int) {
	c.progress = append(c.progress, pct)
}
func (c *captureSinkVCDXRip) Log(_ state.LogLevel, format string, args ...any) {
	c.logs = append(c.logs, fmt.Sprintf(format, args...))
}
func (c *captureSinkVCDXRip) SubStep(name string) {
	c.substeps = append(c.substeps, name)
}

func TestParseVCDXRipProgress(t *testing.T) {
	// vcdxrip --progress reprints its per-file line with carriage returns
	// as it advances through each track.
	in := bytes.NewBufferString(
		"#extract[avseq01.mpg]: 0/217803 ( 0%)\r" +
			"#extract[avseq01.mpg]: 11523/217803 ( 5%)\r" +
			"#extract[avseq01.mpg]: 217803/217803 (100%)\r",
	)
	sink := &captureSinkVCDXRip{}
	tools.ParseVCDXRipProgress(in, sink)

	if len(sink.progress) != 3 {
		t.Fatalf("want 3 progress events, got %d: %v", len(sink.progress), sink.progress)
	}
	if sink.progress[0] != 0 || sink.progress[1] != 5 || sink.progress[2] != 100 {
		t.Errorf("progress = %v, want [0 5 100]", sink.progress)
	}
	// The current output file is surfaced once as a sub-step (deduped).
	if len(sink.substeps) != 1 || sink.substeps[0] != "avseq01.mpg" {
		t.Errorf("substeps = %v, want [avseq01.mpg]", sink.substeps)
	}
}

func TestParseVCDXRipProgress_NewFileEmitsSubStep(t *testing.T) {
	in := bytes.NewBufferString(
		"#extract[avseq01.mpg]: 217803/217803 (100%)\r" +
			"#extract[avseq02.mpg]: 0/50000 ( 0%)\r" +
			"#extract[avseq02.mpg]: 50000/50000 (100%)\r",
	)
	sink := &captureSinkVCDXRip{}
	tools.ParseVCDXRipProgress(in, sink)

	if len(sink.substeps) != 2 {
		t.Fatalf("want 2 sub-steps (one per file), got %d: %v", len(sink.substeps), sink.substeps)
	}
	if sink.substeps[0] != "avseq01.mpg" || sink.substeps[1] != "avseq02.mpg" {
		t.Errorf("substeps = %v, want [avseq01.mpg avseq02.mpg]", sink.substeps)
	}
}

func TestParseVCDXRipProgress_ForwardsUnknownLinesToLog(t *testing.T) {
	in := bytes.NewBufferString(
		"++ INFO: CD-ROM XA\n" +
			"#extract[avseq01.mpg]: 50/100 ( 50%)\n" +
			"++ INFO: done\n",
	)
	sink := &captureSinkVCDXRip{}
	tools.ParseVCDXRipProgress(in, sink)

	if len(sink.progress) != 1 || sink.progress[0] != 50 {
		t.Errorf("progress = %v, want [50]", sink.progress)
	}
	if len(sink.logs) != 2 {
		t.Fatalf("want 2 log lines, got %d: %v", len(sink.logs), sink.logs)
	}
	if sink.logs[0] != "vcdxrip: ++ INFO: CD-ROM XA" {
		t.Errorf("log[0] = %q", sink.logs[0])
	}
}

func TestVCDXRip_Name(t *testing.T) {
	v := &tools.VCDXRip{}
	if v.Name() != "vcdxrip" {
		t.Errorf("Name = %q, want vcdxrip", v.Name())
	}
}

func TestVCDXRip_RipErrorsOnMissingBinary(t *testing.T) {
	v := &tools.VCDXRip{Bin: "vcdxrip-does-not-exist"}
	err := v.Rip(context.Background(), "/dev/null", t.TempDir(), &captureSinkVCDXRip{})
	if err == nil {
		t.Error("want error from missing binary")
	}
}
