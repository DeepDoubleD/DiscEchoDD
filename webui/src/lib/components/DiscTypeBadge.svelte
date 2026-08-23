<script lang="ts" context="module">
  import type { DiscType } from '$lib/wire';

  type Meta = { label: string; color: string };

  // All game consoles share one warm colour so "games" read as a single
  // group at a glance; the per-badge label and the list ordering keep
  // individual systems distinguishable.
  const GAME = '#ffb37e';

  export const DISC_TYPE_META: Record<DiscType, Meta> = {
    // Video — cool family, VCD a lighter lavender related to DVD's purple.
    VCD: { label: 'VCD', color: '#c4b5fd' },
    DVD: { label: 'DVD', color: '#a78bfa' },
    BDMV: { label: 'Blu-ray', color: '#5ea3ff' },
    UHD: { label: '4K UHD', color: '#f5a524' },
    // Audio.
    AUDIO_CD: { label: 'CD', color: '#7eb6ff' },
    // Games — unified warm orange.
    PSX: { label: 'PSX', color: GAME },
    PS2: { label: 'PS2', color: GAME },
    SAT: { label: 'Saturn', color: GAME },
    DC: { label: 'Dreamcast', color: GAME },
    XBOX: { label: 'Xbox', color: GAME },
    XBOX360: { label: 'Xbox 360', color: GAME },
    WII: { label: 'Wii', color: GAME },
    PS3: { label: 'PS3', color: GAME },
    SEGACD: { label: 'Sega CD', color: GAME },
    '3DO': { label: '3DO', color: GAME },
    PCFX: { label: 'PC-FX', color: GAME },
    JAGCD: { label: 'Jaguar', color: GAME },
    CDI: { label: 'CD-i', color: GAME },
    PCECD: { label: 'PCE-CD', color: GAME },
    NEOCD: { label: 'Neo CD', color: GAME },
    CD32: { label: 'CD32', color: GAME },
    FMTOWNS: { label: 'FM Towns', color: GAME },
    PIPPIN: { label: 'Pippin', color: GAME },
    // Catch-all.
    DATA: { label: 'Data', color: '#a1a1aa' },
  };
</script>

<script lang="ts">
  export let type: DiscType;
  export let size: 'sm' | 'md' = 'sm';

  $: meta = DISC_TYPE_META[type] ?? { label: type, color: '#a1a1aa' };
  $: pad = size === 'md' ? 'text-[11px] px-2 py-[3px]' : 'text-[10px] px-1.5 py-[2px]';
</script>

<span
  class="inline-flex items-center rounded border font-semibold uppercase tracking-[0.04em] {pad}"
  style="
    color: {meta.color};
    background: color-mix(in oklab, {meta.color} 12%, transparent);
    border-color: color-mix(in oklab, {meta.color} 28%, transparent);
  "
>
  {meta.label}
</span>
