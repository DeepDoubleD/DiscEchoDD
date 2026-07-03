package jobs

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/spool"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// ComputeConfig wires the global transcode-queue worker pool.
type ComputeConfig struct {
	Store       *state.Store
	Broadcaster *state.Broadcaster
	Pipelines   *pipelines.Registry
	Spool       *spool.Spool

	// Concurrency caps the number of transcode handlers that may run
	// in parallel. <=0 normalises to 1 (matches the seeded
	// compute.concurrent_encodes default and is the safest starting
	// posture on shared hosts).
	Concurrency int

	// Tools + URLsForTrigger drive the rip-completion notification sent
	// when a transcode child reaches its terminal state (the true
	// user-facing completion for splittable handlers). Nil → no-op.
	Tools          *tools.Registry
	URLsForTrigger func(ctx context.Context, trigger string) []string
}

// maxComputeWorkers caps the worker-pool size. The actual number of
// in-flight transcodes is governed by limit (atomic, settable via
// SetConcurrency). Setting maxComputeWorkers higher than the UI's
// "Concurrent encodes" max (currently 8) is wasteful but harmless;
// matching the UI cap keeps idle goroutine count predictable.
const maxComputeWorkers = 8

// Compute is the post-drive worker pool. Splittable pipelines' rip
// phases run on the per-drive orchestrator queue; once a rip job
// succeeds the orchestrator creates a kind='transcode' child job and
// calls Compute.Enqueue with its ID. The pool drains the queue at
// limit transcodes in parallel.
//
// Live concurrency reload: SetConcurrency(n) updates the atomic
// limit; existing workers respect the new value on next acquire.
// The worker pool size itself is fixed at maxComputeWorkers, so
// raising limit takes effect immediately without spawning new
// goroutines.
type Compute struct {
	cfg ComputeConfig

	queue chan computeItem

	// Concurrency control. limit is the max number of in-flight
	// transcodes; inFlight tracks the current count. acquire()
	// CAS-bumps inFlight under limit; release() decrements + wakes
	// waiters via wake. wake is a buffered chan so a single
	// release notifies one waiter; SetConcurrency-raise broadcasts
	// by sending up to limit wakeups.
	limit    atomic.Int64
	inFlight atomic.Int64
	wake     chan struct{}

	mu       sync.Mutex
	cancels  map[string]context.CancelFunc
	stopOnce sync.Once
	stopped  chan struct{}
	wg       sync.WaitGroup
}

type computeItem struct {
	jobID string
}

// NewCompute constructs a Compute and spawns maxComputeWorkers worker
// goroutines. Workers run until Close. Concurrency<=0 normalises to 1
// and is clamped at maxComputeWorkers.
func NewCompute(cfg ComputeConfig) *Compute {
	conc := normaliseConcurrency(cfg.Concurrency)
	c := &Compute{
		cfg: cfg,
		// Queue buffer matches the orchestrator's per-drive buffer (64).
		// At a steady-state max-concurrency rip rate, the queue holds the
		// backlog without blocking the orchestrator's enqueue side.
		queue:   make(chan computeItem, 64),
		wake:    make(chan struct{}, maxComputeWorkers),
		cancels: make(map[string]context.CancelFunc),
		stopped: make(chan struct{}),
	}
	c.limit.Store(int64(conc))
	for i := 0; i < maxComputeWorkers; i++ {
		c.wg.Add(1)
		go c.worker()
	}
	return c
}

// normaliseConcurrency clamps a requested value to [1, maxComputeWorkers].
func normaliseConcurrency(n int) int {
	if n <= 0 {
		return 1
	}
	if n > maxComputeWorkers {
		return maxComputeWorkers
	}
	return n
}

// SetConcurrency live-reloads the in-flight cap. n is clamped to
// [1, maxComputeWorkers]. Existing in-flight transcodes are unaffected
// (they finish naturally); the next acquire honours the new limit.
// Raising the limit wakes up to limit waiters so backlogged jobs can
// start immediately.
func (c *Compute) SetConcurrency(n int) {
	old := c.limit.Load()
	next := int64(normaliseConcurrency(n))
	if old == next {
		return
	}
	c.limit.Store(next)
	slog.Info("compute: concurrency updated", "old", old, "new", next)
	// Broadcast up to `next` wakeups in case multiple workers are
	// waiting; each non-blocking send wakes one waiter.
	for i := int64(0); i < next; i++ {
		select {
		case c.wake <- struct{}{}:
		default:
		}
	}
}

