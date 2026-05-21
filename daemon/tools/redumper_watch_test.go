package tools

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestRedumperWatch_AudioBoundaryTrips(t *testing.T) {
	now := time.Unix(0, 0)
	w := newRedumperWatch(now)
	line := "redumper: [LBA: 91] unexpected read type on retry (retry: 7, read type: AUDIO)"

	for i := 0; i < redumperMaxAudioBoundaryRetries-1; i++ {
		w.noteLog(line)
	}
	if r := w.abortReason(now); r != "" {
		t.Fatalf("aborted one retry early: %q", r)
	}
	w.noteLog(line)
	r := w.abortReason(now)
	if r == "" {
		t.Fatal("expected abort at the audio-retry limit")
	}
	if !strings.Contains(r, "mixed-mode") {
		t.Errorf("reason = %q, want a mixed-mode remediation tip", r)
	}

	// Other retry chatter (SCSI/C2) must never count toward the audio trip —
	// a heavily scratched but rippable disc emits those.
	w2 := newRedumperWatch(now)
	for i := 0; i < redumperMaxAudioBoundaryRetries+16; i++ {
		w2.noteLog("redumper: [LBA: 91] SCSI error (retry: 3)")
	}
	if r := w2.abortReason(now); r != "" {
		t.Errorf("non-audio retries must not trip the watchdog: %q", r)
	}
}

func TestRedumperWatch_StallTripsAndResets(t *testing.T) {
	now := time.Unix(1000, 0)

	w := newRedumperWatch(now)
	if r := w.abortReason(now.Add(redumperStallTimeout - time.Second)); r != "" {
		t.Fatalf("tripped before the stall timeout: %q", r)
	}
	r := w.abortReason(now.Add(redumperStallTimeout))
	if r == "" || !strings.Contains(r, "stalled") {
		t.Fatalf("expected a stall trip at the timeout, got %q", r)
	}

	// Forward (increasing-percent) progress resets the stall timer.
	w2 := newRedumperWatch(now)
	w2.noteProgress(5, now.Add(redumperStallTimeout-time.Second))
	if r := w2.abortReason(now.Add(redumperStallTimeout)); r != "" {
		t.Errorf("forward progress must reset the stall timer: %q", r)
	}
	// A non-increasing percent does NOT reset it — the timer still runs from
	// the last real advance.
	w2.noteProgress(5, now.Add(redumperStallTimeout))
	if r := w2.abortReason(now.Add(2 * redumperStallTimeout)); r == "" {
		t.Error("expected a stall trip when percent stops advancing")
	}
}

type watchTestSink struct {
	logs     []string
	progress []float64
}

func (s *watchTestSink) Progress(p float64, _ string, _ int) { s.progress = append(s.progress, p) }
func (s *watchTestSink) Log(_ state.LogLevel, f string, a ...any) {
	s.logs = append(s.logs, fmt.Sprintf(f, a...))
}
func (s *watchTestSink) SubStep(string) {}

// redumperWatchSink must count audio-boundary lines while forwarding every
// event to the wrapped sink unchanged.
func TestRedumperWatchSink_CountsAndForwards(t *testing.T) {
	now := time.Unix(0, 0)
	redumperNow = func() time.Time { return now }
	t.Cleanup(func() { redumperNow = func() time.Time { return time.Now() } })

	w := newRedumperWatch(now)
	under := &watchTestSink{}
	ws := redumperWatchSink{Sink: under, watch: w}

	for i := 0; i < redumperMaxAudioBoundaryRetries; i++ {
		ws.Log(state.LogLevelInfo, "redumper: %s", "[LBA: 91] unexpected read type on retry (retry: 1, read type: AUDIO)")
	}
	ws.Progress(7, "", 0)

	if len(under.logs) != redumperMaxAudioBoundaryRetries {
		t.Errorf("forwarded %d logs, want %d", len(under.logs), redumperMaxAudioBoundaryRetries)
	}
	if len(under.progress) != 1 || under.progress[0] != 7 {
		t.Errorf("progress not forwarded: %v", under.progress)
	}
	if r := w.abortReason(now); r == "" {
		t.Error("watch should have tripped on the forwarded audio lines")
	}
}
