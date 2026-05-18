<script lang="ts">
  import { discs, profiles, startDisc } from '$lib/store';
  import type { Disc } from '$lib/wire';
  import DiscTypeBadge from './DiscTypeBadge.svelte';
  import DiscArt from './DiscArt.svelte';

  export let disc: Disc;

  type TitleInfo = {
    id: number;
    duration_sec: number;
    chapter_count?: number;
    source_file?: string;
    size_bytes?: number;
  };

  // Read the scan payload off disc.metadata_json. The orchestrator's
  // scan-job path writes {scan: {titles: [...], scanned_at: '...'}}
  // alongside any prior metadata keys (cover_url, dvd_titles, …).
  $: liveDisc = $discs[disc.id] ?? disc;
  $: titles = parseScanTitles(liveDisc.metadata_json ?? '');

  function parseScanTitles(blob: string): TitleInfo[] {
    if (!blob || blob === '{}') return [];
    try {
      const parsed = JSON.parse(blob) as { scan?: { titles?: TitleInfo[] } };
      return parsed?.scan?.titles ?? [];
    } catch {
      return [];
    }
  }

  // Default-pick the longest title so a one-click submit matches the
  // legacy auto-pick behaviour for movies. Multi-episode discs (TV
  // box sets) typically have several similar-duration titles; the user
  // checks the rest.
  let selected: Record<number, boolean> = {};
  let initialised = false;
  $: if (!initialised && titles.length > 0) {
    let longest = titles[0];
    for (const t of titles) {
      if (t.duration_sec > longest.duration_sec) longest = t;
    }
    selected = { [longest.id]: true };
    initialised = true;
  }

  $: selectedIDs = titles.filter((t) => selected[t.id]).map((t) => t.id);
  $: anySelected = selectedIDs.length > 0;

  // Profile resolution mirrors AwaitingDecisionCard.profileForCandidate
  // but without a candidate slot — the picker fires after the user has
  // already engaged with the candidates step, so the top candidate's
  // profile is what we want.
  function profileID(): string {
    const enabled = $profiles.filter((p) => p.enabled);
    if (liveDisc.type === 'DVD') {
      const top = liveDisc.candidates?.[0];
      const wantName = top?.media_type === 'tv' ? 'DVD-Series' : 'DVD-Movie';
      return enabled.find((p) => p.disc_type === 'DVD' && p.name === wantName)?.id ?? '';
    }
    return enabled.find((p) => p.disc_type === liveDisc.type)?.id ?? '';
  }

  let starting = false;
  let errMsg = '';

  async function onConfirm(): Promise<void> {
    if (starting || !anySelected) return;
    const pid = profileID();
    if (!pid) {
      errMsg = 'No matching profile for this disc type.';
      return;
    }
    starting = true;
    errMsg = '';
    try {
      // Candidate index 0 = top match; the user has already had the
      // chance to override via the candidates step before opening the
      // picker, so we honour their last metadata choice.
      await startDisc(liveDisc.id, pid, 0, selectedIDs);
    } catch (e) {
      starting = false;
      errMsg = (e as Error).message;
    }
  }

  function formatDuration(sec: number): string {
    if (!Number.isFinite(sec) || sec <= 0) return '—';
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = sec % 60;
    if (h > 0) return `${h}h ${String(m).padStart(2, '0')}m`;
    if (m > 0) return `${m}m ${String(s).padStart(2, '0')}s`;
    return `${s}s`;
  }

  function formatBytes(n?: number): string {
    if (!n || n <= 0) return '';
    const units = ['B', 'KB', 'MB', 'GB', 'TB'];
    let u = 0;
    let v = n;
    while (v >= 1024 && u < units.length - 1) {
      v /= 1024;
      u += 1;
    }
    return `${v.toFixed(v < 10 ? 1 : 0)} ${units[u]}`;
  }
</script>

<div
  class="rounded-2xl border p-5"
  style="border-color: rgba(var(--accent-rgb),0.35); background: var(--surface-1)"
  data-disc-id={liveDisc.id}
  data-testid="title-picker"
>
  <div class="mb-4 flex gap-3">
    <DiscArt
      disc={liveDisc}
      size={64}
      ratio={liveDisc.type === 'AUDIO_CD' ? 'square' : 'portrait'}
    />
    <div class="min-w-0 flex-1">
      <div class="flex items-center gap-2">
        <DiscTypeBadge type={liveDisc.type} />
        <span class="text-[11px] font-medium uppercase tracking-[0.14em] text-text-3">
          Pick titles
        </span>
      </div>
      <div class="mt-1 truncate text-[15px] font-semibold text-text">
        {liveDisc.title || 'Untitled'}
      </div>
      <div class="mt-1 text-[11px] text-text-3">
        {titles.length} title{titles.length === 1 ? '' : 's'} found · check what to rip
      </div>
    </div>
  </div>

  <div class="space-y-2">
    {#each titles as t (t.id)}
      <label
        class="flex w-full min-h-[44px] items-center gap-3 rounded-xl border p-3 text-left"
        style="
          border-color: {selected[t.id] ? 'rgba(0,214,143,0.35)' : 'var(--border)'};
          background: {selected[t.id] ? 'rgba(0,214,143,0.04)' : 'transparent'};
        "
      >
        <input
          type="checkbox"
          bind:checked={selected[t.id]}
          class="h-5 w-5 cursor-pointer accent-accent"
        />
        <div class="min-w-0 flex-1">
          <div class="font-mono text-[13px] text-text">
            Title {t.id}
            {#if t.source_file}<span class="ml-2 text-text-3">{t.source_file}</span>{/if}
          </div>
          <div class="mt-0.5 font-mono text-[11px] text-text-3">
            {formatDuration(t.duration_sec)}{t.chapter_count
              ? ` · ${t.chapter_count} chapter${t.chapter_count === 1 ? '' : 's'}`
              : ''}{t.size_bytes ? ` · ${formatBytes(t.size_bytes)}` : ''}
          </div>
        </div>
      </label>
    {/each}
  </div>

  {#if errMsg}
    <div class="mt-3 text-[12px] text-error">{errMsg}</div>
  {/if}

  <div class="mt-5 flex flex-col gap-2 sm:flex-row">
    <button
      class="min-h-[44px] flex-1 rounded-xl bg-accent text-[14px] font-semibold text-black disabled:opacity-50"
      on:click={onConfirm}
      disabled={starting || !anySelected}
      data-testid="title-picker-confirm"
    >
      {starting
        ? 'Starting…'
        : `Rip ${selectedIDs.length} title${selectedIDs.length === 1 ? '' : 's'}`}
    </button>
  </div>
</div>
