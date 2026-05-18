-- 017_extras_profiles: seed `DVD-Movie + Extras` and `BD-1080p + Extras`
-- into existing installs that already have the parent profile. Fresh
-- installs get all four profiles via the seeder in settings.go;
-- gating on the parent ensures this migration only fires as a true
-- backfill (and keeps `state.Open` from polluting test DBs that
-- bypass the seeder).
--
-- INSERT OR IGNORE keeps a user's hand-rolled same-named profile
-- intact across re-runs.
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
SELECT
  'dvd-movie-extras-seed', 'DVD', 'DVD-Movie + Extras', 'HandBrake',
  'MKV', 'x264 RF 18 · slow · + extras',
  'MKV', 'x264', 'x264 RF 18 · slow · + extras',
  '', 'any',
  '{"dvd_selection_mode":"main_feature","quality_rf":18,"encoder_preset":"slow","include_extras":true,"min_extra_seconds":60,"extras_max_ratio":90}',
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
  'bd-1080p-extras-seed', 'BDMV', 'BD-1080p + Extras',
  'MakeMKV+HandBrake', 'MKV', 'x265 RF 19 10-bit · + extras',
  'MKV', 'x265', 'x265 RF 19 10-bit · + extras',
  'passthrough', 'any',
  '{"min_title_seconds":3600,"keep_all_tracks":false,"include_extras":true,"min_extra_seconds":60,"extras_max_ratio":90}',
  '{{.Title}} ({{.Year}})/{{.Title}} ({{.Year}}).mkv',
  1, 7,
  strftime('%Y-%m-%dT%H:%M:%fZ','now'),
  strftime('%Y-%m-%dT%H:%M:%fZ','now')
WHERE EXISTS (SELECT 1 FROM profiles WHERE disc_type='BDMV' AND name='BD-1080p');
