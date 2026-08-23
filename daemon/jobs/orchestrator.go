package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/pipelines"
	"github.com/jumpingmushroom/DiscEcho/daemon/spool"
	"github.com/jumpingmushroom/DiscEcho/daemon/state"
	"github.com/jumpingmushroom/DiscEcho/daemon/tools"
)

// OrchestratorConfig configures NewOrchestrator.
//
// Compute + Spool are optional: when nil the orchestrator routes every
// handler through the monolithic Run path, even handlers that
// implement SplittableHandler. The splittable path requires both
// (Spool to allocate the staging dir, Compute to drain the resulting
// transcode jobs).
//
// CapBytesFunc returns the current spool cap (bytes). When non-nil
// and Spool is wired, the per-drive worker pauses new rip-job picks
// while Spool.UsageBytes >= CapBytesFunc(). nil disables
// backpressure entirely (legacy behaviour). The func is called once
// per pause-check so a setting reload takes effect without a daemon
// restart.
type OrchestratorConfig struct {
	Store        *state.Store
	Broadcaster  *state.Broadcaster
	Pipelines    *pipelines.Registry
	Compute      *Compute
	Spool        *spool.Spool
	CapBytesFunc func() int64
	// Tools + URLsForTrigger drive rip-completion notifications sent at
	// finalise. Nil tools / nil func / no subscribed URLs → no-op.
	Tools          *tools.Registry
	URLsForTrigger func(ctx context.Context, trigger string) []string
}

// Orchestrator owns job lifecycle: queueing, per-drive serialization,
// ctx cancellation, and the PersistentSink wiring.
type Orchestrator struct {
	cfg OrchestratorConfig

	mu       sync.Mutex
	queues   map[string]chan jobItem // per-drive
	cancels  map[string]context.CancelFunc
	stopOnce sync.Once
	stopped  chan struct{}
	wg       sync.WaitGroup
}

type jobItem struct {
	jobID string
}

// NewOrchestrator constructs an Orchestrator. Marks any
// queued/identifying/running jobs as interrupted (crash recovery).
func NewOrchestrator(cfg OrchestratorConfig) *Orchestrator {
	o := &Orchestrator{
		cfg:     cfg,
		queues:  make(map[string]chan jobItem),
		cancels: make(map[string]context.CancelFunc),
		stopped: make(chan struct{}),
	}
	if n, err := cfg.Store.MarkInterruptedJobs(context.Background()); err != nil {
		slog.Warn("orchestrator: MarkInterruptedJobs", "err", err)
	} else if n > 0 {
		slog.Info("orchestrator: marked interrupted jobs", "count", n)
	}
	return o
}

// Close stops every per-drive worker. Idempotent.
//
// We don't close the per-drive channels; closing them races with
// Submit (which sends on them). Instead we close o.stopped, which
// every worker selects on, and discard the queues map so future
// Submits return errStopped without sending. Pending items in the
// channels are dropped — the worker observes <-o.stopped first.
//
// In-flight jobs get their per-job context cancelled (mirroring
// Compute.Close) so a rip mid-handler.Run winds down and reaches a
// terminal state instead of blocking wg.Wait() for the rip's full
// duration — without this, SIGTERM hangs until docker force-kills the
// process. A cancelled handler returns ctx.Err(), so runJob writes the
// job `cancelled`.
func (o *Orchestrator) Close() {
	o.stopOnce.Do(func() {
		o.mu.Lock()
		o.queues = nil
		for _, cancel := range o.cancels {
			cancel()
		}
		o.mu.Unlock()
		close(o.stopped)
		o.wg.Wait()
	})
}

// Submit creates a job and enqueues it on the disc's drive's worker.
// Returns the persisted Job (state=queued).
func (o *Orchestrator) Submit(ctx context.Context, discID, profileID string) (*state.Job, error) {
	disc, err := o.cfg.Store.GetDisc(ctx, discID)
	if err != nil {
		return nil, fmt.Errorf("get disc: %w", err)
	}
	prof, err := o.cfg.Store.GetProfile(ctx, profileID)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	handler, ok := o.cfg.Pipelines.Get(disc.Type)
	if !ok {
		return nil, fmt.Errorf("no handler registered for %s", disc.Type)
	}

	driveID := disc.DriveID
	if driveID == "" {
		return nil, errors.New("submit: disc has no drive_id; cannot queue")
	}

	plan := handler.Plan(disc, prof)
	steps := make([]state.JobStep, 0, len(plan))
	for _, sp := range plan {
		st := state.JobStepStatePending
		if sp.Skip {
			st = state.JobStepStateSkipped
		}
		steps = append(steps, state.JobStep{Step: sp.ID, State: st})
	}
	job := &state.Job{
		DiscID:    disc.ID,
		DriveID:   driveID,
		ProfileID: prof.ID,
		State:     state.JobStateQueued,
		Steps:     steps,
	}
	if err := o.cfg.Store.CreateJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create job: %w", err)
	}
	o.cfg.Broadcaster.Publish(state.Event{
		Name: "job.created", Payload: map[string]any{"job": job},
	})

	if err := o.enqueue(driveID, job.ID); err != nil {
		return nil, err
	}
	return job, nil
}

