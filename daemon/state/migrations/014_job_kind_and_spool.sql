-- 014_job_kind_and_spool: split prep for the decoupled rip → transcode
-- queue. Every existing row becomes kind='rip' so historical jobs render
-- unchanged; new transcode-kind rows land via Store.CreateTranscodeJob
-- once the orchestrator wiring goes in. parent_job_id ties a transcode
-- row back to its originating rip; spool_path records where the
-- intermediate artefact lives on disk so the transcode worker can pick
-- up after the drive has been freed.
--
-- No CHECK constraint on kind: SQLite's ALTER TABLE ADD COLUMN can't
-- carry CHECK, and existing migrations (005/010/011/013) skip CHECK on
-- ADD COLUMN for the same reason. Validation lives in CreateJob /
-- CreateTranscodeJob at the Go layer.
ALTER TABLE jobs ADD COLUMN kind          TEXT NOT NULL DEFAULT 'rip';
ALTER TABLE jobs ADD COLUMN parent_job_id TEXT REFERENCES jobs(id) ON DELETE CASCADE;
ALTER TABLE jobs ADD COLUMN spool_path    TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_jobs_parent_job_id ON jobs(parent_job_id);
CREATE INDEX idx_jobs_kind_state    ON jobs(kind, state);

-- Compute queue concurrency and spool soft cap. Defaults: serial
-- transcoding (least surprise on shared hosts) and ~3 BD rips of
-- spool headroom (100 GiB).
INSERT INTO settings (key, value, updated_at)
  VALUES ('compute.concurrent_encodes', '1', strftime('%Y-%m-%dT%H:%M:%fZ','now'))
  ON CONFLICT(key) DO NOTHING;

INSERT INTO settings (key, value, updated_at)
  VALUES ('spool.cap_bytes', '107374182400', strftime('%Y-%m-%dT%H:%M:%fZ','now'))
  ON CONFLICT(key) DO NOTHING;
