<script lang="ts">
  import { onMount } from 'svelte';
  import { goto } from '$app/navigation';
  import { liveStatus } from '$lib/store';
  import { fetchDiscHistory } from '$lib/api';
  import type { DiscHistoryResponse } from '$lib/wire';
  import AppBar from '$lib/components/mobile/AppBar.svelte';
  import TabBar from '$lib/components/mobile/TabBar.svelte';
  import LiveDot from '$lib/components/LiveDot.svelte';
  import { isDesktop } from '$lib/viewport';
  import DiscHistoryRow from '$lib/components/DiscHistoryRow.svelte';
  import ClearHistoryButton from '$lib/components/ClearHistoryButton.svelte';

  const PAGE_SIZE = 50;

  let resp: DiscHistoryResponse | null = null;
  let offset = 0;
  let loading = false;
  let error: string | null = null;

  async function load(): Promise<void> {
    loading = true;
    error = null;
    try {
      resp = await fetchDiscHistory(PAGE_SIZE, offset);
    } catch (e) {
      error = e instanceof Error ? e.message : String(e);
    } finally {
      loading = false;
    }
  }

  onMount(load);

  function gotoJob(jobID: string): void {
    if (jobID) void goto(`/jobs/${encodeURIComponent(jobID)}`);
  }

  function onCleared(): void {
    offset = 0;
    void load();
  }

  // Removing a single row locally avoids a full reload's flicker/scroll
  // jump; total/pagination still reload from the server since deleting
  // near a page boundary can shift what belongs on this page.
  function onDeleted(e: CustomEvent<string>): void {
    if (!resp) return;
    resp = { ...resp, discs: resp.discs.filter((r) => r.disc.id !== e.detail) };
    void load();
  }

  function retry(): void {
    void load();
  }

  function prevPage(): void {
    offset = Math.max(0, offset - PAGE_SIZE);
    void load();
  }

  function nextPage(): void {
    offset += PAGE_SIZE;
    void load();
  }

  $: total = resp?.total ?? 0;
  $: pageCount = total > 0 ? Math.max(1, Math.ceil(total / PAGE_SIZE)) : 1;
  $: currentPage = Math.floor(offset / PAGE_SIZE) + 1;
</script>

<div class="min-h-screen pb-24">
  <AppBar title="History" subtitle="all discs">
    <div slot="right" class="flex items-center gap-2">
      <ClearHistoryButton {total} on:cleared={onCleared} />
      <span class="lg:hidden">
        <LiveDot label={$liveStatus === 'live' ? 'LIVE' : 'WAIT'} />
      </span>
    </div>
  </AppBar>

  <div class="mx-auto max-w-screen-2xl px-5">
    {#if error}
      <div
        class="mb-3 rounded-xl border px-4 py-3"
        style="border-color: rgba(255,91,91,0.3); background: rgba(255,91,91,0.1)"
      >
        <div class="text-[13px] font-medium" style="color: var(--error)">
          Couldn't load history.
        </div>
        <div class="mt-1 font-mono text-[11px] text-text-3">{error}</div>
        <button
          class="mt-2 min-h-[36px] rounded-md border border-border px-3 text-[12px] text-text-2"
          on:click={retry}>Retry</button
        >
      </div>
    {/if}

    {#if loading && !resp}
      <div class="mt-12 flex justify-center">
        <span class="font-mono text-[12px] text-text-3">Loading…</span>
      </div>
    {:else if resp && resp.discs.length === 0}
      <div class="mt-12 text-center">
        <div class="mb-2 text-[16px] font-semibold text-text-2">No rips yet</div>
        <div class="mx-auto max-w-[300px] text-[13px] text-text-3">
          Insert a disc to get started.
        </div>
      </div>
    {:else if resp}
      {#each resp.discs as row (row.disc.id)}
        <DiscHistoryRow {row} on:navigate={(e) => gotoJob(e.detail)} on:deleted={onDeleted} />
      {/each}

      {#if total > PAGE_SIZE}
        <div class="mt-4 flex items-center justify-between text-[12px] text-text-3">
          <button
            class="rounded border border-border px-3 py-1.5 hover:bg-surface-2 disabled:opacity-40"
            disabled={offset === 0 || loading}
            on:click={prevPage}
          >
            ← Previous
          </button>
          <span>Page {currentPage} of {pageCount} · {total} discs</span>
          <button
            class="rounded border border-border px-3 py-1.5 hover:bg-surface-2 disabled:opacity-40"
            disabled={offset + PAGE_SIZE >= total || loading}
            on:click={nextPage}
          >
            Next →
          </button>
        </div>
      {/if}
    {/if}
  </div>
</div>

{#if !$isDesktop}
  <TabBar active="history" />
{/if}
