-- 001_initial.sql's discs.type CHECK constraint enumerated the disc
-- types that existed at the time (AUDIO_CD, DVD, BDMV, UHD, PSX, PS2,
-- XBOX, SAT, DC, VCD, DATA) and was never extended as new console
-- types were added. Ten disc types added since -- SEGACD, 3DO, PCFX,
-- JAGCD, CDI, PCECD, NEOCD, CD32, FMTOWNS, PIPPIN -- have been unable
-- to persist a disc row at all (CreateDisc fails with "CHECK
-- constraint failed"), and XBOX360 hit the same wall the moment it was
-- added. Rather than extend the enum yet again -- the same fix that
-- already failed to hold twice -- this drops the CHECK entirely.
-- Application-level type safety (state.DiscType, the pipeline
-- registry, classify.go) already governs which values are ever
-- written; a DB-level enum duplicating that has only been a
-- maintenance trap, not a real safety net.
--
-- SQLite can't drop a CHECK constraint in place, so this rebuilds the
-- table (the standard SQLite ALTER-TABLE procedure: create the new
-- shape, copy rows, drop the old table, rename). defer_foreign_keys
-- lets DROP TABLE proceed inside this transaction despite jobs.disc_id
-- referencing discs(id) ON DELETE CASCADE -- the FK is revalidated at
-- COMMIT, by which point discs (renamed from discs_new) exists again
-- with the same rows.
PRAGMA defer_foreign_keys = ON;

CREATE TABLE discs_new (
  id                TEXT PRIMARY KEY,
  drive_id          TEXT REFERENCES drives(id) ON DELETE SET NULL,
  type              TEXT NOT NULL,
  title             TEXT NOT NULL DEFAULT '',
  year              INTEGER NOT NULL DEFAULT 0,
  runtime_seconds   INTEGER NOT NULL DEFAULT 0,
  size_bytes_raw    INTEGER NOT NULL DEFAULT 0,
  toc_hash          TEXT NOT NULL DEFAULT '',
  metadata_provider TEXT NOT NULL DEFAULT '',
  metadata_id       TEXT NOT NULL DEFAULT '',
  candidates_json   TEXT NOT NULL DEFAULT '[]',
  created_at        TEXT NOT NULL,
  metadata_json     TEXT NOT NULL DEFAULT '{}'
);

INSERT INTO discs_new (
  id, drive_id, type, title, year, runtime_seconds, size_bytes_raw,
  toc_hash, metadata_provider, metadata_id, candidates_json, created_at,
  metadata_json
)
SELECT
  id, drive_id, type, title, year, runtime_seconds, size_bytes_raw,
  toc_hash, metadata_provider, metadata_id, candidates_json, created_at,
  metadata_json
FROM discs;

DROP TABLE discs;

ALTER TABLE discs_new RENAME TO discs;

CREATE UNIQUE INDEX idx_discs_drive_toc_unique
  ON discs(drive_id, toc_hash)
  WHERE toc_hash != '';
