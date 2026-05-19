package state

import (
	"testing"
	"time"
)

func TestDeriveLifecycleState(t *testing.T) {
	now := time.Now().UTC()
	earlier := now.Add(-1 * time.Hour)
	older := now.Add(-2 * time.Hour)

	tests := []struct {
		name     string
		discType DiscType
		jobs     []Job
		want     DiscLifecycleState
	}{
		{
			name:     "no jobs at all",
			discType: DiscTypeDVD,
			jobs:     nil,
			want:     DiscLifecycleAwaitingDecision,
		},
		{
			name:     "rip queued",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateQueued, CreatedAt: now},
			},
			want: DiscLifecycleRipping,
		},
		{
			name:     "rip running",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateRunning, CreatedAt: now},
			},
			want: DiscLifecycleRipping,
		},
		{
			name:     "rip failed, no later attempt",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateFailed, CreatedAt: now},
			},
			want: DiscLifecycleFailed,
		},
		{
			name:     "rip cancelled, no later attempt",
			discType: DiscTypePSX,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateCancelled, CreatedAt: now},
			},
			want: DiscLifecycleCancelled,
		},
		{
			name:     "rip interrupted, no later attempt",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateInterrupted, CreatedAt: now},
			},
			want: DiscLifecycleInterrupted,
		},
		{
			name:     "audio CD rip done — monolithic, journey complete",
			discType: DiscTypeAudioCD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: now},
			},
			want: DiscLifecycleDone,
		},
		{
			name:     "PSX rip done — no transcode half, journey complete",
			discType: DiscTypePSX,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: now},
			},
			want: DiscLifecycleDone,
		},
		{
			name:     "DVD rip done, no transcode queued yet",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: now},
			},
			want: DiscLifecycleAwaitingEncode,
		},
		{
			name:     "DVD rip done, transcode queued",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: earlier},
				{ID: "t1", Kind: JobKindTranscode, State: JobStateQueued, CreatedAt: now, ParentJobID: "r1"},
			},
			want: DiscLifecycleEncoding,
		},
		{
			name:     "DVD rip done, transcode running",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: earlier},
				{ID: "t1", Kind: JobKindTranscode, State: JobStateRunning, CreatedAt: now, ParentJobID: "r1"},
			},
			want: DiscLifecycleEncoding,
		},
		{
			name:     "DVD rip done, transcode done",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: earlier},
				{ID: "t1", Kind: JobKindTranscode, State: JobStateDone, CreatedAt: now, ParentJobID: "r1"},
			},
			want: DiscLifecycleDone,
		},
		{
			name:     "DVD rip done, transcode failed",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateDone, CreatedAt: earlier},
				{ID: "t1", Kind: JobKindTranscode, State: JobStateFailed, CreatedAt: now, ParentJobID: "r1"},
			},
			want: DiscLifecycleFailed,
		},
		{
			name:     "latest-attempt-wins: 4 failed rips then done rip + running transcode",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: JobKindRip, State: JobStateFailed, CreatedAt: older},
				{ID: "r2", Kind: JobKindRip, State: JobStateFailed, CreatedAt: older.Add(1 * time.Minute)},
				{ID: "r3", Kind: JobKindRip, State: JobStateFailed, CreatedAt: older.Add(2 * time.Minute)},
				{ID: "r4", Kind: JobKindRip, State: JobStateInterrupted, CreatedAt: older.Add(3 * time.Minute)},
				{ID: "r5", Kind: JobKindRip, State: JobStateDone, CreatedAt: earlier},
				{ID: "t1", Kind: JobKindTranscode, State: JobStateRunning, CreatedAt: now, ParentJobID: "r5"},
			},
			want: DiscLifecycleEncoding,
		},
		{
			name:     "legacy rip with empty kind treated as rip",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "r1", Kind: "", State: JobStateRunning, CreatedAt: now},
			},
			want: DiscLifecycleRipping,
		},
		{
			name:     "scan jobs are ignored when computing lifecycle",
			discType: DiscTypeDVD,
			jobs: []Job{
				{ID: "s1", Kind: JobKindScan, State: JobStateDone, CreatedAt: older},
				{ID: "r1", Kind: JobKindRip, State: JobStateRunning, CreatedAt: now},
			},
			want: DiscLifecycleRipping,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := DeriveLifecycleState(tc.discType, tc.jobs)
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}
