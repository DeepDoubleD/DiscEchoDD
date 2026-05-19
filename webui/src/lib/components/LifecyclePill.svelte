<script lang="ts">
  import type { DiscLifecycleState } from '$lib/wire';

  export let state: DiscLifecycleState;

  // Match AccurateRipBadge's convention: scoped styles with semantic
  // tones rather than Tailwind palette utilities, so the colours track
  // the rest of the app (--warn / --error / --info / --accent) and the
  // dark/light/contrast theme switches in app.css apply automatically.
  const labels: Record<DiscLifecycleState, string> = {
    awaiting_decision: 'Awaiting decision',
    ripping: 'Ripping',
    awaiting_encode: 'Awaiting encode',
    encoding: 'Encoding',
    done: 'Done',
    failed: 'Failed',
    cancelled: 'Cancelled',
    interrupted: 'Interrupted',
  };

  const tones: Record<DiscLifecycleState, string> = {
    awaiting_decision: 'tone-warn',
    ripping: 'tone-warn',
    awaiting_encode: 'tone-warn',
    encoding: 'tone-info',
    done: 'tone-accent',
    failed: 'tone-error',
    cancelled: 'tone-muted',
    interrupted: 'tone-error',
  };
</script>

<span
  data-testid="lifecycle-pill"
  data-state={state}
  class={`inline-block rounded-full px-2 py-0.5 text-[10px] font-bold uppercase tracking-[0.12em] ${tones[state]}`}
>
  {labels[state]}
</span>

<style>
  .tone-warn {
    background: color-mix(in oklab, var(--warn) 18%, transparent);
    color: var(--warn);
  }
  .tone-info {
    background: color-mix(in oklab, var(--info) 18%, transparent);
    color: var(--info);
  }
  .tone-accent {
    background: var(--accent-soft);
    color: var(--accent);
  }
  .tone-error {
    background: color-mix(in oklab, var(--error) 18%, transparent);
    color: var(--error);
  }
  .tone-muted {
    background: var(--surface-2);
    color: var(--text-3);
  }
</style>
