<script lang="ts">
  import { onMount } from 'svelte';
  import {
    settings,
    updateRetention,
    fetchRetentionStatus,
    runRetentionNow,
    type RetentionPolicyInput,
  } from '$lib/store';
  import { pushToast } from '$lib/toasts';
  import { relativeTime } from '$lib/time';
  import type { RetentionStatus } from '$lib/wire';

  const inputCls =
    'w-20 rounded-md border border-border bg-surface-2 px-2 py-1 text-[13px] text-text';

  function numOrNull(s: string | undefined): number | null {
    const n = parseInt(s ?? '', 10);
    return Number.isInteger(n) && n > 0 ? n : null;
  }

  // Working copy. Number inputs bind as number | null; blank = no limit.
  let forever = true;
  let successDays: number | null = null;
  let successCount: number | null = null;
  let failedDays: number | null = null;
  let failedCount: number | null = null;

  let saving = false;
  let running = false;
  let error: string | null = null;
  let status: RetentionStatus | null = null;

  // Seed the working copy once settings load, then stop so a mid-edit SSE
  // refresh can't clobber what the user is typing.
  let initialized = false;
  $: if (!initialized && $settings['retention.forever'] !== undefined) {
    forever = $settings['retention.forever'] === 'true';
    successDays = numOrNull($settings['retention.success.days']);
    successCount = numOrNull($settings['retention.success.count']);
    failedDays = numOrNull($settings['retention.failed.days']);
    failedCount = numOrNull($settings['retention.failed.count']);
    initialized = true;
  }

  $: working = {
    forever,
    successDays,
    successCount,
    failedDays,
    failedCount,
  } satisfies RetentionPolicyInput;
  $: anyKnob = [successDays, successCount, failedDays, failedCount].some((v) => (v ?? 0) > 0);
  $: invalid = !forever && !anyKnob;

  // Live preview: debounce a status fetch with the current (unsaved) edits.
  let previewTimer: ReturnType<typeof setTimeout> | undefined;
  function schedulePreview(p: RetentionPolicyInput): void {
    clearTimeout(previewTimer);
    previewTimer = setTimeout(async () => {
      try {
        status = await fetchRetentionStatus(p);
      } catch {
        // preview is best-effort; keep the previous numbers
      }
    }, 350);
  }
  $: if (initialized) schedulePreview(working);
  onMount(() => () => clearTimeout(previewTimer));

  function fmtAbsolute(iso: string): string {
    const d = new Date(iso);
    return Number.isNaN(d.getTime()) ? '' : d.toLocaleString();
  }

  async function onRunNow(): Promise<void> {
    running = true;
    try {
      const r = await runRetentionNow();
      pushToast(
        'success',
        r.total > 0
          ? `Removed ${r.total} history ${r.total === 1 ? 'entry' : 'entries'}`
          : 'Nothing to clean up',
      );
      status = await fetchRetentionStatus();
    } catch (e) {
      pushToast('error', (e as Error).message);
    } finally {
      running = false;
    }
  }

  async function onSave(): Promise<void> {
    if (invalid) {
      error = 'Set at least one limit, or keep history forever.';
      return;
    }
    error = null;
    saving = true;
    try {
      await updateRetention(working);
      pushToast('success', 'Retention settings saved');
      status = await fetchRetentionStatus();
    } catch (e) {
      error = (e as Error).message;
    } finally {
      saving = false;
    }
  }
</script>

<section class="rounded-2xl border border-border bg-surface-1 p-5">
  <h2 class="text-[14px] font-semibold text-text">History retention</h2>
  <p class="mt-1 text-[12px] text-text-3">
    How long completed-rip history records are kept. Only deletes history records — your ripped
    files are never removed.
  </p>

  <div class="mt-4 space-y-4">
    <label class="flex items-center gap-2 text-[12px] text-text-2">
      <input type="checkbox" bind:checked={forever} />
      Keep all history forever
    </label>

    {#if !forever}
      <div class="rounded-md border border-border bg-surface-2 p-3">
        <div class="text-[12px] font-semibold text-text">Successful rips</div>
        <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:gap-6">
          <label class="flex items-center gap-2 text-[12px] text-text-2">
            Delete after
            <input
              type="number"
              min="1"
              placeholder="∞"
              bind:value={successDays}
              class={inputCls}
            />
            days
          </label>
          <label class="flex items-center gap-2 text-[12px] text-text-2">
            Keep at most
            <input
              type="number"
              min="1"
              placeholder="∞"
              bind:value={successCount}
              class={inputCls}
            />
            entries
          </label>
        </div>
      </div>

      <div class="rounded-md border border-border bg-surface-2 p-3">
        <div class="text-[12px] font-semibold text-text">Failed / cancelled rips</div>
        <div class="mt-2 flex flex-col gap-2 sm:flex-row sm:gap-6">
          <label class="flex items-center gap-2 text-[12px] text-text-2">
            Delete after
            <input type="number" min="1" placeholder="∞" bind:value={failedDays} class={inputCls} />
            days
          </label>
          <label class="flex items-center gap-2 text-[12px] text-text-2">
            Keep at most
            <input
              type="number"
              min="1"
              placeholder="∞"
              bind:value={failedCount}
              class={inputCls}
            />
            entries
          </label>
        </div>
      </div>

      <p class="text-[11px] text-text-3">Leave a field blank for no limit.</p>
    {/if}

    {#if status}
      {@const total = status.success_total + status.failed_total}
      {@const del = status.would_delete.total}
      <div class="rounded-md border border-border bg-surface-2 px-3 py-2 text-[12px] text-text-2">
        {#if forever}
          Keeping all {total} history {total === 1 ? 'entry' : 'entries'}.
        {:else}
          This policy removes <span class="font-semibold text-warn">{del}</span> of {total} history {total ===
          1
            ? 'entry'
            : 'entries'} now, keeping {total - del}.
        {/if}
        <div class="mt-1 text-[11px] text-text-3">
          {#if status.last_run_at}
            Last cleanup {relativeTime(status.last_run_at)} · removed {status.last_run_count}.
          {:else}
            No cleanup has run yet.
          {/if}
          Next scheduled {fmtAbsolute(status.next_run_at)}.
        </div>
      </div>
    {/if}

    {#if error}<div class="text-[12px] text-error">{error}</div>{/if}

    <div class="flex items-center gap-3">
      <button
        on:click={onSave}
        disabled={saving}
        class="rounded-md bg-accent px-3 py-1.5 text-[12px] font-semibold text-black disabled:opacity-50"
      >
        Save
      </button>
      <button
        on:click={onRunNow}
        disabled={running || forever}
        title={forever
          ? 'Disabled while keeping history forever'
          : 'Runs the saved policy now (save your changes first)'}
        class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2 transition-colors hover:border-border-strong disabled:opacity-50"
      >
        {running ? 'Cleaning…' : 'Run cleanup now'}
      </button>
    </div>
  </div>
</section>
