-- 018_dvd_makemkv_seeds: seed `DVD-Movie (MakeMKV)`,
-- `DVD-Movie + Extras (MakeMKV)`, and `DVD-Series (MakeMKV)` into
-- existing installs that already have the legacy HandBrake-engine
-- DVD profiles. Fresh installs get every entry via the seeder in
-- settings.go; gating on the parent keeps this migration from
-- polluting test DBs that bypass the seeder, and INSERT OR IGNORE
-- keeps a user's hand-rolled same-named profile intact across
-- re-runs.
--
-- DVD-Movie (MakeMKV) is MakeMKV-passthrough: the disc is ripped as
-- MPEG-2-in-MKV with no re-encode. Step count is 6 (no transcode).
-- The other two go through MakeMKV+HandBrake and run the full 7-step
-- pipeline.
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
SELECT
  'dvd-movie-makemkv-seed', 'DVD', 'DVD-Movie (MakeMKV)', 'MakeMKV',
  'MKV', 'passthrough',
  'MKV', 'copy', 'passthrough',
  '', 'any',
  '{"dvd_selection_mode":"main_feature"}',
  '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv',
  1, 6,
  strftime('%Y-%m-%dT%H:%M:%fZ','now'),
  strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE EXISTS (SELECT 1 FROM profiles WHERE disc_type='DVD' AND name='DVD-Movie');

INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
SELECT
  'dvd-movie-extras-makemkv-seed', 'DVD', 'DVD-Movie + Extras (MakeMKV)',
  'MakeMKV+HandBrake', 'MKV', 'x265 RF 20 · slow · + extras',
  'MKV', 'x265', 'x265 RF 20 · slow · + extras',
  '', 'any',
  '{"dvd_selection_mode":"main_feature","quality_rf":20,"encoder_preset":"slow","include_extras":true,"min_extra_seconds":60,"extras_max_ratio":90}',
  '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv',
  1, 7,
  strftime('%Y-%m-%dT%H:%M:%fZ','now'),
  strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE EXISTS (SELECT 1 FROM profiles WHERE disc_type='DVD' AND name='DVD-Movie');

INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
SELECT
  'dvd-series-makemkv-seed', 'DVD', 'DVD-Series (MakeMKV)',
  'MakeMKV+HandBrake', 'MKV', 'x265 RF 20 · slow · per-title',
  'MKV', 'x265', 'x265 RF 20 · slow · per-title',
  '', 'any',
  '{"min_title_seconds":300,"season":1,"dvd_selection_mode":"per_title","quality_rf":20,"encoder_preset":"slow"}',
  '{{.Show}}/Season {{printf "%02d" .Season}}/{{.Show}} - S{{printf "%02d" .Season}}E{{printf "%02d" .EpisodeNumber}}{{if .EpisodeTitle}} - {{.EpisodeTitle}}{{end}}.mkv',
  1, 7,
  strftime('%Y-%m-%dT%H:%M:%fZ','now'),
  strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE EXISTS (SELECT 1 FROM profiles WHERE disc_type='DVD' AND name='DVD-Series');
