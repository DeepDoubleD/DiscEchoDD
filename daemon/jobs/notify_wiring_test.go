package jobs_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/jobs"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

type appriseRecorder struct {
	mu       sync.Mutex
	calls    int
	triggers map[string]int
}

func (a *appriseRecorder) Name() string { return "apprise" }
func (a *appriseRecorder) Run(_ context.Context, _ []string, _ map[string]string, _ string, _ tools.Sink) error {
	a.mu.Lock()
	a.calls++
	a.mu.Unlock()
	return nil
}
func (a *appriseRecorder) count() int { a.mu.Lock(); defer a.mu.Unlock(); return a.calls }

func waitApprise(a *appriseRecorder, n int, d time.Duration) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if a.count() >= n {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return a.count() >= n
}

func notifyOrch(t *testing.T, h pipelines.Handler, failed bool) (*appriseRecorder, *[]string) {
	t.Helper()
	store, bc, _ := openOrch(t)
	t.Cleanup(bc.Close)
	reg := pipelines.NewRegistry()
	reg.Register(h)

	ap := &appriseRecorder{triggers: map[string]int{}}
	toolReg := tools.NewRegistry()
	toolReg.Register(ap)
	var mu sync.Mutex
	var seen []string
	urls := func(_ context.Context, trigger string) []string {
		mu.Lock()
		seen = append(seen, trigger)
		mu.Unlock()
		return []string{"ntfys://x"}
	}

	o := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store: store, Broadcaster: bc, Pipelines: reg,
		Tools: toolReg, URLsForTrigger: urls,
	})
	t.Cleanup(o.Close)

	_, disc, prof := seedJobInputs(t, store)
	job, err := o.Submit(context.Background(), disc.ID, prof.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := state.JobStateDone
	if failed {
		want = state.JobStateFailed
	}
	if err := waitJobState(store, job.ID, want, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	return ap, &seen
}

func TestNotify_MonolithicSuccessFiresDone(t *testing.T) {
	ap, seen := notifyOrch(t, &stubHandler{}, false)
	if !waitApprise(ap, 1, time.Second) {
		t.Fatalf("expected a notification, got %d calls", ap.count())
	}
	if len(*seen) == 0 || (*seen)[len(*seen)-1] != "done" {
		t.Errorf("expected done trigger, saw %v", *seen)
	}
}

func TestNotify_FailureFiresFailed(t *testing.T) {
	ap, seen := notifyOrch(t, &stubHandler{failOnRun: errors.New("boom")}, true)
	if !waitApprise(ap, 1, time.Second) {
		t.Fatalf("expected a failure notification, got %d calls", ap.count())
	}
	if len(*seen) == 0 || (*seen)[len(*seen)-1] != "failed" {
		t.Errorf("expected failed trigger, saw %v", *seen)
	}
}