// SubmitScan creates a kind='scan' job for the given disc + profile
// and enqueues it on the disc's per-drive worker. Rejects with a
// clear error when the handler doesn't implement TitleScanner so the
// picker UI can hide itself for unsupported disc types.
func (o *Orchestrator) SubmitScan(ctx context.Context, discID, profileID string) (*state.Job, error) {
	disc, err := o.cfg.Store.GetDisc(ctx, discID)
	if err != nil {
		return nil, fmt.Errorf("get disc: %w", err)
	}
	prof, err := o.cfg.Store.GetProfile(ctx, profileID)
	if err != nil {
		return nil, fmt.Errorf("get profile: %w", err)
	}
	handler, ok := o.cfg.Pipelines.Get(disc.Type)
	if !ok {
		return nil, fmt.Errorf("no handler registered for %s", disc.Type)
	}
	if _, ok := handler.(pipelines.TitleScanner); !ok {
		return nil, fmt.Errorf("disc type %s does not support title scan", disc.Type)
	}

	driveID := disc.DriveID
	if driveID == "" {
		return nil, errors.New("submit scan: disc has no drive_id; cannot queue")
	}

	job := &state.Job{
		DiscID:    disc.ID,
		DriveID:   driveID,
		ProfileID: prof.ID,
		Kind:      state.JobKindScan,
		State:     state.JobStateQueued,
	}
	if err := o.cfg.Store.CreateJob(ctx, job); err != nil {
		return nil, fmt.Errorf("create scan job: %w", err)
	}
	o.cfg.Broadcaster.Publish(state.Event{
		Name: "job.created", Payload: map[string]any{"job": job},
	})

	if err := o.enqueue(driveID, job.ID); err != nil {
		return nil, err
	}
	return job, nil
}

// Cancel signals the running job to stop. If the job is queued (not
// yet picked up) it's flipped to cancelled in the store so the worker
// skips it when it pops. Transcode-kind jobs live in the Compute
// pool's cancel map, so we route to whichever pool owns the job.
func (o *Orchestrator) Cancel(jobID string) error {
	o.mu.Lock()
	cancel, ok := o.cancels[jobID]
	o.mu.Unlock()
	if ok {
		cancel()
		return nil
	}
	if o.cfg.Compute != nil && o.cfg.Compute.Owns(jobID) {
		return o.cfg.Compute.Cancel(jobID)
	}
	if err := o.cfg.Store.CancelJobIfActive(context.Background(), jobID); err != nil {
		return fmt.Errorf("cancel: %w", err)
	}
	return nil
}

// CancelAll is the kill-switch: cancel every job currently in a
// non-terminal state (queued/identifying/running/paused), across every
// drive. Reuses Cancel's per-job routing (in-memory cancel map, Compute
// pool, or a bare store write for a queued job) so the same "which pool
// owns this job" logic isn't duplicated. One job's cancel failing
// doesn't stop the rest — this exists specifically for a wedged queue,
// so it should clear everything it can rather than bailing on the
// first error. Returns how many jobs it attempted to cancel and the
// first error encountered, if any.
func (o *Orchestrator) CancelAll(ctx context.Context) (int, error) {
	ids, err := o.cfg.Store.ListActiveJobIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("list active jobs: %w", err)
	}
	var firstErr error
	for _, id := range ids {
		if err := o.Cancel(id); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return len(ids), firstErr
}

// errStopped is returned by enqueue when Submit races shutdown.
var errStopped = errors.New("orchestrator: stopped")

func (o *Orchestrator) enqueue(driveID, jobID string) error {
	o.mu.Lock()
	if o.queues == nil {
		o.mu.Unlock()
		return errStopped
	}
	q, ok := o.queues[driveID]
	if !ok {
		q = make(chan jobItem, 64)
		o.queues[driveID] = q
		o.wg.Add(1)
		go o.worker(driveID, q)
	}
	o.mu.Unlock()
	// Send outside the lock so Close/Cancel stay responsive when a
	// drive's queue is full. The queue is never closed (see Close),
	// so there's no closed-channel-send risk here; Close signals via
	// o.stopped instead.
	select {
	case q <- jobItem{jobID: jobID}:
		return nil
	case <-o.stopped:
		return errStopped
	}
}

