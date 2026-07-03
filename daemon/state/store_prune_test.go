package state_test

import (
	"context"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// backdateJobFinishedAt overwrites finished_at via raw SQL so we can test
// cutoff comparisons without coupling to UpdateJobState's "now" logic.
func backdateJobFinishedAt(t *testing.T, s *state.Store, jobID string, at time.Time) {
	t.Helper()
	_, err := s.DB().Conn().ExecContext(context.Background(),
		`UPDATE jobs SET finished_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339Nano), jobID)
	if err != nil {
		t.Fatalf("backdate finished_at: %v", err)
	}
}

func backdateDiscCreatedAt(t *testing.T, s *state.Store, discID string, at time.Time) {
	t.Helper()
	_, err := s.DB().Conn().ExecContext(context.Background(),
		`UPDATE discs SET created_at = ? WHERE id = ?`,
		at.UTC().Format(time.RFC3339Nano), discID)
	if err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}
}

// doneJob creates a finished 'done' job on its own disc, backdated by ageDays.
func doneJob(t *testing.T, s *state.Store, drv *state.Drive, prof *state.Profile, ageDays int) (*state.Job, *state.Disc) {
	t.Helper()
	ctx := context.Background()
	disc := newDisc(t, s, drv)
	j := newJob(t, s, drv, prof, disc)
	if err := s.UpdateJobState(ctx, j.ID, state.JobStateDone, ""); err != nil {
		t.Fatalf("UpdateJobState: %v", err)
	}
	backdateJobFinishedAt(t, s, j.ID, time.Now().AddDate(0, 0, -ageDays))
	return j, disc
}

func failedJob(t *testing.T, s *state.Store, drv *state.Drive, prof *state.Profile, ageDays int) (*state.Job, *state.Disc) {
	t.Helper()
	ctx := context.Background()
	disc := newDisc(t, s, drv)
	j := newJob(t, s, drv, prof, disc)
	if err := s.UpdateJobState(ctx, j.ID, state.JobStateFailed, "boom"); err != nil {
		t.Fatalf("UpdateJobState: %v", err)
	}
	backdateJobFinishedAt(t, s, j.ID, time.Now().AddDate(0, 0, -ageDays))
	return j, disc
}

func mustGone(t *testing.T, s *state.Store, jobID string) {
	t.Helper()
	if _, err := s.GetJob(context.Background(), jobID); err == nil {
		t.Fatalf("job %s should have been pruned", jobID)
	}
}

func mustExist(t *testing.T, s *state.Store, jobID string) {
	t.Helper()
	if _, err := s.GetJob(context.Background(), jobID); err != nil {
		t.Fatalf("job %s should have survived: %v", jobID, err)
	}
}

// Per-outcome isolation: a failed-only age policy prunes the old failed job
// and leaves an equally-old successful job untouched.
func TestStore_PruneHistory_PerOutcomeByDays(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)

	doneOld, _ := doneJob(t, s, drv, prof, 100)
	failOld, _ := failedJob(t, s, drv, prof, 100)

	res, err := s.PruneHistory(ctx, state.RetentionPolicy{FailedDays: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.FailedDeleted != 1 || res.SuccessDeleted != 0 {
		t.Fatalf("want 1 failed / 0 success deleted, got %+v", res)
	}
	mustGone(t, s, failOld.ID)
	mustExist(t, s, doneOld.ID)
}

func TestStore_PruneHistory_SuccessByDays(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)

	old, _ := doneJob(t, s, drv, prof, 100)
	fresh, _ := doneJob(t, s, drv, prof, 1)

	res, err := s.PruneHistory(ctx, state.RetentionPolicy{SuccessDays: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.SuccessDeleted != 1 {
		t.Fatalf("want 1 success deleted, got %+v", res)
	}
	mustGone(t, s, old.ID)
	mustExist(t, s, fresh.ID)
}

// Count cap keeps the newest N regardless of age.
func TestStore_PruneHistory_CountKeepsNewest(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)

	j1, _ := doneJob(t, s, drv, prof, 3) // oldest
	j2, _ := doneJob(t, s, drv, prof, 2)
	j3, _ := doneJob(t, s, drv, prof, 1) // newest

	res, err := s.PruneHistory(ctx, state.RetentionPolicy{SuccessCount: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.SuccessDeleted != 2 {
		t.Fatalf("want 2 deleted (keep newest 1), got %+v", res)
	}
	mustGone(t, s, j1.ID)
	mustGone(t, s, j2.ID)
	mustExist(t, s, j3.ID)
}

// Running jobs (finished_at NULL) are never selected.
func TestStore_PruneHistory_KeepsRunning(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)
	disc := newDisc(t, s, drv)
	run := newJob(t, s, drv, prof, disc)
	if err := s.UpdateJobState(ctx, run.ID, state.JobStateRunning, ""); err != nil {
		t.Fatal(err)
	}

	res, err := s.PruneHistory(ctx, state.RetentionPolicy{SuccessDays: 1, FailedDays: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total() != 0 {
		t.Fatalf("running job should not be pruned, got %+v", res)
	}
	mustExist(t, s, run.ID)
}

// TestStore_PruneHistory_KeepsTerminalWithEmptyFinishedAt guards the
// non-empty-finished_at fix: a terminal job whose finished_at is the empty
// string (never stamped) must not be pruned by a days-only policy. The old
// IS-NOT-NULL guard was a no-op (the column is empty, not NULL), so the empty
// string — which sorts before any timestamp — matched the finished_at-below-
// cutoff age arm and the row was wrongly deleted.
func TestStore_PruneHistory_KeepsTerminalWithEmptyFinishedAt(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)
	disc := newDisc(t, s, drv)
	j := newJob(t, s, drv, prof, disc)
	if _, err := s.DB().Conn().ExecContext(ctx,
		`UPDATE jobs SET state='done', finished_at='' WHERE id=?`, j.ID); err != nil {
		t.Fatal(err)
	}

	res, err := s.PruneHistory(ctx, state.RetentionPolicy{SuccessDays: 1}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total() != 0 {
		t.Fatalf("terminal job with empty finished_at must not be pruned, got %+v", res)
	}
	mustExist(t, s, j.ID)
}

// The orphan-disc cleanup keeps the drive's most-recent disc (still inserted)
// even when its only job is pruned, but drops an older orphaned disc.
func TestStore_PruneHistory_NewestDiscGuard(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)

	jOld, discOld := doneJob(t, s, drv, prof, 100)
	jNew, discNew := doneJob(t, s, drv, prof, 100)
	// Make discNew strictly the most-recent disc on the drive.
	backdateDiscCreatedAt(t, s, discOld.ID, time.Now().AddDate(0, 0, -10))
	backdateDiscCreatedAt(t, s, discNew.ID, time.Now())

	if _, err := s.PruneHistory(ctx, state.RetentionPolicy{SuccessDays: 30}, time.Now()); err != nil {
		t.Fatal(err)
	}
	mustGone(t, s, jOld.ID)
	mustGone(t, s, jNew.ID)
	if _, err := s.GetDisc(ctx, discOld.ID); err == nil {
		t.Fatal("older orphaned disc should be deleted")
	}
	if _, err := s.GetDisc(ctx, discNew.ID); err != nil {
		t.Fatalf("newest disc (still inserted) should be kept: %v", err)
	}
}

func TestStore_PruneHistory_InactivePolicyNoOp(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)
	old, _ := doneJob(t, s, drv, prof, 100)

	res, err := s.PruneHistory(ctx, state.RetentionPolicy{}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.Total() != 0 {
		t.Fatalf("empty policy should prune nothing, got %+v", res)
	}
	mustExist(t, s, old.ID)
}

func TestStore_CountWouldPrune_NoDelete(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)
	old, _ := doneJob(t, s, drv, prof, 100)
	failedJob(t, s, drv, prof, 100)

	res, err := s.CountWouldPrune(ctx, state.RetentionPolicy{SuccessDays: 30, FailedDays: 30}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if res.SuccessDeleted != 1 || res.FailedDeleted != 1 {
		t.Fatalf("want 1/1 would-delete, got %+v", res)
	}
	// Nothing actually deleted.
	mustExist(t, s, old.ID)
}

func TestStore_HistoryBucketTotals(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)
	doneJob(t, s, drv, prof, 1)
	doneJob(t, s, drv, prof, 2)
	failedJob(t, s, drv, prof, 1)

	success, failed, err := s.HistoryBucketTotals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if success != 2 || failed != 1 {
		t.Fatalf("want success=2 failed=1, got success=%d failed=%d", success, failed)
	}
}
