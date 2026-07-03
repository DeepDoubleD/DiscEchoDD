package state_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

// TestStore_Stats_LibrarySeries pins the statsLibrary refactor: the 30-day
// cumulative series is a running total that folds in a pre-window baseline,
// stays monotonic, and ends exactly at UsedBytes.
func TestStore_Stats_LibrarySeries(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "CD-FLAC", state.DiscTypeAudioCD)
	now := time.Date(2026, 7, 15, 12, 0, 0, 0, time.UTC)

	mk := func(daysAgo int, bytes int64) {
		disc := newDisc(t, s, drv)
		j := newJob(t, s, drv, prof, disc)
		if err := s.UpdateJobState(ctx, j.ID, state.JobStateDone, ""); err != nil {
			t.Fatal(err)
		}
		if err := s.RecordOutputBytes(ctx, j.ID, bytes); err != nil {
			t.Fatal(err)
		}
		backdateJobFinishedAt(t, s, j.ID, now.AddDate(0, 0, -daysAgo))
	}
	mk(40, 1000) // before the 30-day window → baseline
	mk(5, 100)   // index 24
	mk(2, 200)   // index 27
	mk(0, 50)    // index 29 (today)

	out, err := s.Stats(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	lib := out.Library
	if lib.UsedBytes != 1350 {
		t.Fatalf("used_bytes = %d, want 1350", lib.UsedBytes)
	}
	if len(lib.Spark30dUsed) != 30 {
		t.Fatalf("series len = %d, want 30", len(lib.Spark30dUsed))
	}
	checks := map[int]int64{0: 1000, 23: 1000, 24: 1100, 27: 1300, 29: 1350}
	for idx, want := range checks {
		if lib.Spark30dUsed[idx] != want {
			t.Errorf("series[%d] = %d, want %d", idx, lib.Spark30dUsed[idx], want)
		}
	}
	if lib.Spark30dUsed[29] != lib.UsedBytes {
		t.Errorf("series must end at used_bytes: %d vs %d", lib.Spark30dUsed[29], lib.UsedBytes)
	}
	for i := 1; i < 30; i++ {
		if lib.Spark30dUsed[i] < lib.Spark30dUsed[i-1] {
			t.Errorf("series not monotonic at %d: %d < %d", i, lib.Spark30dUsed[i], lib.Spark30dUsed[i-1])
		}
	}
}

// TestStore_Stats_ZoneStable pins the fix for the stats timezone-mixing bug:
// finished_at is stored UTC and date(finished_at) buckets by the UTC day, so
// the aggregator must define "today" and every cutoff in UTC regardless of the
// zone of the `now` it's handed. Before the fix, a non-UTC `now` formatted
// cutoffs like `…+09:00` and compared them lexically against the stored `…Z`
// values, so the same instant produced different numbers depending on the
// daemon's local zone. This asserts the result is identical for the same
// instant expressed in UTC and in a +09:00 location.
func TestStore_Stats_ZoneStable(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	drv := newDrive(t, s, "/dev/sr0")
	prof := newProfile(t, s, "CD-FLAC", state.DiscTypeAudioCD)

	// 06:00Z: a west-of-UTC zone (e.g. -10:00) still reads the previous
	// calendar day at this instant, which is exactly where a local-zone
	// cutoff diverges from the UTC-day bucketing of date(finished_at).
	nowUTC := time.Date(2026, 7, 2, 6, 0, 0, 0, time.UTC)

	mkDone := func(finishedAt time.Time, bytes int64) {
		disc := newDisc(t, s, drv)
		j := newJob(t, s, drv, prof, disc)
		if err := s.UpdateJobState(ctx, j.ID, state.JobStateDone, ""); err != nil {
			t.Fatal(err)
		}
		if bytes > 0 {
			if err := s.RecordOutputBytes(ctx, j.ID, bytes); err != nil {
				t.Fatal(err)
			}
		}
		backdateJobFinishedAt(t, s, j.ID, finishedAt)
	}
	mkFailed := func(finishedAt time.Time) {
		disc := newDisc(t, s, drv)
		j := newJob(t, s, drv, prof, disc)
		if err := s.UpdateJobState(ctx, j.ID, state.JobStateFailed, "boom"); err != nil {
			t.Fatal(err)
		}
		backdateJobFinishedAt(t, s, j.ID, finishedAt)
	}

	// Done today (UTC), done yesterday (UTC), one failure today.
	mkDone(time.Date(2026, 7, 2, 3, 0, 0, 0, time.UTC), 1000)
	mkDone(time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC), 2000)
	mkFailed(time.Date(2026, 7, 2, 2, 0, 0, 0, time.UTC))

	utc, err := s.Stats(ctx, nowUTC)
	if err != nil {
		t.Fatal(err)
	}
	// "Today" (UTC = July 2) counts only the 03:00Z done job.
	if utc.TodayRipped.Titles != 1 || utc.TodayRipped.Bytes != 1000 {
		t.Errorf("today ripped = %d titles / %d bytes, want 1 / 1000",
			utc.TodayRipped.Titles, utc.TodayRipped.Bytes)
	}

	// The same instant expressed in other zones must produce identical
	// numbers. A -10:00 zone reads July 1 locally at this instant, so a
	// local-zone cutoff would wrongly fold the July-1 done job into "today".
	for _, offset := range []int{9 * 3600, -10 * 3600, -5 * 3600} {
		nowZoned := nowUTC.In(time.FixedZone("z", offset))
		z, err := s.Stats(ctx, nowZoned)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(z.TodayRipped, utc.TodayRipped) {
			t.Errorf("offset %d: today ripped zone-dependent: utc=%+v got=%+v", offset, utc.TodayRipped, z.TodayRipped)
		}
		if !reflect.DeepEqual(z.Failures7d, utc.Failures7d) {
			t.Errorf("offset %d: failures zone-dependent: utc=%+v got=%+v", offset, utc.Failures7d, z.Failures7d)
		}
	}
}