// Concurrency returns the current cap. Mostly for tests + diagnostics.
func (c *Compute) Concurrency() int { return int(c.limit.Load()) }

// acquire blocks until inFlight is under limit, then bumps inFlight
// atomically. Cancellation comes from ctx (per-job context) or stopped
// (Close). Hot path is a single atomic CAS; the wake channel sleeps
// the goroutine when the queue is at the cap.
func (c *Compute) acquire(ctx context.Context) error {
	for {
		cur := c.inFlight.Load()
		lim := c.limit.Load()
		if cur < lim && c.inFlight.CompareAndSwap(cur, cur+1) {
			return nil
		}
		select {
		case <-c.wake:
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopped:
			return errStopped
		}
	}
}

// release decrements inFlight and notifies one waiter.
func (c *Compute) release() {
	c.inFlight.Add(-1)
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Close stops every worker. Idempotent. In-flight transcodes get their
// per-job context cancelled so handlers wind down cleanly; pending
// queue items are dropped.
func (c *Compute) Close() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		for _, cancel := range c.cancels {
			cancel()
		}
		c.mu.Unlock()
		close(c.stopped)
		c.wg.Wait()
	})
}

// Enqueue schedules a transcode job for execution. The queue is
// bounded; if a producer races shutdown the call returns errStopped
// rather than blocking forever.
func (c *Compute) Enqueue(jobID string) error {
	if jobID == "" {
		return errors.New("compute: empty jobID")
	}
	select {
	case c.queue <- computeItem{jobID: jobID}:
		return nil
	case <-c.stopped:
		return errStopped
	}
}

// Cancel signals a running compute job to stop. If the job is queued
// but not yet running it's flipped to cancelled in the store so the
// worker skips it when popped. This mirrors Orchestrator.Cancel so
// callers can route a single Cancel through whichever pool owns the
// job — see Orchestrator.Cancel for the per-job lookup.
func (c *Compute) Cancel(jobID string) error {
	c.mu.Lock()
	cancel, ok := c.cancels[jobID]
	c.mu.Unlock()
	if !ok {
		if err := c.cfg.Store.CancelJobIfActive(context.Background(), jobID); err != nil {
			return fmt.Errorf("compute cancel: %w", err)
		}
		return nil
	}
	cancel()
	return nil
}

// Owns reports whether a given jobID is currently running on this
// pool's workers. Used by Orchestrator.Cancel to route a cancel to
// whichever pool actually holds the job.
func (c *Compute) Owns(jobID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.cancels[jobID]
	return ok
}

func (c *Compute) worker() {
	defer c.wg.Done()
	for {
		select {
		case <-c.stopped:
			return
		case item := <-c.queue:
			c.runOne(item.jobID)
		}
	}
}

