package state_test

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

type fakeReader struct {
	forever      bool
	successDays  int
	successCount int
	failedDays   int
	failedCount  int
}

func (f *fakeReader) GetBool(_ context.Context, key string) (bool, error) {
	if key == "retention.forever" {
		return f.forever, nil
	}
	return false, nil
}

func (f *fakeReader) GetInt(_ context.Context, key string) (int, error) {
	switch key {
	case "retention.success.days":
		return f.successDays, nil
	case "retention.success.count":
		return f.successCount, nil
	case "retention.failed.days":
		return f.failedDays, nil
	case "retention.failed.count":
		return f.failedCount, nil
	}
	return 0, nil
}

func TestSweeper_Tick_NoOpWhenForever(t *testing.T) {
	s := openStore(t)
	sw := &state.Sweeper{
		Store:    s,
		Settings: &fakeReader{forever: true},
		Now:      time.Now,
		Logger:   slog.Default(),
	}
	sw.Tick(context.Background())
}

func TestSweeper_Tick_NoOpWhenAllZero(t *testing.T) {
	s := openStore(t)
	sw := &state.Sweeper{
		Store:    s,
		Settings: &fakeReader{forever: false}, // all knobs zero
		Now:      time.Now,
		Logger:   slog.Default(),
	}
	sw.Tick(context.Background())
}

func TestSweeper_Tick_DeletesOldJobs_AndRecordsRun(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "p", state.DiscTypeAudioCD)
	old, _ := doneJob(t, s, drv, prof, 100)

	sw := &state.Sweeper{
		Store:    s,
		Settings: &fakeReader{forever: false, successDays: 30},
		Now:      time.Now,
		Logger:   slog.Default(),
	}
	sw.Tick(ctx)

	if _, err := s.GetJob(ctx, old.ID); err == nil {
		t.Fatal("old job should be deleted")
	}
	// The run is recorded for the UI's last-run line.
	if v, _ := s.GetSetting(ctx, "retention.last_run_at"); v == "" {
		t.Error("retention.last_run_at should be set after a sweep")
	}
	if n, _ := s.GetInt(ctx, "retention.last_run_count"); n != 1 {
		t.Errorf("retention.last_run_count: want 1, got %d", n)
	}
}

func TestSweeper_NextThreeAM_BeforeThree(t *testing.T) {
	now := time.Date(2026, 5, 8, 1, 0, 0, 0, time.UTC)
	got := state.NextThreeAM(now)
	want := time.Date(2026, 5, 8, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSweeper_NextThreeAM_AfterThree(t *testing.T) {
	now := time.Date(2026, 5, 8, 5, 0, 0, 0, time.UTC)
	got := state.NextThreeAM(now)
	want := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestSweeper_NextThreeAM_ExactlyAtThree(t *testing.T) {
	// At exactly 03:00, "next" should be the following day.
	now := time.Date(2026, 5, 8, 3, 0, 0, 0, time.UTC)
	got := state.NextThreeAM(now)
	want := time.Date(2026, 5, 9, 3, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Fatalf("got %v want %v", got, want)
	}
}
