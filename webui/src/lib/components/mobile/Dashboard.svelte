<script lang="ts">
  import { drives, jobs, discs, profiles, liveStatus, stats } from '$lib/store';
  import AppBar from './AppBar.svelte';
  import TabBar from './TabBar.svelte';
  import LiveDot from '$lib/components/LiveDot.svelte';
  import AwaitingDecisionList from '$lib/components/AwaitingDecisionList.svelte';
  import StatsGrid from './StatsGrid.svelte';
  import DriveCard from './DriveCard.svelte';
  import QueuedRow from './QueuedRow.svelte';
  import type { Job } from '$lib/wire';

  const TERMINAL_STATES: ReadonlyArray<Job['state']> = [
    'done',
    'failed',
    'cancelled',
    'interrupted',
  ];

  $: activeJobs = $jobs.filter((j) => !TERMINAL_STATES.includes(j.state));
  // Per-drive rip queue and global encode queue render as separate
  // sections; legacy / monolithic jobs without an explicit kind
  // default to 'rip'.
  $: queuedJobs = activeJobs.filter(
    (j) => j.state === 'queued' && (j.kind ?? 'rip') !== 'transcode',
  );
  // Encode-queue rows include both queued AND in-flight transcodes
  // so the user can see what's currently running on the compute pool.
  $: encodeJobs = activeJobs
    .filter((j) => j.kind === 'transcode')
    .sort((a, b) => {
      const aQ = a.state === 'queued' ? 1 : 0;
      const bQ = b.state === 'queued' ? 1 : 0;
      return aQ - bQ;
    });
  $: queuedByDrive = activeJobs.reduce<Record<string, number>>((acc, j) => {
    if (j.state === 'queued' && j.drive_id) {
      acc[j.drive_id] = (acc[j.drive_id] ?? 0) + 1;
    }
    return acc;
  }, {});
</script>

<div class="min-h-screen pb-24">
  <AppBar title="Drives">
    <div slot="right" class="flex items-center gap-2">
      <LiveDot label={$liveStatus === 'live' ? 'LIVE' : 'WAIT'} />
    </div>
  </AppBar>

  <div class="mt-1">
    <StatsGrid stats={$stats} />
  </div>

  <div class="mt-5 px-4">
    <AwaitingDecisionList />
  </div>

  <div class="mt-5 px-4">
    <div
      class="mb-2 font-medium uppercase tracking-[0.14em] text-text-3"
      style="font-size: var(--ts-overline)"
    >
      Optical drives
    </div>
    <div class="space-y-2">
      {#each $drives as d (d.id)}
        {@const activeJob = activeJobs.find((j) => j.drive_id === d.id && j.state !== 'queued')}
        {@const discID = d.current_disc_id ?? activeJob?.disc_id}
        {@const disc = discID ? $discs[discID] : undefined}
        {@const profile = activeJob
          ? $profiles.find((p) => p.id === activeJob.profile_id)
          : undefined}
        <DriveCard
          drive={d}
          {disc}
          job={activeJob}
          {profile}
          queuedCount={queuedByDrive[d.id] ?? 0}
          href={activeJob ? `/jobs/${activeJob.id}` : undefined}
        />
      {:else}
        <div
          class="rounded-2xl border border-dashed border-border p-4 text-center text-text-3"
          style="font-size: var(--ts-meta)"
        >
          No drives detected.
        </div>
      {/each}
    </div>
  </div>

  <div class="mt-5 px-4">
    <div
      class="mb-2 font-medium uppercase tracking-[0.14em] text-text-3"
      style="font-size: var(--ts-overline)"
    >
      Queued ({queuedJobs.length})
    </div>
    {#if queuedJobs.length === 0}
      <div
        class="rounded-2xl border border-dashed border-border p-4 text-center text-text-3"
        style="font-size: var(--ts-meta)"
      >
        No queued jobs.
      </div>
    {:else}
      <div class="overflow-hidden rounded-2xl border border-border bg-surface-1">
        {#each queuedJobs as j (j.id)}
          {@const disc = $discs[j.disc_id]}
          {@const profile = $profiles.find((p) => p.id === j.profile_id)}
          <QueuedRow job={j} {disc} {profile} />
        {/each}
      </div>
    {/if}
  </div>

  {#if encodeJobs.length > 0}
    <div class="mt-5 px-4">
      <div
        class="mb-2 font-medium uppercase tracking-[0.14em] text-text-3"
        style="font-size: var(--ts-overline)"
      >
        Encode queue ({encodeJobs.length})
      </div>
      <div class="overflow-hidden rounded-2xl border border-border bg-surface-1">
        {#each encodeJobs as j (j.id)}
          {@const disc = $discs[j.disc_id]}
          {@const profile = $profiles.find((p) => p.id === j.profile_id)}
          <QueuedRow job={j} {disc} {profile} />
        {/each}
      </div>
    </div>
  {/if}
</div>

<TabBar active="dashboard" />
