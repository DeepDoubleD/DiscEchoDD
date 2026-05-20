<script lang="ts">
  import Icon from '$lib/icons/Icon.svelte';
  import type { IconName } from '$lib/icons/Icon.svelte';

  // tone drives colour: 'warn' (amber) for "this won't work without X",
  // 'info' (accent) for softer "you'll need X configured" notes.
  export let tone: 'warn' | 'info' = 'warn';
  export let title: string;

  $: icon = (tone === 'warn' ? 'alert' : 'key') as IconName;
</script>

<div
  class="rounded-md border px-3 py-2.5 text-[12px] {tone === 'warn'
    ? 'border-warn/30 bg-warn/10'
    : 'border-accent/30 bg-accent/10'}"
>
  <div
    class="mb-1 flex items-center gap-1.5 font-semibold {tone === 'warn'
      ? 'text-warn'
      : 'text-accent'}"
  >
    <Icon name={icon} size={14} />
    {title}
  </div>
  <div class="text-text-2">
    <slot />
  </div>
</div>