// worker drains one drive's queue serially. Exits when o.stopped is
// closed; the queue itself is never closed (see Close). Backpressure:
// before each runJob, the worker waits for spool capacity so a slow
// transcode queue can't fill the disk while rips keep landing.
func (o *Orchestrator) worker(driveID string, q chan jobItem) {
	defer o.wg.Done()
	for {
		select {
		case <-o.stopped:
			return
		case item := <-q:
			if !o.waitForSpoolCapacity(driveID, item.jobID) {
				// Shutdown raced the wait; the job stays queued and is
				// flipped to interrupted on next startup.
				return
			}
			o.runJob(item.jobID)
		}
	}
}

// backpressurePollInterval is how often the per-drive worker re-checks
// spool usage when over-cap. 5s is short enough that drive responsiveness
// stays low-latency after a transcode finishes; long enough that an
// extended over-cap window doesn't burn CPU on filesystem walks.
const backpressurePollInterval = 5 * time.Second

// waitForSpoolCapacity blocks the per-drive worker until the spool is
// under cap, the shutdown signal fires, or backpressure is disabled
// (no Spool / no CapBytesFunc / cap<=0). Returns false if shutdown
// races the wait.
func (o *Orchestrator) waitForSpoolCapacity(driveID, jobID string) bool {
	if o.cfg.Spool == nil || o.cfg.CapBytesFunc == nil {
		return true
	}
	announced := false
	for {
		cap := o.cfg.CapBytesFunc()
		if cap <= 0 {
			return true
		}
		usage, err := o.cfg.Spool.UsageBytes(context.Background())
		if err != nil {
			slog.Warn("orchestrator: spool usage check", "err", err)
			return true
		}
		if usage < cap {
			if announced {
				slog.Info("orchestrator: spool under cap, resuming rip", "drv", driveID, "job", jobID, "usage", usage, "cap", cap)
				o.cfg.Broadcaster.Publish(state.Event{
					Name:    "spool.changed",
					Payload: map[string]any{"usage_bytes": usage, "cap_bytes": cap, "blocked": false},
				})
			}
			return true
		}
		if !announced {
			slog.Warn("orchestrator: spool over cap, pausing rip", "drv", driveID, "job", jobID, "usage", usage, "cap", cap)
			o.cfg.Broadcaster.Publish(state.Event{
				Name:    "spool.changed",
				Payload: map[string]any{"usage_bytes": usage, "cap_bytes": cap, "blocked": true},
			})
			announced = true
		}
		select {
		case <-time.After(backpressurePollInterval):
		case <-o.stopped:
			return false
		}
	}
}

// failEarly marks a job failed when a pre-flight lookup or the
// queued→running transition fails, before the handler ever runs. Without
// it the queue item is consumed but the row stays queued forever —
// invisible to retry and blocking eject/reclassify on its disc/drive
// until the next daemon restart flips it to interrupted. The drive is
// still idle at these points (ripping is set only after all lookups), so
// no drive-state reset is needed. Uses a fresh context because the
// per-job ctx may already be cancelled on the caller's path.
func (o *Orchestrator) failEarly(jobID string, cause error) {
	if uerr := o.cfg.Store.UpdateJobState(context.Background(), jobID, state.JobStateFailed, cause.Error()); uerr != nil {
		slog.Error("orchestrator: mark failed (early)", "id", jobID, "err", uerr)
	}
	o.cfg.Broadcaster.Publish(state.Event{
		Name:    "job.failed",
		Payload: map[string]any{"job_id": jobID, "error": cause.Error()},
	})
}

