package state_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jumpingmushroom/DiscEcho/daemon/state"
)

func TestOpen_AppliesMigrationsOnFreshDB(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = db.Close() }()

	row := db.Conn().QueryRowContext(context.Background(),
		"SELECT MAX(version) FROM schema_migrations")
	var v int
	if err := row.Scan(&v); err != nil {
		t.Fatalf("scan version: %v", err)
	}
	if v != 20 {
		t.Errorf("schema_migrations max version: want 20, got %d", v)
	}

	for _, tbl := range []string{
		"drives", "discs", "profiles", "jobs", "job_steps",
		"log_lines", "notifications", "settings", "schema_migrations",
	} {
		var name string
		row := db.Conn().QueryRowContext(context.Background(),
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?", tbl)
		if err := row.Scan(&name); err != nil {
			t.Errorf("table %s missing: %v", tbl, err)
		}
	}
}

func TestOpen_IsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")

	db1, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = db1.Close()

	db2, err := state.Open(path)
	if err != nil {
		t.Fatalf("re-Open: %v", err)
	}
	defer func() { _ = db2.Close() }()

	row := db2.Conn().QueryRowContext(context.Background(),
		"SELECT COUNT(*) FROM schema_migrations")
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 20 {
		t.Errorf("schema_migrations rows after second open: want 20, got %d", n)
	}
}

func TestOpen_EnforcesForeignKeys(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()

	_, err = db.Conn().ExecContext(context.Background(),
		`INSERT INTO jobs (id, disc_id, profile_id, state, created_at)
		 VALUES ('j1', 'disc-nope', 'prof-nope', 'queued', '2026-01-01T00:00:00Z')`)
	if err == nil {
		t.Errorf("FK violation should have failed")
	}
}

