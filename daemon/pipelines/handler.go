// Package pipelines defines the per-disc-type Handler contract that
// the orchestrator dispatches to. Handlers are pure Go code per disc
// type (audio CD, DVD, BDMV, ...) and compose the tools/* wrappers to
// implement the canonical 8-step pipeline.
package pipelines

import (
	"context"
	"errors"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// ErrNoCandidates is returned by Handler.Identify when MB returns no
// matches (or the per-type lookup otherwise has nothing to offer).
// The orchestrator turns this into a job state of "failed" with an
// explanatory error_message.
var ErrNoCandidates = errors.New("pipelines: no candidates")

// Handler implements one disc type's pipeline.
type Handler interface {
	DiscType() state.DiscType

	// Identify probes the disc and returns base info + candidates.
	// ErrNoCandidates surfaces 0-match cases.
	Identify(ctx context.Context, drv *state.Drive) (*state.Disc, []state.Candidate, error)

	// Plan returns the ordered step list for UI rendering / DB
	// persistence. Includes skipped steps so the stepper renders all
	// 8 canonical positions. No execution happens here.
	Plan(disc *state.Disc, profile *state.Profile) []StepPlan

	// Run executes the pipeline. drv supplies the dev_path for eject
	// and any other drive-scoped operations. The handler owns its
	// temp dir, moves outputs, fires Apprise, ejects.
	Run(ctx context.Context, drv *state.Drive, disc *state.Disc, profile *state.Profile, sink EventSink) error
}

// StepPlan is one canonical-step descriptor used at job-creation time
// to materialize the job_steps rows.
type StepPlan struct {
	ID   state.StepID
	Skip bool
}

// RipResult is the artefact produced by SplittableHandler.RunRip and
// consumed by SplittableHandler.RunTranscode. SpoolPath points at the
// per-rip-job directory under ${DISCECHO_DATA}/spool/. Notes is an
// optional metadata bag (e.g. picked MakeMKV title id, redumper logs)
// forwarded onto the transcode job's runtime context — it's persisted
// onto the rip job's eject-step notes for crash-recovery readability
// and is also passed in-process to RunTranscode in the same daemon
// boot. After a daemon restart, the compute worker resumes by reading
// SpoolPath off the transcode job row; Notes is best-effort and may
// be empty in that case.
type RipResult struct {
	SpoolPath string
	Notes     map[string]any
}

// SplittableHandler is the optional extension a Handler implements when
// it supports the decoupled rip → transcode flow. The orchestrator
// type-asserts on this interface; handlers that don't implement it
// stay on the monolithic Run path (audio CD, DATA in v1).
//
// Contract:
//   - PlanRip returns the canonical 8-step plan for the rip-half job
//     row, with the transcode-half steps marked Skip=true. Keeping the
//     full 8 lets the UI stepper continue to render a uniform list.
//   - PlanTranscode returns the 4-step plan for the transcode-half
//     child job (transcode/compress/move/notify), with per-pipeline
//     Skip flags (e.g. UHD passes transcode=Skip, compress=Skip).
//   - RunRip executes the drive-bound steps and lands its output under
//     RipResult.SpoolPath. The drive is freed when RunRip returns.
//   - RunTranscode runs on the global compute queue, reads from
//     ripResult.SpoolPath, and is responsible for move + notify. Spool
//     cleanup is the compute worker's job, not the handler's.
type SplittableHandler interface {
	Handler

	PlanRip(disc *state.Disc, profile *state.Profile) []StepPlan
	PlanTranscode(disc *state.Disc, profile *state.Profile) []StepPlan

	// spoolDir is the absolute path the orchestrator pre-allocated for
	// this rip job's intermediate output. Handlers write into it and
	// typically return RipResult{SpoolPath: spoolDir}. Handlers may
	// override SpoolPath if they want the transcode worker to read
	// from a different path (e.g. a future network-mount mode).
	RunRip(ctx context.Context, drv *state.Drive, disc *state.Disc, profile *state.Profile, spoolDir string, sink EventSink) (RipResult, error)
	RunTranscode(ctx context.Context, ripResult RipResult, disc *state.Disc, profile *state.Profile, sink EventSink) error
}

// EventSink receives every event a Handler emits during Run.
// JobID identifies the job the sink is bound to — pipelines use it to
// attribute final output sizes back onto the right job row.
type EventSink interface {
	OnStepStart(stepID state.StepID)
	OnProgress(stepID state.StepID, pct float64, speed string, etaSeconds int)
	OnLog(level state.LogLevel, format string, args ...any)
	// OnSubStep records a long-running sub-phase within a step (e.g.
	// redumper's REFINE or SPLIT phase). Empty name clears the field.
	OnSubStep(name string)
	OnStepDone(stepID state.StepID, notes map[string]any)
	OnStepFailed(stepID state.StepID, err error)
	JobID() string
}
