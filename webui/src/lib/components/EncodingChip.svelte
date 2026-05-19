<script lang="ts">
  import type { Disc, Job } from '$lib/wire';
  import { createEventDispatcher } from 'svelte';
  import { formatDuration } from '$lib/time';
  import { formatProgress } from '$lib/formatProgress';
  import DiscArt from './DiscArt.svelte';

  export let disc: Disc;
  export let job: Job;

  // Sibling layout for the body button and the cancel button is mandatory:
  // HTML forbids nesting a button inside another button, and the parent
  // DriveCard sometimes wraps its children in a clickable anchor element.
  // The chip stays self-contained so it can be dropped into either layout
  // without the outer wrapper assuming its internals.
  const dispatch = createEventDispatcher<{
    cancel: string;
    navigate: string;
  }>();

  function onCancel(e: MouseEvent): void {
    // Stop propagation so the body button's click handler doesn't also fire
    // when the cancel button happens to overlap (sibling layout makes this
    // safe today, but a future flex/grid tweak could re-introduce overlap).
    e.stopPropagation();
    dispatch('cancel', job.id);
  }

  function onBody(): void {
    dispatch('navigate', job.id);
  }
</script>

<div
  data-testid="encoding-chip"
  class="mt-2 flex items-center gap-3 rounded-lg border border-dashed border-border bg-surface-2/40 px-3 py-2"
>
  <button
    data-testid="encoding-chip-body"
    type="button"
    class="flex flex-1 items-center gap-3 text-left"
    on:click={onBody}
  >
    <div class="h-10 w-7 flex-shrink-0 overflow-hidden rounded">
      <DiscArt {disc} size={28} ratio="portrait" />
    </div>
    <div class="min-w-0 flex-1">
      <div class="truncate text-[10px] font-medium uppercase tracking-[0.1em] text-text-3">
        Last disc — still encoding
      </div>
      <div class="truncate text-[12px] font-semibold text-text">
        {disc.title || disc.id.slice(0, 8)}
      </div>
      <div class="truncate text-[10px] text-text-3">
        Encoding · ETA {job.eta_seconds ? formatDuration(job.eta_seconds) : '—'}
      </div>
    </div>
    <div class="text-right font-mono">
      <div class="text-[14px] font-bold text-accent">{formatProgress(job.progress)}</div>
    </div>
    <div class="text-text-3" aria-hidden="true">›</div>
  </button>
  <button
    data-testid="encoding-chip-cancel"
    type="button"
    class="rounded p-1 text-text-3 hover:bg-surface-2 hover:text-error"
    title="Cancel encode"
    aria-label="Cancel encode"
    on:click={onCancel}
  >
    ✕
  </button>
</div>