// TestMigration003_FlipsDVDMovieDefaults seeds a DVD-Movie row matching
// the pre-003 shape, then executes the migration 003 SQL directly to
// confirm its UPDATE flips the row to MKV + main_feature. We don't
// re-Open the DB because schema-altering migrations (004+) aren't
// idempotent and the migration runner uses MAX(version) — replaying
// 003 by wiping its schema_migrations row doesn't work once newer
// schema-mutating migrations exist.
func TestMigration003_FlipsDVDMovieDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")

	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if _, err := db.Conn().ExecContext(ctx, `
		INSERT INTO profiles (id, disc_type, name, engine, format, preset,
		                      container, video_codec, quality_preset,
		                      drive_policy, options_json,
		                      output_path_template, enabled, step_count,
		                      created_at, updated_at)
		VALUES ('dvd-mov-test', 'DVD', 'DVD-Movie', 'HandBrake', 'MP4',
		        'x264 RF 20', 'MP4', 'x264', 'x264 RF 20', 'any',
		        '{}',
		        '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mp4',
		        1, 7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	// Replay migration 003's body directly. Idempotent for our purposes:
	// the WHERE clauses target the pre-003 seed shape, which is exactly
	// what the test row matches.
	body, err := migrationBody("003_dvd_default_mkv.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().ExecContext(ctx, body); err != nil {
		t.Fatalf("re-exec migration 003: %v", err)
	}

	row := db.Conn().QueryRowContext(ctx,
		`SELECT format, container, output_path_template, options_json
		   FROM profiles WHERE id = 'dvd-mov-test'`)
	var format, container, tmpl, opts string
	if err := row.Scan(&format, &container, &tmpl, &opts); err != nil {
		t.Fatal(err)
	}
	if format != "MKV" {
		t.Errorf("format: want MKV, got %s", format)
	}
	if container != "MKV" {
		t.Errorf("container: want MKV, got %s", container)
	}
	if !strings.HasSuffix(tmpl, ".mkv") {
		t.Errorf("template suffix: %s", tmpl)
	}
	if !strings.Contains(opts, `"dvd_selection_mode":"main_feature"`) {
		t.Errorf("options_json missing dvd_selection_mode: %s", opts)
	}
}

// TestMigration006_BackfillsDVDQualityOptions seeds a pre-006 DVD
// HandBrake profile (no quality_rf / encoder_preset in its options
// blob, stale "RF 20" display string), then executes migration 006's
// SQL directly to confirm it backfills the two new options without
// clobbering existing keys and refreshes the cosmetic display string.
// Like the 003 test, we don't re-Open — the migration runner is
// MAX(version)-based and won't replay 006.
func TestMigration006_BackfillsDVDQualityOptions(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	if _, err := db.Conn().ExecContext(ctx, `
		INSERT INTO profiles (id, disc_type, name, engine, format, preset,
		                      quality_preset, options_json,
		                      output_path_template, step_count,
		                      created_at, updated_at)
		VALUES ('dvd-q-test', 'DVD', 'DVD-Movie', 'HandBrake', 'MKV',
		        'x264 RF 20', 'x264 RF 20',
		        '{"dvd_selection_mode":"main_feature"}',
		        '{{.Title}}.mkv', 7,
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	body, err := migrationBody("006_dvd_quality_options.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().ExecContext(ctx, body); err != nil {
		t.Fatalf("re-exec migration 006: %v", err)
	}

	var blob, preset, qualityPreset string
	if err := db.Conn().QueryRowContext(ctx,
		`SELECT options_json, preset, quality_preset FROM profiles WHERE id = 'dvd-q-test'`).
		Scan(&blob, &preset, &qualityPreset); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"quality_rf":18`,
		`"encoder_preset":"slow"`,
		`"dvd_selection_mode":"main_feature"`, // pre-existing key preserved
	} {
		if !strings.Contains(blob, want) {
			t.Errorf("options_json missing %s: %s", want, blob)
		}
	}
	if preset != "x264 RF 18 · slow" || qualityPreset != "x264 RF 18 · slow" {
		t.Errorf("display strings not refreshed: preset=%q quality_preset=%q", preset, qualityPreset)
	}
}

// TestMigration019_RewritesQualityPresetSlugs seeds profiles carrying the
// legacy free-text quality_preset display strings and confirms migration
// 019 maps the known encode strings to the "high" tier slug and the
// lossless/passthrough strings to "", while leaving an unrecognised
// user-authored value untouched.
func TestMigration019_RewritesQualityPresetSlugs(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	rows := []struct{ id, qp string }{
		{"enc", "x265 RF 20 · slow · + extras"},
		{"pass", "passthrough"},
		{"def", "default"},
		{"ar", "AccurateRip · cuesheet"},
		{"custom", "my hand-rolled preset"},
	}
	for _, r := range rows {
		if _, err := db.Conn().ExecContext(ctx, `
			INSERT INTO profiles (id, disc_type, name, engine, format, preset,
			                      container, video_codec, quality_preset,
			                      drive_policy, options_json,
			                      output_path_template, enabled, step_count,
			                      created_at, updated_at)
			VALUES (?, 'DVD', ?, 'HandBrake', 'MKV', ?, 'MKV', 'x264', ?, 'any',
			        '{}', '{{.Title}}.mkv', 1, 7,
			        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`,
			r.id, r.id, r.qp, r.qp); err != nil {
			t.Fatal(err)
		}
	}

	body, err := migrationBody("019_quality_preset_slugs.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().ExecContext(ctx, body); err != nil {
		t.Fatalf("re-exec migration 019: %v", err)
	}

	want := map[string]string{
		"enc":    "high",
		"pass":   "",
		"def":    "",
		"ar":     "",
		"custom": "my hand-rolled preset",
	}
	for id, exp := range want {
		var qp, preset string
		if err := db.Conn().QueryRowContext(ctx,
			`SELECT quality_preset, preset FROM profiles WHERE id = ?`, id).
			Scan(&qp, &preset); err != nil {
			t.Fatal(err)
		}
		if qp != exp {
			t.Errorf("%s: quality_preset = %q, want %q", id, qp, exp)
		}
		// preset mirror tracks quality_preset for the rewritten rows; the
		// untouched custom row keeps both as-is.
		if id != "custom" && preset != exp {
			t.Errorf("%s: preset = %q, want %q", id, preset, exp)
		}
	}
}

// TestMigration009_CollapsesDuplicateDiscsAndReparentsJobs seeds three
// disc rows sharing the same (drive_id, toc_hash) plus jobs scattered
// across them — the exact prod shape we observed at v0.11.0 with seven
// "Fear and Bullets" rows on /dev/sr0. The unique index that 009
// creates blocks fresh dupes, so the test drops it, seeds the
// pre-migration state, and replays the migration body to confirm the
// dedup collapses the rows and rebinds the jobs.
func TestMigration009_CollapsesDuplicateDiscsAndReparentsJobs(t *testing.T) {
	dir := t.TempDir()
	db, err := state.Open(filepath.Join(dir, "test.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// Drop the unique index so we can recreate the pre-migration shape.
	if _, err := db.Conn().ExecContext(ctx, `DROP INDEX IF EXISTS idx_discs_drive_toc_unique`); err != nil {
		t.Fatalf("drop unique index: %v", err)
	}

	// Seed the prerequisites: drive + profile + three dupe discs (same
	// drive_id + toc_hash, different IDs, ascending created_at so the
	// last one is the "keeper") + jobs scattered across all three.
	if _, err := db.Conn().ExecContext(ctx, `
		INSERT INTO drives (id, model, bus, dev_path, state, last_seen_at)
		VALUES ('drv-x', 'Test', 'USB', '/dev/sr0', 'idle', '2026-05-17T13:00:00Z');

		INSERT INTO profiles (id, disc_type, name, engine, format, preset,
		                      container, video_codec, quality_preset,
		                      drive_policy, options_json,
		                      output_path_template, enabled, step_count,
		                      created_at, updated_at)
		VALUES ('prof-x', 'AUDIO_CD', 'CD-FLAC-test', 'whipper', 'FLAC',
		        'AccurateRip', 'FLAC', '', 'AccurateRip', 'any',
		        '{}',
		        '{{.Title}}/{{.Title}}.flac',
		        1, 6, '2026-05-17T13:00:00Z', '2026-05-17T13:00:00Z');

		INSERT INTO discs (id, drive_id, type, toc_hash, candidates_json,
		                   metadata_json, created_at)
		VALUES
		  ('d-old',  'drv-x', 'AUDIO_CD', 'fbHash', '[]', '{}', '2026-05-17T12:00:00Z'),
		  ('d-mid',  'drv-x', 'AUDIO_CD', 'fbHash', '[]', '{}', '2026-05-17T12:30:00Z'),
		  ('d-new',  'drv-x', 'AUDIO_CD', 'fbHash', '[]', '{}', '2026-05-17T13:00:00Z');

		INSERT INTO jobs (id, disc_id, drive_id, profile_id, state, created_at)
		VALUES
		  ('j-old', 'd-old', 'drv-x', 'prof-x', 'failed', '2026-05-17T12:05:00Z'),
		  ('j-mid', 'd-mid', 'drv-x', 'prof-x', 'failed', '2026-05-17T12:35:00Z'),
		  ('j-new', 'd-new', 'drv-x', 'prof-x', 'running', '2026-05-17T13:05:00Z');
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	body, err := migrationBody("009_dedupe_discs.sql")
	if err != nil {
		t.Fatal(err)
	}
	// The migration body re-creates the unique index; drop it first
	// since Open already added it on this fresh DB.
	if _, err := db.Conn().ExecContext(ctx, `DROP INDEX IF EXISTS idx_discs_drive_toc_unique`); err != nil {
		t.Fatalf("drop unique index before replay: %v", err)
	}
	if _, err := db.Conn().ExecContext(ctx, body); err != nil {
		t.Fatalf("replay migration 009: %v", err)
	}

	// Only the most-recent disc should remain.
	row := db.Conn().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM discs WHERE drive_id = 'drv-x' AND toc_hash = 'fbHash'`)
	var n int
	if err := row.Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("discs after collapse: want 1, got %d", n)
	}

	// All three jobs should point at d-new (the keeper).
	rows, err := db.Conn().QueryContext(ctx,
		`SELECT id, disc_id FROM jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id, discID string
		if err := rows.Scan(&id, &discID); err != nil {
			t.Fatal(err)
		}
		if discID != "d-new" {
			t.Errorf("job %s: disc_id = %s, want d-new", id, discID)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	// The unique index must be back so future dupes are blocked.
	_, err = db.Conn().ExecContext(ctx, `
		INSERT INTO discs (id, drive_id, type, toc_hash, candidates_json, metadata_json, created_at)
		VALUES ('d-dupe', 'drv-x', 'AUDIO_CD', 'fbHash', '[]', '{}', '2026-05-17T17:00:00Z')`)
	if err == nil {
		t.Error("insert with duplicate (drive_id, toc_hash) should fail after migration")
	}
}

// TestMigration018_SeedsDVDMakeMKVProfiles confirms that on a DB that
// already carries the legacy HandBrake-engine DVD profiles (which
// migration 018 keys off via WHERE EXISTS), replaying 018's body
// inserts the three new MakeMKV-engine seeds. Mirrors the structure of
// the other migration-replay tests: we don't re-Open, we hand-INSERT
// the predicate rows after the migration runner has caught up, then
// execute 018's SQL directly.
func TestMigration018_SeedsDVDMakeMKVProfiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.sqlite")

	db, err := state.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	ctx := context.Background()

	// Migration 018's WHERE EXISTS predicates gate on the legacy DVD
	// rows; without them the inserts no-op (intended — keeps the
	// migration from polluting test DBs that bypassed the seeder).
	if _, err := db.Conn().ExecContext(ctx, `
		INSERT INTO profiles (id, disc_type, name, engine, format, preset,
		                      container, video_codec, quality_preset,
		                      drive_policy, options_json,
		                      output_path_template, enabled, step_count,
		                      created_at, updated_at)
		VALUES
		  ('dvd-mov-pre',    'DVD', 'DVD-Movie',          'HandBrake', 'MKV', 'x', 'MKV', 'x264', 'x', 'any', '{}', '{{.Title}}.mkv', 1, 7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z'),
		  ('dvd-series-pre', 'DVD', 'DVD-Series',         'HandBrake', 'MKV', 'x', 'MKV', 'x264', 'x', 'any', '{}', '{{.Show}}.mkv',  1, 7, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}

	body, err := migrationBody("018_dvd_makemkv_seeds.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Conn().ExecContext(ctx, body); err != nil {
		t.Fatalf("re-exec migration 018: %v", err)
	}

	wantSeeds := map[string]string{
		"DVD-Movie (MakeMKV)":          "MakeMKV",
		"DVD-Movie + Extras (MakeMKV)": "MakeMKV+HandBrake",
		"DVD-Series (MakeMKV)":         "MakeMKV+HandBrake",
	}
	for name, wantEngine := range wantSeeds {
		row := db.Conn().QueryRowContext(ctx,
			`SELECT engine FROM profiles WHERE disc_type='DVD' AND name=?`, name)
		var got string
		if err := row.Scan(&got); err != nil {
			t.Errorf("seed %q not inserted: %v", name, err)
			continue
		}
		if got != wantEngine {
			t.Errorf("seed %q engine: want %s, got %s", name, wantEngine, got)
		}
	}
}

// migrationBody loads a migration file by name from the embed FS. Used
// only by tests that need to re-apply a specific migration's SQL.
func migrationBody(name string) (string, error) {
	// The embed FS lives in db.go; expose its bytes here via a small
	// helper that re-reads the file from disk under daemon/state/.
	b, err := os.ReadFile(filepath.Join("migrations", name))
	if err != nil {
		return "", err
	}
	return string(b), nil
}
