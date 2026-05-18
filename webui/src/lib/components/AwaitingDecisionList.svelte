<script lang="ts">
  import { discs, jobs } from '$lib/store';
  import type { Disc } from '$lib/wire';
  import AwaitingDecisionCard from './AwaitingDecisionCard.svelte';
  import TitlePicker from './TitlePicker.svelte';

  // A disc is "awaiting decision" when it has no active or completed
  // rip job — failed / cancelled / interrupted are intentionally NOT
  // hidden so the user is auto-prompted to retry (clear retry intent).
  // Running and queued jobs DO hide the card: an active job means the
  // disc is already being handled and re-showing the card would auto-
  // rip on its 8 s countdown → 409 conflict → infinite loop.
  // A `done` job hides the disc; from there it surfaces as "already
  // ripped, re-rip?" on the drive card (DriveHeroCard / DriveCard).
  //
  // A scan job (kind='scan') doesn't count as "active or done" since
  // it doesn't produce a rip — its sole purpose is to populate the
  // disc's title list so the picker UI can render. The picker
  // branches on disc.metadata_json.scan below.
  $: discsWithRipJob = new Set(
    $jobs
      .filter(
        (j) =>
          (j.kind ?? 'rip') !== 'scan' &&
          (j.state === 'done' || j.state === 'running' || j.state === 'queued'),
      )
      .map((j) => j.disc_id),
  );

  // No filter on candidate count: a disc with zero candidates is still
  // an awaiting decision — it just means MusicBrainz / TMDB didn't
  // recognise it, and the card needs to surface that to the user
  // (otherwise the dashboard silently swallows the insert and the
  // drive just snaps back to idle). The card itself renders the right
  // copy + affordances for zero-candidate discs based on disc.type.
  $: pending = Object.values($discs)
    .filter((d: Disc) => !discsWithRipJob.has(d.id))
    .sort((a, b) => (a.created_at < b.created_at ? 1 : -1))
    .slice(0, 3);

  // Branch per-disc: when the daemon has populated disc.metadata_json
  // with a scan block (post-scan-job), render the TitlePicker for the
  // user to select titles. Otherwise render the regular card.
  function hasScanResult(d: Disc): boolean {
    if (!d.metadata_json || d.metadata_json === '{}') return false;
    try {
      const parsed = JSON.parse(d.metadata_json) as { scan?: { titles?: unknown[] } };
      return Array.isArray(parsed?.scan?.titles) && parsed.scan.titles.length > 0;
    } catch {
      return false;
    }
  }
</script>

{#if pending.length > 0}
  <div class="space-y-3" data-testid="awaiting-decision-list">
    {#each pending as d (d.id)}
      {#if hasScanResult(d)}
        <TitlePicker disc={d} />
      {:else}
        <AwaitingDecisionCard disc={d} />
      {/if}
    {/each}
  </div>
{/if}