// runJob dispatches one job through its handler.
func (o *Orchestrator) runJob(jobID string) {
	ctx, cancel := context.WithCancel(context.Background())
	o.mu.Lock()
	o.cancels[jobID] = cancel
	o.mu.Unlock()
	defer func() {
		cancel()
		o.mu.Lock()
		delete(o.cancels, jobID)
		o.mu.Unlock()
	}()

	job, err := o.cfg.Store.GetJob(ctx, jobID)
	if err != nil {
		slog.Error("orchestrator: get job", "id", jobID, "err", err)
		o.failEarly(jobID, err)
		return
	}
	if job.State == state.JobStateCancelled {
		// User cancelled before the worker picked it up. Carry state:cancelled
		// so the webui renders CANCELLED, not FAILED (its job.failed handler
		// only downgrades to cancelled when the payload says so).
		o.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID, "state": "cancelled"}})
		return
	}

	disc, err := o.cfg.Store.GetDisc(ctx, job.DiscID)
	if err != nil {
		slog.Error("orchestrator: get disc", "id", jobID, "err", err)
		o.failEarly(jobID, err)
		return
	}
	drv, err := o.cfg.Store.GetDrive(ctx, job.DriveID)
	if err != nil {
		slog.Error("orchestrator: get drive", "id", jobID, "err", err)
		o.failEarly(jobID, err)
		return
	}
	prof, err := o.cfg.Store.GetProfile(ctx, job.ProfileID)
	if err != nil {
		slog.Error("orchestrator: get profile", "id", jobID, "err", err)
		o.failEarly(jobID, err)
		return
	}
	handler, ok := o.cfg.Pipelines.Get(disc.Type)
	if !ok {
		err := fmt.Errorf("no handler for %s", disc.Type)
		_ = o.cfg.Store.UpdateJobState(ctx, jobID, state.JobStateFailed, err.Error())
		o.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID}})
		return
	}

	// Transition: queued → running
	if err := o.cfg.Store.UpdateJobState(ctx, jobID, state.JobStateRunning, ""); err != nil {
		slog.Error("orchestrator: state running", "id", jobID, "err", err)
		o.failEarly(jobID, err)
		return
	}
	if err := o.cfg.Store.UpdateDriveState(ctx, drv.ID, state.DriveStateRipping); err != nil {
		slog.Warn("orchestrator: drive state ripping", "drv", drv.ID, "err", err)
	}
	o.cfg.Broadcaster.Publish(state.Event{Name: "drive.changed", Payload: map[string]any{"drive_id": drv.ID, "state": "ripping"}})

	sink := NewPersistentSink(o.cfg.Store, o.cfg.Broadcaster, jobID)

	// Route by job kind. scan: a brief title-enumeration job that
	// populates disc.metadata_json.scan and doesn't eject. rip
	// (default): splittable path when wired, else the monolithic Run
	// fallback.
	var runErr error
	// wentSplit: the rip went down the splittable path, which enqueued a
	// transcode child. The child's completion (Compute.finalise) is the
	// user-facing "done", so this rip-half job must NOT send a success
	// notification — only a failure (the child never runs if the rip failed).
	wentSplit := false
	switch job.Kind {
	case state.JobKindScan:
		scanner, ok := handler.(pipelines.TitleScanner)
		if !ok {
			runErr = fmt.Errorf("scan: handler for %s does not implement TitleScanner", disc.Type)
		} else {
			runErr = o.runScan(ctx, scanner, drv, disc, prof, sink)
		}
	default:
		if splittable, ok := handler.(pipelines.SplittableHandler); ok && o.cfg.Compute != nil && o.cfg.Spool != nil {
			wentSplit = true
			runErr = o.runSplittable(ctx, splittable, drv, disc, prof, job, sink)
		} else {
			runErr = handler.Run(ctx, drv, disc, prof, sink)
		}
	}

	// Determine final state
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
	// Final state writes use a fresh context: the per-job ctx may have
	// been cancelled (cancellation path) and we still need to persist
	// the terminal state.
	if err := o.cfg.Store.UpdateJobState(context.Background(), jobID, final, errMsg); err != nil {
		slog.Error("orchestrator: state final", "id", jobID, "err", err)
	}
	if err := o.cfg.Store.UpdateDriveState(context.Background(), drv.ID, state.DriveStateIdle); err != nil {
		slog.Warn("orchestrator: drive state idle", "drv", drv.ID, "err", err)
	}
	o.cfg.Broadcaster.Publish(state.Event{Name: "drive.changed", Payload: map[string]any{"drive_id": drv.ID, "state": "idle"}})

	switch final {
	case state.JobStateDone:
		o.cfg.Broadcaster.Publish(state.Event{Name: "job.done", Payload: map[string]any{"job_id": jobID}})
	case state.JobStateCancelled:
		o.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID, "state": "cancelled"}})
	case state.JobStateFailed:
		o.cfg.Broadcaster.Publish(state.Event{Name: "job.failed", Payload: map[string]any{"job_id": jobID, "error": errMsg}})
	}

	// Notifications. Scans never notify. A splittable rip's success is
	// announced by its transcode child (Compute.finalise), so we suppress the
	// success notification here when wentSplit; failures always notify (the
	// child never runs after a rip-half failure). Cancelled → no notify.
	if job.Kind != state.JobKindScan {
		if final == state.JobStateFailed || (final == state.JobStateDone && !wentSplit) {
			sendTerminalNotification(o.cfg.Tools, o.cfg.URLsForTrigger, o.cfg.Store, jobID, disc, prof, final)
		}
	}
}

