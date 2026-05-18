// Package pipelines defines the per-disc-type Handler contract that
// the orchestrator dispatches to. Handlers are pure Go code per disc
// type (audio CD, DVD, BDMV, ...) and compose the tools/* wrappers to
// implement the canonical 8-step pipeline.
package pipelines

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

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

// EpisodeAssignment is the per-title TV-episode metadata the picker
// persisted onto disc.metadata_json under selected_title_episodes.
// Used by BDMV/UHD/DVD multi-title rip to populate the OutputFields
// EpisodeNumber + EpisodeTitle so output paths render as the
// canonical `Show - S##E## - Episode Title` shape.
type EpisodeAssignment struct {
	Episode      int
	EpisodeTitle string
}

// SelectedSeasonFromDisc returns the season number the picker
// recorded for a TV rip, or 0 when unset. Stored under the
// "selected_season" key in metadata_json.
func SelectedSeasonFromDisc(disc *state.Disc) int {
	if disc == nil || disc.MetadataJSON == "" || disc.MetadataJSON == "{}" {
		return 0
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(disc.MetadataJSON), &blob); err != nil {
		return 0
	}
	raw, ok := blob["selected_season"]
	if !ok {
		return 0
	}
	switch n := raw.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

// SelectedEpisodeMapFromDisc returns the per-title episode
// assignments the picker recorded, keyed by title ID. Empty map
// when no mapping was persisted (movie rip, audio CD, picker-less
// rip, etc.). Caller decides whether to fall back to title-index
// based EpisodeNumber.
func SelectedEpisodeMapFromDisc(disc *state.Disc) map[int]EpisodeAssignment {
	if disc == nil || disc.MetadataJSON == "" || disc.MetadataJSON == "{}" {
		return nil
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(disc.MetadataJSON), &blob); err != nil {
		return nil
	}
	raw, ok := blob["selected_title_episodes"]
	if !ok {
		return nil
	}
	asMap, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[int]EpisodeAssignment, len(asMap))
	for k, v := range asMap {
		// Title IDs are strings on the wire (JSON object keys); parse
		// back to int so callers can match against TitleInfo.ID.
		var tid int
		if _, err := fmt.Sscanf(k, "%d", &tid); err != nil {
			continue
		}
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		ea := EpisodeAssignment{}
		if f, ok := entry["episode"].(float64); ok {
			ea.Episode = int(f)
		}
		if s, ok := entry["title"].(string); ok {
			ea.EpisodeTitle = s
		}
		out[tid] = ea
	}
	return out
}

// SelectedTitleIDsFromDisc returns the user-picked title IDs from a
// disc's metadata_json blob (key: "selected_title_ids"). Returns nil
// when the key is absent, empty, or malformed — callers fall back to
// their default auto-pick behaviour in that case. The conversion is
// permissive: JSON numbers decode as float64, and the slice can be
// of any numeric type.
func SelectedTitleIDsFromDisc(disc *state.Disc) []int {
	if disc == nil || disc.MetadataJSON == "" || disc.MetadataJSON == "{}" {
		return nil
	}
	var blob map[string]any
	if err := json.Unmarshal([]byte(disc.MetadataJSON), &blob); err != nil {
		return nil
	}
	raw, ok := blob["selected_title_ids"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]int, 0, len(arr))
	for _, v := range arr {
		switch n := v.(type) {
		case float64:
			out = append(out, int(n))
		case int:
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// TitleInfo describes one selectable title from a MakeMKV / HandBrake
// scan. Carries enough metadata for the picker UI to render a useful
// row (duration, chapter count) and enough for a subsequent rip to
// reference the title by its tool-specific ID.
type TitleInfo struct {
	ID           int    `json:"id"`                      // tool's title id (MakeMKV tnum, HandBrake title number)
	DurationSec  int    `json:"duration_sec"`            // total runtime
	ChapterCount int    `json:"chapter_count,omitempty"` // 0 when the tool doesn't report it
	SourceFile   string `json:"source_file,omitempty"`   // .mpls / .ifo / .m2ts hint (BDMV) — useful for box-set debugging
	SizeBytes    int64  `json:"size_bytes,omitempty"`    // best-effort byte estimate (MakeMKV reports this)
}

// TitleScanner is the optional interface a handler implements when it
// supports pre-rip title enumeration for the picker flow. The
// orchestrator type-asserts on this when running a kind='scan' job.
// Handlers that don't implement it surface a clear "not supported"
// error so the picker UI can hide itself for that disc type.
//
// ScanTitles is drive-bound and brief (~30-60s on MakeMKV BD; <10s on
// HandBrake DVD-direct). It must not eject — the disc stays in the
// drive for the subsequent rip the user kicks off from the picker.
type TitleScanner interface {
	Handler
	ScanTitles(ctx context.Context, drv *state.Drive, disc *state.Disc, profile *state.Profile, sink EventSink) ([]TitleInfo, error)
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
