<script lang="ts">
  import type { DiscHistoryRow } from '$lib/wire';
  import { createEventDispatcher } from 'svelte';
  import LifecyclePill from './LifecyclePill.svelte';
  import DiscArt from './DiscArt.svelte';
  import DiscTypeBadge from './DiscTypeBadge.svelte';
  import { formatDuration } from '$lib/time';
  import { formatBytes } from '$lib/format';
  import { jobs, startDisc, cancelJob } from '$lib/store';
  import { lastDoneJobForDisc } from './lastDoneJobForDisc';
  import { pushToast } from '$lib/toasts';

  export let row: DiscHistoryRow;

  // Sibling layout (body button + action button under a wrapping div) is
  // mandatory — HTML forbids nesting a button inside another button, and
  // the parent list may wrap us in further click handlers. Mirrors the
  // EncodingChip / HistoryRow hygiene.
  const dispatch = createEventDispatcher<{
    navigate: string;
  }>();

  $: state = row.disc.lifecycle_state ?? 'awaiting_decision';
  $: inFlight = state === 'ripping' || state === 'encoding';
  $: failedLike = state === 'failed' || state === 'interrupted' || state === 'cancelled';
  $: actionLabel = failedLike ? 'Retry' : inFlight ? 'Stop' : 'Re-rip';
  $: navigateTo = row.latest_transcode_job_id || row.latest_rip_job_id;

  let busy = false;

  async function onAction(e: MouseEvent): Promise<void> {
    e.stopPropagation();
    if (busy) return;
    busy = true;
    try {
      if (inFlight) {
        const jobID = row.latest_transcode_job_id || row.latest_rip_job_id;
        await cancelJob(jobID);
        return;
      }
      // Re-rip / Retry reuses the profile from the most recent done job
      // for this disc — same approach as DriveHeroCard's re-rip button
      // (lastDoneJobForDisc is the canonical helper per CLAUDE.md). Falls
      // back to the latest rip job's profile when no done job exists
      // (failed-only history → user wants to retry the same profile).
      const lastDone = lastDoneJobForDisc($jobs, row.disc.id);
      const fallback = $jobs.find((j) => j.id === row.latest_rip_job_id);
      const profileID = lastDone?.profile_id ?? fallback?.profile_id;
      if (!profileID) {
        pushToast('error', 'Cannot determine profile to re-rip with');
        return;
      }
      await startDisc(row.disc.id, profileID, 0);
    } catch (err) {
      pushToast('error', (err as Error).message);
    } finally {
      busy = false;
    }
  }

  function onBody(): void {
    dispatch('navigate', navigateTo);
  }
</script>

<div data-testid="disc-history-row" class="mb-2 rounded-2xl border border-border bg-surface-1 p-3">
  <div class="flex items-center gap-3">
    <button
      data-testid="disc-history-body"
      type="button"
      class="flex flex-1 items-center gap-3 text-left"
      on:click={onBody}
    >
      <div class="h-[72px] w-[52px] flex-shrink-0 overflow-hidden rounded">
        <DiscArt disc={row.disc} size={52} ratio="portrait" />
      </div>
      <div class="min-w-0 flex-1">
        <div class="mb-1 flex items-center gap-2">
          <DiscTypeBadge type={row.disc.type} />
          <span class="truncate text-[13px] font-semibold text-text">
            {row.disc.title || row.disc.id.slice(0, 8)}
          </span>
          <LifecyclePill {state} />
        </div>
        <div class="truncate text-[11px] text-text-3">
          {row.disc.year ?? ''}
          {row.disc.runtime_seconds ? '· ' + formatDuration(row.disc.runtime_seconds) : ''}
          {row.output_bytes ? '· ' + formatBytes(row.output_bytes) : ''}
          {row.attempt_summary ? '· ' + row.attempt_summary : ''}
        </div>
      </div>
    </button>
    <div class="flex-shrink-0">
      <button
        data-testid="disc-history-action"
        type="button"
        class="min-h-[36px] rounded-xl border border-border bg-surface-2 px-3 text-[13px] font-medium text-accent disabled:opacity-50"
        on:click={onAction}
        disabled={busy}
      >
        {actionLabel}
      </button>
    </div>
  </div>
</div>