// runScan executes a kind='scan' job: drive-bound title enumeration
// that doesn't eject. Results land on disc.metadata_json under the
// "scan" key with a "titles" array and "scanned_at" timestamp; the
// dashboard's picker UI reads from there once the disc.changed SSE
// event fires.
func (o *Orchestrator) runScan(
	ctx context.Context,
	handler pipelines.TitleScanner,
	drv *state.Drive, disc *state.Disc, prof *state.Profile,
	sink pipelines.EventSink,
) error {
	titles, err := handler.ScanTitles(ctx, drv, disc, prof, sink)
	if err != nil {
		return err
	}

	// Merge the scan results into the existing metadata blob so other
	// keys (cover_url, dvd_titles, …) survive.
	merged := map[string]any{}
	if disc.MetadataJSON != "" && disc.MetadataJSON != "{}" {
		_ = json.Unmarshal([]byte(disc.MetadataJSON), &merged)
	}
	merged["scan"] = map[string]any{
		"titles":     titles,
		"scanned_at": time.Now().UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(merged)
	if err != nil {
		return fmt.Errorf("scan: marshal metadata: %w", err)
	}
	if err := o.cfg.Store.UpdateDiscMetadataBlob(ctx, disc.ID, string(body)); err != nil {
		return fmt.Errorf("scan: persist metadata: %w", err)
	}
	o.cfg.Broadcaster.Publish(state.Event{
		Name: "disc.changed",
		Payload: map[string]any{
			"id":            disc.ID,
			"metadata_json": string(body),
		},
	})
	return nil
}

// runSplittable drives the drive-bound half of a split pipeline: it
// allocates the spool dir, hands the rip-job sink to the handler's
// RunRip, and on success enqueues a child kind='transcode' job onto
// the Compute pool. The drive is freed when this function returns.
//
// A failed RunRip cleans up the spool dir (no transcode job will run
// against it) and bubbles the error so the caller flips the job to
// failed. A succeeded RunRip leaves the spool intact for the compute
// worker; that worker owns cleanup on transcode-done.
func (o *Orchestrator) runSplittable(
	ctx context.Context,
	handler pipelines.SplittableHandler,
	drv *state.Drive, disc *state.Disc, prof *state.Profile,
	ripJob *state.Job, sink pipelines.EventSink,
) error {
	spoolPath, err := o.cfg.Spool.Create(ripJob.ID)
	if err != nil {
		return fmt.Errorf("spool create: %w", err)
	}

	result, runErr := handler.RunRip(ctx, drv, disc, prof, spoolPath, sink)
	if runErr != nil {
		// Spool cleanup best-effort: a half-written rip dir is not useful
		// to anyone and would otherwise count against the soft cap.
		if cleanupErr := o.cfg.Spool.Cleanup(ripJob.ID); cleanupErr != nil {
			slog.Warn("orchestrator: spool cleanup after rip failure", "id", ripJob.ID, "err", cleanupErr)
		}
		return runErr
	}
	// Default the spool path to the one we allocated when the handler
	// didn't override it. Handlers normally just write into the
	// allocated dir and return it; preserving the override keeps the
	// door open for handlers that stash output elsewhere (e.g. a future
	// network-mount mode).
	if result.SpoolPath == "" {
		result.SpoolPath = spoolPath
	}

	// Materialise the child transcode job + enqueue it.
	transcodePlan := handler.PlanTranscode(disc, prof)
	steps := make([]state.JobStep, 0, len(transcodePlan))
	for _, sp := range transcodePlan {
		st := state.JobStepStatePending
		if sp.Skip {
			st = state.JobStepStateSkipped
		}
		steps = append(steps, state.JobStep{Step: sp.ID, State: st})
	}
	child := &state.Job{
		DiscID:      disc.ID,
		ProfileID:   prof.ID,
		Kind:        state.JobKindTranscode,
		ParentJobID: ripJob.ID,
		SpoolPath:   result.SpoolPath,
		State:       state.JobStateQueued,
		Steps:       steps,
	}
	if err := o.cfg.Store.CreateJob(ctx, child); err != nil {
		// Spool stays — the user can manually retry by re-running the rip,
		// at which point the new rip job's spool dir takes precedence.
		return fmt.Errorf("create transcode job: %w", err)
	}
	o.cfg.Broadcaster.Publish(state.Event{
		Name:    "job.created",
		Payload: map[string]any{"job": child},
	})
	if err := o.cfg.Compute.Enqueue(child.ID); err != nil {
		return fmt.Errorf("compute enqueue: %w", err)
	}
	return nil
}
