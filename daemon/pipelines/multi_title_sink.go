package pipelines

import (
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// MultiTitleSink scales per-title Progress events from a multi-title
// rip into a single 0→100 aggregate against the parent sink. The
// MakeMKV-based pipelines (DVD / BDMV / UHD) rip each picked title in
// a loop; without scaling, the dashboard's progress bar resets to 0
// at every title boundary, which gives the impression of a stuck rip
// even when 5 of 6 titles are already on disk. Log and SubStep
// events forward unchanged.
//
// ETA seconds is recomputed from wall-clock elapsed since the first
// title started vs the aggregate percent — the per-title ETA the
// rip tool provides only describes the current title, not the
// remaining work. Speed forwards unchanged (MakeMKV doesn't emit
// bytes/sec; redumper passes the LBA-derived value through).
type MultiTitleSink struct {
	inner       tools.Sink
	titleIdx    int // 1..totalTitles
	totalTitles int
	startWall   time.Time
	nowFn       func() time.Time
}

// NewMultiTitleSink wraps inner so that Progress(pct) for the current
// title (1-indexed titleIdx of totalTitles) reports the overall
// fraction to inner. startWall is the wall-clock moment the
// multi-title rip began, used for the aggregate ETA computation.
func NewMultiTitleSink(inner tools.Sink, titleIdx, totalTitles int, startWall time.Time) *MultiTitleSink {
	return &MultiTitleSink{
		inner:       inner,
		titleIdx:    titleIdx,
		totalTitles: totalTitles,
		startWall:   startWall,
		nowFn:       time.Now,
	}
}

// Progress scales the per-title pct into a 0→100 aggregate. ETA is
// recomputed against the aggregate so it reflects total remaining
// work across the picked title set, not just the current title.
func (s *MultiTitleSink) Progress(pct float64, speed string, _ int) {
	if s.totalTitles <= 0 {
		s.inner.Progress(pct, speed, 0)
		return
	}
	clamped := pct
	if clamped < 0 {
		clamped = 0
	}
	if clamped > 100 {
		clamped = 100
	}
	aggPct := (float64(s.titleIdx-1)*100 + clamped) / float64(s.totalTitles)
	aggETA := 0
	if aggPct > 0 && aggPct < 100 {
		elapsed := s.nowFn().Sub(s.startWall).Seconds()
		if elapsed > 0 {
			aggETA = int(elapsed * (100 - aggPct) / aggPct)
		}
	}
	s.inner.Progress(aggPct, speed, aggETA)
}

// Log forwards unchanged.
func (s *MultiTitleSink) Log(level state.LogLevel, format string, args ...any) {
	s.inner.Log(level, format, args...)
}

// SubStep forwards unchanged.
func (s *MultiTitleSink) SubStep(name string) {
	s.inner.SubStep(name)
}

// SetNowForTest replaces the wall-clock source. Only used by tests.
func (s *MultiTitleSink) SetNowForTest(now func() time.Time) { s.nowFn = now }
