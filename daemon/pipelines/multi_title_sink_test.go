package pipelines_test

import (
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

type recordingToolsSink struct {
	pcts   []float64
	speeds []string
	etas   []int
	logs   []string
	subs   []string
}

func (r *recordingToolsSink) Progress(pct float64, speed string, eta int) {
	r.pcts = append(r.pcts, pct)
	r.speeds = append(r.speeds, speed)
	r.etas = append(r.etas, eta)
}
func (r *recordingToolsSink) Log(_ state.LogLevel, format string, _ ...any) {
	r.logs = append(r.logs, format)
}
func (r *recordingToolsSink) SubStep(name string) { r.subs = append(r.subs, name) }

func TestMultiTitleSink_ScalesProgress(t *testing.T) {
	cases := []struct {
		name     string
		titleIdx int
		total    int
		inputPct float64
		wantAgg  float64
	}{
		{"first title 0%", 1, 4, 0, 0},
		{"first title 50%", 1, 4, 50, 12.5},
		{"first title 100%", 1, 4, 100, 25},
		{"second title 0%", 2, 4, 0, 25},
		{"second title 50%", 2, 4, 50, 37.5},
		{"last title 100%", 4, 4, 100, 100},
		{"only title 50%", 1, 1, 50, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := &recordingToolsSink{}
			s := pipelines.NewMultiTitleSink(inner, tc.titleIdx, tc.total, time.Now())
			s.Progress(tc.inputPct, "", 0)
			if len(inner.pcts) != 1 || inner.pcts[0] != tc.wantAgg {
				t.Errorf("agg pct = %v, want %v", inner.pcts, tc.wantAgg)
			}
		})
	}
}

func TestMultiTitleSink_ETAFromAggregate(t *testing.T) {
	// 4 titles total. We're 60s into the rip. Title 2 just hit 50%,
	// so aggregate = (1 * 100 + 50) / 4 = 37.5%. ETA: 60s × (100 - 37.5) / 37.5 = 100s.
	start := time.Unix(1_700_000_000, 0)
	inner := &recordingToolsSink{}
	s := pipelines.NewMultiTitleSink(inner, 2, 4, start)
	s.SetNowForTest(func() time.Time { return start.Add(60 * time.Second) })
	s.Progress(50, "", 999) // per-title ETA discarded; aggregate ETA replaces it
	if len(inner.etas) != 1 || inner.etas[0] != 100 {
		t.Fatalf("agg eta = %v, want [100]", inner.etas)
	}
}

func TestMultiTitleSink_ForwardsLogAndSubStep(t *testing.T) {
	inner := &recordingToolsSink{}
	s := pipelines.NewMultiTitleSink(inner, 1, 1, time.Now())
	s.Log(state.LogLevelInfo, "hello")
	s.SubStep("scan")
	if len(inner.logs) != 1 || inner.logs[0] != "hello" {
		t.Errorf("logs = %v", inner.logs)
	}
	if len(inner.subs) != 1 || inner.subs[0] != "scan" {
		t.Errorf("substeps = %v", inner.subs)
	}
}
