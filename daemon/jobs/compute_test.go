package jobs_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/jobs"
	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/spool"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// splittableStub is a fake pipelines.SplittableHandler that records
// every RunRip / RunTranscode invocation. failOnRip / failOnTranscode
// let individual tests drive each phase to failure.
type splittableStub struct {
	failOnRip       error
	failOnTranscode error
	ripDelay        time.Duration
	transcodeDelay  time.Duration

	ripStarted       chan struct{}
	transcodeStarted chan struct{}

	mu             sync.Mutex
	ripCalls       int
	transcodeCalls int
	ripSpoolPath   string
}

func (s *splittableStub) DiscType() state.DiscType { return state.DiscTypeBDMV }
func (s *splittableStub) Identify(_ context.Context, _ *state.Drive) (*state.Disc, []state.Candidate, error) {
	return nil, nil, nil
}
func (s *splittableStub) Plan(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	return nil
}

func (s *splittableStub) PlanRip(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 8)
	for _, sid := range state.CanonicalSteps() {
		// Mark the transcode-half steps as skipped on the rip-half row.
		skip := false
		switch sid {
		case state.StepTranscode, state.StepCompress, state.StepMove, state.StepNotify:
			skip = true
		}
		out = append(out, pipelines.StepPlan{ID: sid, Skip: skip})
	}
	return out
}

func (s *splittableStub) PlanTranscode(_ *state.Disc, _ *state.Profile) []pipelines.StepPlan {
	out := make([]pipelines.StepPlan, 0, 4)
	for _, sid := range state.CanonicalTranscodeSteps() {
		out = append(out, pipelines.StepPlan{ID: sid})
	}
	return out
}

func (s *splittableStub) Run(ctx context.Context, drv *state.Drive, disc *state.Disc, prof *state.Profile, sink pipelines.EventSink) error {
	// SplittableHandler embeds Handler; the orchestrator should never
	// reach this path when Compute+Spool are wired. Returning an error
	// makes test failures obvious if the routing breaks.
	return errors.New("splittableStub.Run should not be invoked when split path is wired")
}

func (s *splittableStub) RunRip(ctx context.Context, _ *state.Drive, _ *state.Disc, _ *state.Profile, spoolDir string, _ pipelines.EventSink) (pipelines.RipResult, error) {
	s.mu.Lock()
	s.ripCalls++
	started := s.ripStarted
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if s.ripDelay > 0 {
		select {
		case <-time.After(s.ripDelay):
		case <-ctx.Done():
			return pipelines.RipResult{}, ctx.Err()
		}
	}
	if s.failOnRip != nil {
		return pipelines.RipResult{}, s.failOnRip
	}
	path := s.ripSpoolPath
	if path == "" {
		path = spoolDir
	}
	return pipelines.RipResult{SpoolPath: path}, nil
}

