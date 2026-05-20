-- Remove all README-screenshot mock data from the local DB.
BEGIN;
DELETE FROM job_steps WHERE job_id LIKE 'shot-%';
DELETE FROM jobs   WHERE id LIKE 'shot-%';
DELETE FROM discs  WHERE id LIKE 'shot-%';
DELETE FROM drives WHERE id LIKE 'shot-%';
COMMIT;
