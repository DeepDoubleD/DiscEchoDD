import type { Disc, Drive, Job } from './wire';

// Pure helpers for the disc-as-unit UI. Mirrors the daemon's
// DeriveLifecycleState semantics: "latest attempt wins", transcode
// is tied to its parent rip via parent_job_id.

/**
 * Returns the disc that originated from `drive` and is currently
 * encoding (or awaiting its encode). When multiple discs from the
 * same drive are still in flight (rare — would require the rip
 * cycle to outpace the encoder concurrency), returns the one whose
 * latest job started most recently.
 *
 * Used by the dashboard chip: when a drive is now idle / has a
 * different disc loaded, the chip shows the prior disc whose
 * encode is still cooking.
 */
export function lastEncodingDiscForDrive(
  drive: Drive,
  discs: Record<string, Disc>,
  jobs: Job[],
): Disc | undefined {
  const candidates = Object.values(discs).filter(
    (d) =>
      d.drive_id === drive.id &&
      (d.lifecycle_state === 'encoding' || d.lifecycle_state === 'awaiting_encode'),
  );
  if (candidates.length <= 1) return candidates[0];
  const scoreOf = (d: Disc): number => {
    const j = jobs
      .filter((x) => x.disc_id === d.id)
      .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))[0];
    return j ? Date.parse(j.created_at) : 0;
  };
  return candidates.sort((a, b) => scoreOf(b) - scoreOf(a))[0];
}

/**
 * Returns the active (queued or running) transcode job for a disc,
 * or undefined when no such job exists. Used by EncodingChip and
 * the dashboard hero to render progress / ETA / cancel actions.
 */
export function activeTranscodeJob(disc: Disc, jobs: Job[]): Job | undefined {
  return jobs
    .filter(
      (j) =>
        j.disc_id === disc.id &&
        j.kind === 'transcode' &&
        (j.state === 'queued' || j.state === 'running'),
    )
    .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))[0];
}
