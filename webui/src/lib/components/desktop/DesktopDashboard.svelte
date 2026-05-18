<script lang="ts">
  import { drives, jobs, discs, profiles, selectedJobID, stats } from '$lib/store';
  import DriveHeroCard from './DriveHeroCard.svelte';
  import QueueTable from './QueueTable.svelte';
  import EncodeQueueCard from './EncodeQueueCard.svelte';
  import JobDetailPanel from './JobDetailPanel.svelte';
  import RipCard from '$lib/components/RipCard.svelte';
  import StatsRow from './StatsRow.svelte';
  import AwaitingDecisionList from '../AwaitingDecisionList.svelte';
  import type { Job } from '$lib/wire';

  const TERMINAL_STATES: ReadonlyArray<Job['state']> = [
    'done',
    'failed',
    'cancelled',
    'interrupted',
  ];

  $: activeJobs = $jobs.filter((j) => !TERMINAL_STATES.includes(j.state));
  // Split active jobs by kind so the rip queue (per-drive) and the
  // encode queue (global compute pool) render in separate cards. The
  // user understands at a glance which side the bottleneck is on.
  // Treat absent kind as 'rip' since that's the schema default for
  // legacy / monolithic-pipeline jobs.
  $: ripActiveJobs = activeJobs.filter((j) => (j.kind ?? 'rip') !== 'transcode');
  $: transcodeActiveJobs = activeJobs.filter((j) => j.kind === 'transcode');
  $: orderedJobs = [...ripActiveJobs].sort((a, b) => {
    const aQ = a.state === 'queued' ? 1 : 0;
    const bQ = b.state === 'queued' ? 1 : 0;
    return aQ - bQ;
  });
  $: orderedTranscodeJobs = [...transcodeActiveJobs].sort((a, b) => {
    const aQ = a.state === 'queued' ? 1 : 0;
    const bQ = b.state === 'queued' ? 1 : 0;
    return aQ - bQ;
  });
  $: queuedByDrive = $jobs.reduce<Record<string, number>>((acc, j) => {
    if (j.state === 'queued' && j.drive_id) {
      acc[j.drive_id] = (acc[j.drive_id] ?? 0) + 1;
    }
    return acc;
  }, {});
  // The detail panel only renders when the user has explicitly opted in
  // via a queue-row click. No fallback to "first running job" — that
  // duplicated the hero RipCard for the most common case (single rip).
  $: selectedJob = $selectedJobID ? $jobs.find((j) => j.id === $selectedJobID) : undefined;

  function toggleSelected(id: string): void {
    selectedJobID.update((cur) => (cur === id ? null : id));
  }
</script>

<div class="mx-auto min-h-screen max-w-screen-2xl p-6">
  <!-- Top widgets row (active jobs / today ripped / library / failures) -->
  <StatsRow stats={$stats} />

  <!-- Hero band — idle drives render as DriveHeroCard; busy drives
       swap to RipCard. A busy drive whose active job is selected in
       the sidebar collapses entirely from the hero band to avoid the
       same RipCard rendering twice on screen. -->
  <div class="mb-6 grid gap-4" style="grid-template-columns: repeat(auto-fit, minmax(380px, 1fr))">
    {#each $drives as d (d.id)}
      {@const activeJob = activeJobs.find((j) => j.drive_id === d.id && j.state !== 'queued')}
      {@const discID = d.current_disc_id ?? activeJob?.disc_id}
      {@const disc = discID ? $discs[discID] : undefined}
      {#if activeJob && d.state !== 'idle' && $selectedJobID === activeJob.id}
        <!-- Hero RipCard collapsed: this drive's job is in the sidebar. -->
      {:else if activeJob && d.state !== 'idle'}
        {@const profile = $profiles.find((p) => p.id === activeJob.profile_id)}
        <div data-drive-id={d.id}>
          <RipCard drive={d} {disc} job={activeJob} {profile} />
        </div>
      {:else}
        <DriveHeroCard
          drive={d}
          {disc}
          job={activeJob}
          queuedCount={queuedByDrive[d.id] ?? 0}
          on:select={(e) => e.detail && toggleSelected(e.detail)}
        />
      {/if}
    {:else}
      <div class="rounded-2xl border border-dashed border-border p-6 text-center text-text-3">
        No drives detected.
      </div>
    {/each}
  </div>

  <!-- Awaiting decision -->
  <div class="mb-6">
    <AwaitingDecisionList />
  </div>

  <!-- Queue + detail. Two-column layout only when a job is selected;
       otherwise the queue takes the full width. -->
  {#if selectedJob}
    <div class="grid gap-6" style="grid-template-columns: 1fr 360px">
      <div class="space-y-4">
        <div>
          <div class="mb-2 px-1 text-[10px] font-medium uppercase tracking-[0.14em] text-text-3">
            Rip queue
          </div>
          <QueueTable
            jobs={orderedJobs}
            selectedJobID={$selectedJobID}
            on:select={(e) => toggleSelected(e.detail)}
          />
        </div>
        {#if orderedTranscodeJobs.length > 0}
          <div>
            <div class="mb-2 px-1 text-[10px] font-medium uppercase tracking-[0.14em] text-text-3">
              Encode queue
            </div>
            <EncodeQueueCard
              jobs={orderedTranscodeJobs}
              selectedJobID={$selectedJobID}
              on:select={(e) => toggleSelected(e.detail)}
            />
          </div>
        {/if}
      </div>
      <div class="sticky top-20 self-start">
        <JobDetailPanel job={selectedJob} />
      </div>
    </div>
  {:else}
    <div class="space-y-4">
      <div>
        <div class="mb-2 px-1 text-[10px] font-medium uppercase tracking-[0.14em] text-text-3">
          Rip queue
        </div>
        <QueueTable
          jobs={orderedJobs}
          selectedJobID={null}
          on:select={(e) => toggleSelected(e.detail)}
        />
      </div>
      {#if orderedTranscodeJobs.length > 0}
        <div>
          <div class="mb-2 px-1 text-[10px] font-medium uppercase tracking-[0.14em] text-text-3">
            Encode queue
          </div>
          <EncodeQueueCard
            jobs={orderedTranscodeJobs}
            selectedJobID={null}
            on:select={(e) => toggleSelected(e.detail)}
          />
        </div>
      {/if}
    </div>
  {/if}
</div>
