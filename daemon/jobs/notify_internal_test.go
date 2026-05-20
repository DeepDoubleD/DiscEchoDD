package jobs

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type fakeNotifyStore struct{ job *state.Job }

func (f fakeNotifyStore) GetJob(_ context.Context, _ string) (*state.Job, error) {
	return f.job, nil
}

type recordingApprise struct {
	mu    sync.Mutex
	calls [][]string
}

func (r *recordingApprise) Name() string { return "apprise" }
func (r *recordingApprise) Run(_ context.Context, args []string, _ map[string]string, _ string, _ tools.Sink) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, args)
	return nil
}
func (r *recordingApprise) callCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.calls)
}
func (r *recordingApprise) lastArgs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.calls) == 0 {
		return nil
	}
	return r.calls[len(r.calls)-1]
}

func regWithApprise(t *testing.T) (*tools.Registry, *recordingApprise) {
	t.Helper()
	reg := tools.NewRegistry()
	ap := &recordingApprise{}
	reg.Register(ap)
	return reg, ap
}

func urlsAlways(trigger *string) func(context.Context, string) []string {
	return func(_ context.Context, tr string) []string {
		if trigger != nil {
			*trigger = tr
		}
		return []string{"ntfys://x"}
	}
}

func sampleDiscJob() (*state.Disc, *state.Job, *state.Profile) {
	disc := &state.Disc{Type: state.DiscTypeAudioCD, Title: "Kind of Blue"}
	job := &state.Job{
		OutputBytes: 300_000_000,
		Steps: []state.JobStep{
			{Step: state.StepMove, State: state.JobStepStateDone, Notes: map[string]any{"path": "/library/music/x.flac"}},
		},
	}
	prof := &state.Profile{Engine: "whipper", Container: "FLAC"}
	return disc, job, prof
}

func TestSendTerminalNotification_DoneFiresDoneTrigger(t *testing.T) {
	reg, ap := regWithApprise(t)
	disc, job, prof := sampleDiscJob()
	var trigger string
	sendTerminalNotification(reg, urlsAlways(&trigger), fakeNotifyStore{job: job}, "j1", disc, prof, state.JobStateDone)

	if ap.callCount() != 1 {
		t.Fatalf("apprise calls: %d", ap.callCount())
	}
	if trigger != "done" {
		t.Errorf("trigger: %q", trigger)
	}
	if joined := strings.Join(ap.lastArgs(), " "); !strings.Contains(joined, "-i markdown") {
		t.Errorf("expected markdown args: %v", ap.lastArgs())
	}
}

func TestSendTerminalNotification_FailedFiresFailedTrigger(t *testing.T) {
	reg, ap := regWithApprise(t)
	disc, job, prof := sampleDiscJob()
	job.ErrorMessage = "boom"
	var trigger string
	sendTerminalNotification(reg, urlsAlways(&trigger), fakeNotifyStore{job: job}, "j1", disc, prof, state.JobStateFailed)

	if ap.callCount() != 1 || trigger != "failed" {
		t.Fatalf("calls=%d trigger=%q", ap.callCount(), trigger)
	}
}

func TestSendTerminalNotification_CancelledIsNoOp(t *testing.T) {
	reg, ap := regWithApprise(t)
	disc, job, prof := sampleDiscJob()
	sendTerminalNotification(reg, urlsAlways(nil), fakeNotifyStore{job: job}, "j1", disc, prof, state.JobStateCancelled)
	if ap.callCount() != 0 {
		t.Errorf("cancelled should not notify; calls=%d", ap.callCount())
	}
}

func TestSendTerminalNotification_NoApprise(t *testing.T) {
	disc, job, prof := sampleDiscJob()
	// nil registry → silent no-op (must not panic).
	sendTerminalNotification(nil, urlsAlways(nil), fakeNotifyStore{job: job}, "j1", disc, prof, state.JobStateDone)
}

func TestSendTerminalNotification_NoSubscribers(t *testing.T) {
	reg, ap := regWithApprise(t)
	disc, job, prof := sampleDiscJob()
	none := func(context.Context, string) []string { return nil }
	sendTerminalNotification(reg, none, fakeNotifyStore{job: job}, "j1", disc, prof, state.JobStateDone)
	if ap.callCount() != 0 {
		t.Errorf("no subscribers → no send; calls=%d", ap.callCount())
	}
}