// runOne owns the per-job lifecycle: acquire a concurrency slot,
// load the job + sibling rows, dispatch to the splittable handler's
// RunTranscode, persist terminal state, clean up the spool on success.
func (c *Compute) runOne(jobID string) {
	// Acquire concurrency slot first so a backlog of jobs at
	// concurrency=1 doesn't fan out simultaneous loads from the store.
	if err := c.acquire(context.Background()); err != nil {
		return
	}
	defer c.release()

	ctx, cancel := context.WithCancel(context.Background())
	c.mu.Lock()
	c.cancels[jobID] = cancel
	c.mu.Unlock()
	defer func() {
		cancel()
		c.mu.Lock()
		delete(c.cancels, jobID)
		c.mu.Unlock()
	}()

	job, err := c.cfg.Store.GetJob(ctx, jobID)
	if err != nil {
		slog.Error("compute: get job", "id", jobID, "err", err)
		// Strand-proof: mark failed so the consumed queue item doesn't
		// leave the row queued forever (matches the finalise paths below).
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}
	if job.Kind != state.JobKindTranscode {
		slog.Error("compute: refusing non-transcode job", "id", jobID, "kind", job.Kind)
		c.finalise(jobID, "", state.JobStateFailed, fmt.Sprintf("refusing non-transcode job (kind %s)", job.Kind))
		return
	}
	if job.State == state.JobStateCancelled {
		// Carry state:cancelled so the UI shows CANCELLED, not FAILED.
		c.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID, "state": "cancelled"}})
		return
	}

	disc, err := c.cfg.Store.GetDisc(ctx, job.DiscID)
	if err != nil {
		slog.Error("compute: get disc", "id", jobID, "err", err)
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}
	prof, err := c.cfg.Store.GetProfile(ctx, job.ProfileID)
	if err != nil {
		slog.Error("compute: get profile", "id", jobID, "err", err)
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}
	handler, ok := c.cfg.Pipelines.Get(disc.Type)
	if !ok {
		err := fmt.Errorf("no handler for %s", disc.Type)
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}
	splittable, ok := handler.(pipelines.SplittableHandler)
	if !ok {
		err := fmt.Errorf("handler for %s is not splittable", disc.Type)
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}
	if job.SpoolPath == "" {
		err := errors.New("transcode job has empty spool_path")
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}

	if err := c.cfg.Store.UpdateJobState(ctx, jobID, state.JobStateRunning, ""); err != nil {
		slog.Error("compute: state running", "id", jobID, "err", err)
		c.finalise(jobID, "", state.JobStateFailed, err.Error())
		return
	}
	c.cfg.Broadcaster.Publish(state.Event{Name: "job.started", Payload: map[string]any{"job_id": jobID}})

	sink := NewPersistentSink(c.cfg.Store, c.cfg.Broadcaster, jobID)
	runErr := splittable.RunTranscode(ctx, pipelines.RipResult{SpoolPath: job.SpoolPath}, disc, prof, sink)

	var final state.JobState
	errMsg := ""
	switch {
	case runErr == nil:
		final = state.JobStateDone
	case errors.Is(runErr, context.Canceled), errors.Is(runErr, context.DeadlineExceeded):
		final = state.JobStateCancelled
	default:
		final = state.JobStateFailed
		errMsg = runErr.Error()
	}
	c.finalise(jobID, job.SpoolPath, final, errMsg)

	// The transcode child finishing is the user-facing completion for a
	// splittable rip: notify on success or failure (cancelled → no-op inside).
	sendTerminalNotification(c.cfg.Tools, c.cfg.URLsForTrigger, c.cfg.Store, jobID, disc, prof, final)
}

// finalise writes the terminal job state and, on success, removes the
// spool dir. Failure leaves the spool in place so the user can retry
// the transcode without re-ripping the disc.
//
// Uses a fresh ctx for the state write because the per-job ctx may
// have been cancelled (cancellation path) and we still need to persist
// the terminal row.
func (c *Compute) finalise(jobID, spoolPath string, final state.JobState, errMsg string) {
	if err := c.cfg.Store.UpdateJobState(context.Background(), jobID, final, errMsg); err != nil {
		slog.Error("compute: state final", "id", jobID, "err", err)
	}
	switch final {
	case state.JobStateDone:
		if spoolPath != "" && c.cfg.Spool != nil {
			// The spool dir is named after the *parent rip job* ID; the
			// transcode job stores that path on its row. Cleanup keys
			// off the dir name (last path segment) so we can call
			// spool.Cleanup with the parent job ID.
			if err := c.cleanupSpool(spoolPath); err != nil {
				slog.Warn("compute: cleanup spool", "id", jobID, "path", spoolPath, "err", err)
			}
		}
		c.cfg.Broadcaster.Publish(state.Event{Name: "job.done", Payload: map[string]any{"job_id": jobID}})
	case state.JobStateCancelled:
		c.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID, "state": "cancelled"}})
	case state.JobStateFailed:
		c.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID, "error": errMsg}})
	}
}

// cleanupSpool resolves the parent rip-job ID from spoolPath (the last
// path segment) and removes the directory. Tolerates a nil spool for
// tests that don't wire one.
func (c *Compute) cleanupSpool(spoolPath string) error {
	if c.cfg.Spool == nil {
		return nil
	}
	// spoolPath comes from Spool.Path(parentJobID), so the basename is
	// exactly the parent rip job ID.
	parentID := basenameOf(spoolPath)
	if parentID == "" {
		return errors.New("compute: cannot derive parent ID from spool path")
	}
	return c.cfg.Spool.Cleanup(parentID)
}

func basenameOf(p string) string {
	for i := len(p) - 1; i >= 0; i-- {
		if p[i] == '/' || p[i] == '\\' {
			return p[i+1:]
		}
	}
	return p
}
