package state

import (
	"context"
	"log/slog"
	"strconv"
	"time"
)

// SettingsReader is the small interface the sweeper consults for
// retention configuration. *Store satisfies it via GetBool/GetInt.
type SettingsReader interface {
	GetBool(ctx context.Context, key string) (bool, error)
	GetInt(ctx context.Context, key string) (int, error)
}

// Sweeper periodically deletes old history rows according to retention
// settings. It runs immediately on Start, then daily at 03:00 daemon-local.
type Sweeper struct {
	Store    *Store
	Settings SettingsReader
	Now      func() time.Time
	Logger   *slog.Logger
}

// Start launches the sweeper goroutine. Cancel ctx to stop.
func (s *Sweeper) Start(ctx context.Context) {
	go s.run(ctx)
}

func (s *Sweeper) run(ctx context.Context) {
	s.Tick(ctx)
	for {
		next := NextThreeAM(s.Now())
		timer := time.NewTimer(next.Sub(s.Now()))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
			s.Tick(ctx)
		}
	}
}

// Tick runs one sweep iteration. Exported so tests can drive it directly.
// A no-op when retention.forever is set or no per-outcome knob is active.
func (s *Sweeper) Tick(ctx context.Context) {
	logger := s.Logger
	if logger == nil {
		logger = slog.Default()
	}

	forever, _ := s.Settings.GetBool(ctx, "retention.forever")
	if forever {
		return
	}

	policy := s.policy(ctx)
	if !policy.active() {
		return
	}

	now := s.Now()
	res, err := s.Store.PruneHistory(ctx, policy, now)
	if err != nil {
		logger.Error("retention sweep failed", "err", err)
		return
	}
	s.recordRun(ctx, now, res.Total())
	logger.Info("retention sweep",
		"deleted_success", res.SuccessDeleted,
		"deleted_failed", res.FailedDeleted,
		"deleted_discs", res.DiscsDeleted)
}

// policy reads the four per-outcome retention knobs from settings.
func (s *Sweeper) policy(ctx context.Context) RetentionPolicy {
	sd, _ := s.Settings.GetInt(ctx, "retention.success.days")
	sc, _ := s.Settings.GetInt(ctx, "retention.success.count")
	fd, _ := s.Settings.GetInt(ctx, "retention.failed.days")
	fc, _ := s.Settings.GetInt(ctx, "retention.failed.count")
	return RetentionPolicy{SuccessDays: sd, SuccessCount: sc, FailedDays: fd, FailedCount: fc}
}

// recordRun persists the last-run timestamp and entry count for the UI.
func (s *Sweeper) recordRun(ctx context.Context, now time.Time, deleted int) {
	_ = s.Store.SetSetting(ctx, "retention.last_run_at", now.UTC().Format(time.RFC3339Nano))
	_ = s.Store.SetSetting(ctx, "retention.last_run_count", strconv.Itoa(deleted))
}

// NextThreeAM returns the next 03:00 in now's location, strictly after now.
func NextThreeAM(now time.Time) time.Time {
	target := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	return target
}
