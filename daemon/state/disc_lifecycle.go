package state

// DeriveLifecycleState computes a disc's lifecycle state from its
// jobs slice. Pure function — no DB access, fully unit-testable.
// Callers (Store.LifecycleStates, buildSnapshot) supply the jobs.
//
// jobs MUST be in any order; the function picks the latest rip and
// its associated transcode itself. Empty/nil jobs returns
// DiscLifecycleAwaitingDecision.
//
// "Latest attempt wins" — a disc with N failed rip attempts followed
// by a `done` rip is considered to be in whatever state its NEWEST
// rip (or its child transcode) implies. Older failures don't poison
// the current state.
//
// Job kinds:
//   - rip      — drive-bound; produces source files into spool/library
//   - transcode— compute-bound; reads from spool, encodes, moves
//   - scan     — short title-list probe (e.g. TitlePicker); ignored
//     here because it does not advance the disc journey
//   - "" (empty) — legacy rows from before JobKind shipped; treated as rip
func DeriveLifecycleState(t DiscType, jobs []Job) DiscLifecycleState {
	if len(jobs) == 0 {
		return DiscLifecycleAwaitingDecision
	}

	var latestRip *Job
	for i := range jobs {
		j := &jobs[i]
		kind := j.Kind
		if kind == "" {
			kind = JobKindRip
		}
		if kind != JobKindRip {
			continue
		}
		if latestRip == nil || j.CreatedAt.After(latestRip.CreatedAt) {
			latestRip = j
		}
	}
	if latestRip == nil {
		return DiscLifecycleAwaitingDecision
	}

	switch latestRip.State {
	case JobStateQueued, JobStateRunning, JobStateIdentifying, JobStatePaused:
		return DiscLifecycleRipping
	case JobStateFailed:
		return DiscLifecycleFailed
	case JobStateCancelled:
		return DiscLifecycleCancelled
	case JobStateInterrupted:
		return DiscLifecycleInterrupted
	}

	// latestRip.State == JobStateDone from here.
	if !t.HasTranscodeHalf() {
		return DiscLifecycleDone
	}

	var latestTranscode *Job
	for i := range jobs {
		j := &jobs[i]
		if j.Kind != JobKindTranscode || j.ParentJobID != latestRip.ID {
			continue
		}
		if latestTranscode == nil || j.CreatedAt.After(latestTranscode.CreatedAt) {
			latestTranscode = j
		}
	}
	if latestTranscode == nil {
		return DiscLifecycleAwaitingEncode
	}

	switch latestTranscode.State {
	case JobStateQueued, JobStateRunning, JobStateIdentifying, JobStatePaused:
		return DiscLifecycleEncoding
	case JobStateFailed:
		return DiscLifecycleFailed
	case JobStateCancelled:
		return DiscLifecycleCancelled
	case JobStateInterrupted:
		return DiscLifecycleInterrupted
	case JobStateDone:
		return DiscLifecycleDone
	}

	return DiscLifecycleAwaitingDecision
}