func (s *splittableStub) RunTranscode(ctx context.Context, result pipelines.RipResult, _ *state.Disc, _ *state.Profile, _ pipelines.EventSink) error {
	s.mu.Lock()
	s.transcodeCalls++
	started := s.transcodeStarted
	s.mu.Unlock()
	if started != nil {
		select {
		case started <- struct{}{}:
		default:
		}
	}
	if s.transcodeDelay > 0 {
		select {
		case <-time.After(s.transcodeDelay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.failOnTranscode
}

func openSplit(t *testing.T) (*state.Store, *state.Broadcaster, *spool.Spool, *splittableStub) {
	t.Helper()
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "x.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store := state.NewStore(db)
	sp, err := spool.New(filepath.Join(dir, "spool"))
	if err != nil {
		t.Fatal(err)
	}
	return store, state.NewBroadcaster(), sp, &splittableStub{}
}

func seedSplitInputs(t *testing.T, store *state.Store) (*state.Drive, *state.Disc, *state.Profile) {
	t.Helper()
	ctx := context.Background()
	drv := &state.Drive{DevPath: "/dev/sr0", Model: "x", Bus: "y",
		State: state.DriveStateIdle, LastSeenAt: time.Now()}
	if err := store.UpsertDrive(ctx, drv); err != nil {
		t.Fatal(err)
	}
	prof := &state.Profile{DiscType: state.DiscTypeBDMV, Name: "BD-test",
		Engine: "MakeMKV+HandBrake", Format: "MKV", Preset: "x265 RF 19",
		OutputPathTemplate: "{{.Title}}", Enabled: true, StepCount: 7}
	if err := store.CreateProfile(ctx, prof); err != nil {
		t.Fatal(err)
	}
	disc := &state.Disc{DriveID: drv.ID, Type: state.DiscTypeBDMV,
		Title:      "Movie",
		Candidates: []state.Candidate{{Source: "tmdb", Title: "Movie", TMDBID: 1, Confidence: 90}}}
	if err := store.CreateDisc(ctx, disc); err != nil {
		t.Fatal(err)
	}
	return drv, disc, prof
}

func TestOrchestrator_SplittablePath_RunsBothHalves(t *testing.T) {
	store, bc, sp, h := openSplit(t)
	defer bc.Close()

	reg := pipelines.NewRegistry()
	reg.Register(h)

	compute := jobs.NewCompute(jobs.ComputeConfig{
		Store: store, Broadcaster: bc, Pipelines: reg, Spool: sp, Concurrency: 1,
	})
	t.Cleanup(compute.Close)

	o := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store: store, Broadcaster: bc, Pipelines: reg,
		Compute: compute, Spool: sp,
	})
	t.Cleanup(o.Close)

	_, disc, prof := seedSplitInputs(t, store)
	ripJob, err := o.Submit(context.Background(), disc.ID, prof.ID)
	if err != nil {
		t.Fatal(err)
	}

	// Rip job hits done first.
	if err := waitJobState(store, ripJob.ID, state.JobStateDone, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	// Child transcode job materialises and reaches done too.
	transcodeID := waitForChild(t, store, ripJob.ID, 2*time.Second)
	if err := waitJobState(store, transcodeID, state.JobStateDone, 2*time.Second); err != nil {
		t.Fatal(err)
	}

	if h.ripCalls != 1 {
		t.Errorf("ripCalls = %d, want 1", h.ripCalls)
	}
	if h.transcodeCalls != 1 {
		t.Errorf("transcodeCalls = %d, want 1", h.transcodeCalls)
	}

	// Spool should be cleaned up on successful transcode.
	child, _ := store.GetJob(context.Background(), transcodeID)
	if child.SpoolPath == "" {
		t.Errorf("transcode job missing spool_path")
	}
	if _, err := stat(child.SpoolPath); err == nil {
		t.Errorf("spool dir %s should have been cleaned, still exists", child.SpoolPath)
	}
	if child.ParentJobID != ripJob.ID {
		t.Errorf("transcode parent: got %s want %s", child.ParentJobID, ripJob.ID)
	}
	if child.Kind != state.JobKindTranscode {
		t.Errorf("transcode kind: got %s want transcode", child.Kind)
	}
	if len(child.Steps) != 4 {
		t.Errorf("transcode steps: want 4, got %d", len(child.Steps))
	}
}

func TestOrchestrator_SplittablePath_RipFailureSkipsTranscode(t *testing.T) {
	store, bc, sp, h := openSplit(t)
	defer bc.Close()
	h.failOnRip = errors.New("ripper exploded")

	reg := pipelines.NewRegistry()
	reg.Register(h)
	compute := jobs.NewCompute(jobs.ComputeConfig{
		Store: store, Broadcaster: bc, Pipelines: reg, Spool: sp, Concurrency: 1,
	})
	t.Cleanup(compute.Close)
	o := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store: store, Broadcaster: bc, Pipelines: reg,
		Compute: compute, Spool: sp,
	})
	t.Cleanup(o.Close)

	_, disc, prof := seedSplitInputs(t, store)
	ripJob, err := o.Submit(context.Background(), disc.ID, prof.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitJobState(store, ripJob.ID, state.JobStateFailed, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if h.transcodeCalls != 0 {
		t.Errorf("transcode should not run on rip failure, calls = %d", h.transcodeCalls)
	}
	// No transcode child should exist.
	jobs2, _ := store.ListJobs(context.Background(), state.JobFilter{})
	for _, j := range jobs2 {
		if j.Kind == state.JobKindTranscode {
			t.Errorf("found transcode job %s after rip failure", j.ID)
		}
	}
}

func TestOrchestrator_SplittablePath_TranscodeFailureLeavesSpool(t *testing.T) {
	store, bc, sp, h := openSplit(t)
	defer bc.Close()
	h.failOnTranscode = errors.New("encoder exploded")

	reg := pipelines.NewRegistry()
	reg.Register(h)
	compute := jobs.NewCompute(jobs.ComputeConfig{
		Store: store, Broadcaster: bc, Pipelines: reg, Spool: sp, Concurrency: 1,
	})
	t.Cleanup(compute.Close)
	o := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store: store, Broadcaster: bc, Pipelines: reg,
		Compute: compute, Spool: sp,
	})
	t.Cleanup(o.Close)

	_, disc, prof := seedSplitInputs(t, store)
	ripJob, err := o.Submit(context.Background(), disc.ID, prof.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitJobState(store, ripJob.ID, state.JobStateDone, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	transcodeID := waitForChild(t, store, ripJob.ID, 2*time.Second)
	if err := waitJobState(store, transcodeID, state.JobStateFailed, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	// Spool must survive a failed transcode so the user can retry.
	child, _ := store.GetJob(context.Background(), transcodeID)
	if _, err := stat(child.SpoolPath); err != nil {
		t.Errorf("spool dir %s should survive transcode failure: %v", child.SpoolPath, err)
	}
}

// TestOrchestrator_FallbackPath_NonSplittableUsesMonolithicRun verifies
// the backward-compat path: a plain Handler (no SplittableHandler
// implementation) routes through handler.Run even when Compute+Spool
// are wired into the orchestrator.
func TestOrchestrator_FallbackPath_NonSplittableUsesMonolithicRun(t *testing.T) {
	store, bc, h := openOrch(t)
	defer bc.Close()
	sp, _ := spool.New(filepath.Join(t.TempDir(), "spool"))
	reg := pipelines.NewRegistry()
	reg.Register(h)
	compute := jobs.NewCompute(jobs.ComputeConfig{
		Store: store, Broadcaster: bc, Pipelines: reg, Spool: sp, Concurrency: 1,
	})
	t.Cleanup(compute.Close)
	o := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store: store, Broadcaster: bc, Pipelines: reg,
		Compute: compute, Spool: sp,
	})
	t.Cleanup(o.Close)

	_, disc, prof := seedJobInputs(t, store)
	job, err := o.Submit(context.Background(), disc.ID, prof.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := waitJobState(store, job.ID, state.JobStateDone, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if h.calls != 1 {
		t.Errorf("monolithic Run calls = %d, want 1", h.calls)
	}
	// No transcode child should be created.
	jobs2, _ := store.ListJobs(context.Background(), state.JobFilter{})
	for _, j := range jobs2 {
		if j.Kind == state.JobKindTranscode {
			t.Errorf("transcode job %s leaked from monolithic path", j.ID)
		}
	}
}

// TestCompute_ConcurrencyLimits asserts the sem actually caps parallel
// transcodes. Concurrency=1 + 2 enqueued jobs → only 1 in-flight at a
// time. Uses atomic counter to detect any overlap.
func TestCompute_ConcurrencyLimits(t *testing.T) {
	store, bc, sp, h := openSplit(t)
	defer bc.Close()
	h.transcodeDelay = 80 * time.Millisecond
	h.transcodeStarted = make(chan struct{}, 4)

	var inFlight, maxInFlight atomic.Int32

	// Wrap the stub's RunTranscode to track concurrency. We can't easily
	// inject this through the existing stub, so use the started channel
	// + a small sleep + the delay to observe overlap.
	reg := pipelines.NewRegistry()
	reg.Register(h)
	compute := jobs.NewCompute(jobs.ComputeConfig{
		Store: store, Broadcaster: bc, Pipelines: reg, Spool: sp, Concurrency: 1,
	})
	t.Cleanup(compute.Close)
	o := jobs.NewOrchestrator(jobs.OrchestratorConfig{
		Store: store, Broadcaster: bc, Pipelines: reg,
		Compute: compute, Spool: sp,
	})
	t.Cleanup(o.Close)

	_, disc, prof := seedSplitInputs(t, store)

	// Issue two rip submits back-to-back. Per-drive serialisation runs
	// the second rip after the first done; the transcodes serialise on
	// the compute sem (concurrency=1).
	for i := 0; i < 2; i++ {
		if _, err := o.Submit(context.Background(), disc.ID, prof.ID); err != nil {
			t.Fatal(err)
		}
	}

	// Drain the started channel until both transcodes have fired,
	// recording max-overlap as we go.
	go func() {
		for range h.transcodeStarted {
			n := inFlight.Add(1)
			if n > maxInFlight.Load() {
				maxInFlight.Store(n)
			}
			time.AfterFunc(h.transcodeDelay, func() { inFlight.Add(-1) })
		}
	}()

	// Wait for both transcode jobs to reach done.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		all, _ := store.ListJobs(context.Background(), state.JobFilter{})
		done := 0
		for _, j := range all {
			if j.Kind == state.JobKindTranscode && j.State == state.JobStateDone {
				done++
			}
		}
		if done == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got := maxInFlight.Load(); got > 1 {
		t.Errorf("concurrency cap broken: maxInFlight = %d, want 1", got)
	}
}

// waitForChild polls for a kind='transcode' job whose ParentJobID is
// the given rip-job ID, returning the child's ID. Tests use this to
// fish out the auto-created transcode row.
func waitForChild(t *testing.T, store *state.Store, parentID string, timeout time.Duration) string {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		all, _ := store.ListJobs(context.Background(), state.JobFilter{})
		for _, j := range all {
			if j.Kind == state.JobKindTranscode && j.ParentJobID == parentID {
				return j.ID
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no transcode child appeared for parent %s within %s", parentID, timeout)
	return ""
}

// stat is a tiny test helper so test files don't all need to import os.
func stat(path string) (any, error) {
	return os.Stat(path)
}
