-- 020_retention_buckets: expand history retention from a single global
-- "delete after N days" into per-outcome policies. Each outcome bucket
-- (successful rips = state 'done'; failed/cancelled/interrupted) gets an
-- optional max-age (days) and max-count (keep last N) limit; 0 = no limit.
-- retention.forever (seeded elsewhere, default true) remains the master
-- override. Seed sensible defaults so turning "forever" off is immediately
-- meaningful: keep successful rips indefinitely, prune failures after 14 days.

INSERT INTO settings (key, value, updated_at) VALUES
  ('retention.success.days',  '0',  '2026-05-20T00:00:00Z'),
  ('retention.success.count', '0',  '2026-05-20T00:00:00Z'),
  ('retention.failed.days',   '14', '2026-05-20T00:00:00Z'),
  ('retention.failed.count',  '0',  '2026-05-20T00:00:00Z')
ON CONFLICT(key) DO NOTHING;

-- Carry a pre-existing legacy retention.days into BOTH bucket day-limits, but
-- only when the user actually had time-based pruning enabled (forever=false
-- and days>=1). Absent rows make the subqueries NULL, so the guard is false
-- and the seeded defaults above stand. The legacy retention.days row is left
-- in place but no longer read.
UPDATE settings
SET value = (SELECT value FROM settings WHERE key = 'retention.days'),
    updated_at = '2026-05-20T00:00:00Z'
WHERE key IN ('retention.success.days', 'retention.failed.days')
  AND (SELECT value FROM settings WHERE key = 'retention.forever') = 'false'
  AND CAST((SELECT value FROM settings WHERE key = 'retention.days') AS INTEGER) >= 1;
