<script lang="ts">
  import { discs, profiles, startDisc, fetchEpisodes, type EpisodeInfo } from '$lib/store';
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
  // TV discs (TMDB media_type='tv') get the episode-mapping column.
  // Movies skip it entirely so the picker is a simple title list.
  $: isTV = (liveDisc.candidates ?? []).some(
    (c) => c.media_type === 'tv' && c.tmdb_id && c.tmdb_id > 0,
  );

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

  // Selected order preserves user click sequence (insertion order on
  // the object); we keep them sorted by title id so multi-title
  // auto-mapping is deterministic.
  $: selectedIDs = titles.filter((t) => selected[t.id]).map((t) => t.id);
  $: anySelected = selectedIDs.length > 0;

  // ─── TV episode mapping ──────────────────────────────────────────
  let season = 1;
  let episodes: EpisodeInfo[] = [];
  let episodesPending = false;
  let episodesError = '';
  let episodeMap: Record<number, number> = {}; // titleID → episode number

  async function loadEpisodes(): Promise<void> {
    if (!isTV) return;
    episodesPending = true;
    episodesError = '';
    try {
      episodes = await fetchEpisodes(liveDisc.id, season);
      autoAssignEpisodes();
    } catch (e) {
      episodesError = (e as Error).message;
      episodes = [];
    } finally {
      episodesPending = false;
    }
  }

  // Auto-assign episodes in selected order: title 1 → episode N1,
  // title 2 → episode N2, etc. Users can override per-title via the
  // dropdown. Re-runs when season changes (because episodes refresh)
  // or selection changes. Preserves user overrides where possible.
  function autoAssignEpisodes(): void {
    if (episodes.length === 0) return;
    const next: Record<number, number> = {};
    let nextEpIdx = 0;
    for (const id of selectedIDs) {
      if (episodeMap[id] && episodes.some((e) => e.number === episodeMap[id])) {
        // Preserve a still-valid user override.
        next[id] = episodeMap[id];
      } else {
        if (nextEpIdx < episodes.length) {
          next[id] = episodes[nextEpIdx].number;
        }
      }
      nextEpIdx++;
    }
    episodeMap = next;
  }

  // Refetch episodes when the user changes season. Initial fetch
  // happens on $: isTV transition below.
  let seasonInit = false;
  $: if (isTV && !seasonInit) {
    seasonInit = true;
    void loadEpisodes();
  }
  let lastSeason = season;
  $: if (isTV && season !== lastSeason) {
    lastSeason = season;
    void loadEpisodes();
  }
  // Re-run auto-assign when selection changes (without re-fetching
  // episodes — they only depend on season).
  $: if (isTV && episodes.length > 0 && selectedIDs.length > 0) {
    autoAssignEpisodes();
  }

  // ─── Profile + submit ────────────────────────────────────────────
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
      await startDisc(liveDisc.id, pid, 0, {
        titleIDs: selectedIDs,
        season: isTV ? season : undefined,
        episodeMap: isTV ? episodeMap : undefined,
      });
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

  {#if isTV}
    <div class="mb-3 flex flex-wrap items-center gap-3 rounded-xl border border-border p-3">
      <label class="flex items-center gap-2 text-[12px] text-text-2">
        Season
        <input
          type="number"
          min="0"
          max="99"
          step="1"
          bind:value={season}
          class="w-16 rounded-md border border-border bg-surface-2 px-2 py-1 text-[13px]"
        />
      </label>
      {#if episodesPending}
        <span class="text-[11px] text-text-3">Loading episodes…</span>
      {:else if episodesError}
        <span class="text-[11px] text-error">Episodes: {episodesError}</span>
      {:else if episodes.length === 0}
        <span class="text-[11px] text-text-3">No episodes found for this season.</span>
      {:else}
        <span class="text-[11px] text-text-3"
          >{episodes.length} episode{episodes.length === 1 ? '' : 's'} from TMDB</span
        >
      {/if}
    </div>
  {/if}

  <div class="space-y-2">
    {#each titles as t (t.id)}
      <div
        class="flex w-full min-h-[44px] items-center gap-3 rounded-xl border p-3 text-left"
        style="
          border-color: {selected[t.id] ? 'rgba(0,214,143,0.35)' : 'var(--border)'};
          background: {selected[t.id] ? 'rgba(0,214,143,0.04)' : 'transparent'};
        "
      >
        <label class="flex items-center gap-3">
          <input
            type="checkbox"
            bind:checked={selected[t.id]}
            class="h-5 w-5 cursor-pointer accent-accent"
          />
        </label>
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
        {#if isTV && selected[t.id] && episodes.length > 0}
          <label class="flex shrink-0 items-center gap-2 text-[11px] text-text-3">
            Episode
            <select
              bind:value={episodeMap[t.id]}
              class="rounded-md border border-border bg-surface-2 px-2 py-1 font-mono text-[12px] text-text"
            >
              {#each episodes as ep (ep.number)}
                <option value={ep.number}>
                  E{String(ep.number).padStart(2, '0')} — {ep.name}
                </option>
              {/each}
            </select>
          </label>
        {/if}
      </div>
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
