-- 021_tier1_cd_console_seeds: seed default CHD rip profiles for the
-- seven Tier-1 CD console disc types added in M3a:
--   SegaCD, 3DO, PC-FX, Jaguar CD, CD-i, PC Engine CD, Neo Geo CD.
--
-- All seven use the redumper+chdman engine (lossless CD image → CHD),
-- with an empty quality_preset (lossless, no encode), drive_policy
-- "any", and step_count 7 (the cdgame handler's real pipeline length).
-- INSERT OR IGNORE keeps a hand-rolled same-named profile intact across
-- re-runs and makes the migration safe to replay.
--
-- DOWN: delete the seven seeded rows by their stable seed IDs.

-- SegaCD
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    'segacd-chd-seed', 'SEGACD', 'SegaCD-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- 3DO
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    '3do-chd-seed', '3DO', '3DO-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- PC-FX
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    'pcfx-chd-seed', 'PCFX', 'PCFX-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Jaguar CD
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    'jagcd-chd-seed', 'JAGCD', 'JaguarCD-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- CD-i
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    'cdi-chd-seed', 'CDI', 'CDi-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- PC Engine CD
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    'pcecd-chd-seed', 'PCECD', 'PCEngineCD-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- Neo Geo CD
INSERT OR IGNORE INTO profiles (
    id, disc_type, name, engine, format, preset,
    container, video_codec, quality_preset, hdr_pipeline,
    drive_policy, options_json,
    output_path_template, enabled, step_count,
    created_at, updated_at)
VALUES (
    'neocd-chd-seed', 'NEOCD', 'NeoGeoCD-CHD', 'redumper+chdman', 'CHD', '',
    'CHD', '', '', '',
    'any', '{}',
    '{{.Title}} ({{.Region}})/{{.Title}} ({{.Region}}).chd', 1, 7,
    strftime('%Y-%m-%dT%H:%M:%fZ','now'),
    strftime('%Y-%m-%dT%H:%M:%fZ','now'));

-- DOWN: remove the seven seeded rows.
-- DELETE FROM profiles WHERE id IN (
--   'segacd-chd-seed','3do-chd-seed','pcfx-chd-seed','jagcd-chd-seed',
--   'cdi-chd-seed','pcecd-chd-seed','neocd-chd-seed'
-- );
