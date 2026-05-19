package state_test

import (
	"context"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestStore_LifecycleStates_Batched(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	// Seed: two drives, three discs.
	// - DVD on drive A: 2 failed rips, 1 done rip, 1 running transcode child.
	// - PSX on drive B: single done rip (no transcode half).
	// - AudioCD on drive A: no jobs (awaiting decision).
	drvA := newDrive(t, s, "/dev/sr0")
	drvB := newDrive(t, s, "/dev/sr1")
	discDVD := newDiscTyped(t, s, drvA, state.DiscTypeDVD)
	discPSX := newDiscTyped(t, s, drvB, state.DiscTypePSX)
	discAwait := newDiscTyped(t, s, drvA, state.DiscTypeAudioCD)

	profDVD := newProfile(t, s, "DVD-MKV", state.DiscTypeDVD)
	profPSX := newProfile(t, s, "PSX-CHD", state.DiscTypePSX)

	// DVD: 2 failures, 1 done rip, 1 running transcode child.
	newJobKind(t, s, drvA, profDVD, discDVD, state.JobKindRip, state.JobStateFailed)
	newJobKind(t, s, drvA, profDVD, discDVD, state.JobKindRip, state.JobStateFailed)
	rDone := newJobKind(t, s, drvA, profDVD, discDVD, state.JobKindRip, state.JobStateDone)
	newJobChild(t, s, profDVD, discDVD, state.JobKindTranscode, state.JobStateRunning, rDone.ID)

	// PSX: single done rip.
	newJobKind(t, s, drvB, profPSX, discPSX, state.JobKindRip, state.JobStateDone)

	got, err := s.LifecycleStates(ctx, []string{discDVD.ID, discPSX.ID, discAwait.ID})
	if err != nil {
		t.Fatalf("LifecycleStates: %v", err)
	}
	if got[discDVD.ID] != state.DiscLifecycleEncoding {
		t.Errorf("DVD: got %q, want %q", got[discDVD.ID], state.DiscLifecycleEncoding)
	}
	if got[discPSX.ID] != state.DiscLifecycleDone {
		t.Errorf("PSX: got %q, want %q", got[discPSX.ID], state.DiscLifecycleDone)
	}
	if got[discAwait.ID] != state.DiscLifecycleAwaitingDecision {
		t.Errorf("AwaitingDecision: got %q, want %q", got[discAwait.ID], state.DiscLifecycleAwaitingDecision)
	}
}

func TestStore_LifecycleStates_EmptyInput(t *testing.T) {
	ctx := context.Background()
	s := openStore(t)

	got, err := s.LifecycleStates(ctx, nil)
	if err != nil {
		t.Fatalf("LifecycleStates(nil): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for nil input, got %v", got)
	}

	got, err = s.LifecycleStates(ctx, []string{})
	if err != nil {
		t.Fatalf("LifecycleStates([]): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty map for empty input, got %v", got)
	}
}

// newDiscTyped seeds a disc with an explicit DiscType. The default
// newDisc helper hardcodes DiscTypeAudioCD; lifecycle tests need DVD
// and PSX too.
func newDiscTyped(t *testing.T, s *state.Store, drv *state.Drive, dt state.DiscType) *state.Disc {
	t.Helper()
	d := &state.Disc{DriveID: drv.ID, Type: dt, Title: "x"}
	if err := s.CreateDisc(context.Background(), d); err != nil {
		t.Fatalf("create disc: %v", err)
	}
	return d
}

// newJobKind mirrors newJob but lets the caller set Kind + State.
// Used by lifecycle tests that need rip vs transcode rows in mixed
// states.
func newJobKind(t *testing.T, s *state.Store, drv *state.Drive, prof *state.Profile, disc *state.Disc, kind state.JobKind, st state.JobState) *state.Job {
	t.Helper()
	j := &state.Job{
		DiscID:    disc.ID,
		DriveID:   drv.ID,
		ProfileID: prof.ID,
		Kind:      kind,
		State:     st,
	}
	if err := s.CreateJob(context.Background(), j); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return j
}

// newJobChild seeds a transcode-half job linked to a parent rip via
// ParentJobID. Compute-half jobs are not bound to a drive, so DriveID
// stays empty.
func newJobChild(t *testing.T, s *state.Store, prof *state.Profile, disc *state.Disc, kind state.JobKind, st state.JobState, parentID string) *state.Job {
	t.Helper()
	j := &state.Job{
		DiscID:      disc.ID,
		ProfileID:   prof.ID,
		Kind:        kind,
		State:       st,
		ParentJobID: parentID,
	}
	if err := s.CreateJob(context.Background(), j); err != nil {
		t.Fatalf("create job: %v", err)
	}
	return j
}
