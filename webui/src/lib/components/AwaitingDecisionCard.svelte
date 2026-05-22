<script lang="ts">
  import { onDestroy } from 'svelte';
  import {
    profiles,
    startDisc,
    scanDisc,
    discs,
    jobs,
    manualIdentify,
    pendingDiscID,
    skipDisc,
    operationMode,
    bootCodeCounts,
    setDiscType,
  } from '$lib/store';
  import type { Disc, Candidate } from '$lib/wire';
  import DiscTypeBadge from './DiscTypeBadge.svelte';
  import DiscArt from './DiscArt.svelte';

  export let disc: Disc;

  const COUNTDOWN_SEC = 8;
  const AUTO_CONFIRM_MIN_CONFIDENCE = 50;

  let countdownSec = COUNTDOWN_SEC;
  let cancelled = false;
  // `starting` becomes true the instant a rip-start request is in
  // flight, whether from a manual click or the auto-confirm timer.
  // It disables both buttons + the timer's startDisc call so two
  // jobs can never enqueue for the same disc from this card.
  let starting = false;
  // Index of the currently radio-selected candidate. Defaults to the
  // top match. Row clicks update this without firing a rip; the Start
  // button reads from here.
  let selectedIndex = 0;

  $: liveDisc = $discs[disc.id] ?? disc;
  $: candidates = liveDisc.candidates ?? [];
  $: topConfidence = candidates[0]?.confidence ?? 0;
  $: selectedCandidate = candidates[selectedIndex] ?? candidates[0];
  $: isGameDisc = [
    'PSX',
    'PS2',
    'SAT',
    'DC',
    'XBOX',
    'SEGACD',
    '3DO',
    'PCFX',
    'JAGCD',
    'CDI',
    'PCECD',
    'NEOCD',
    'CD32',
    'FMTOWNS',
    'PIPPIN',
  ].includes(liveDisc.type);
  // BDMV/UHD/DVD are the only disc types whose handlers implement
  // TitleScanner. Hide the Pick titles… affordance for everything else
  // so audio CDs / game discs / DATA discs don't show a button that
  // would 422 from the server.
  $: titleScanSupported = ['BDMV', 'UHD', 'DVD'].includes(liveDisc.type);

  // Per-rip profile selector. When a disc type has more than one
  // enabled profile (e.g. DVD-Movie vs DVD-Movie + Extras), the user
  // gets a dropdown to pick which one this rip uses. Default tracks
  // today's auto-pick by name; user override is sticky for the
  // remainder of the card's lifetime via profileTouched.
  $: availableProfiles = $profiles.filter((p) => p.enabled && p.disc_type === liveDisc.type);
  $: defaultProfileID = pickerProfileID(liveDisc, $profiles, candidates[0]);
  let profileTouched = false;
  let chosenProfileID = '';
  $: if (!profileTouched && chosenProfileID !== defaultProfileID) {
    chosenProfileID = defaultProfileID;
  }
  // scanProfileID is what the "Pick titles…" button hands to /scan.
  // Mirrors the chosen profile when there's a non-empty selection so
  // the picker UI later inherits the same choice via the scan job row.
  $: scanProfileID = chosenProfileID || defaultProfileID;

  function pickerProfileID(d: typeof liveDisc, pros: typeof $profiles, top?: Candidate): string {
    const enabled = pros.filter((p) => p.enabled);
    if (d.type === 'DVD') {
      const wantName = top?.media_type === 'tv' ? 'DVD-Series' : 'DVD-Movie';
      return enabled.find((p) => p.disc_type === 'DVD' && p.name === wantName)?.id ?? '';
    }
    return enabled.find((p) => p.disc_type === d.type)?.id ?? '';
  }

  function onProfileChange(e: Event): void {
    const v = (e.target as HTMLSelectElement).value;
    chosenProfileID = v;
    profileTouched = true;
    // Any user interaction with the picker cancels the auto-rip
    // countdown — the user is making choices, don't race them.
    cancelled = true;
    clearInterval(timer);
  }

  function select(idx: number): void {
    if (starting) return;
    if (idx < 0 || idx >= candidates.length) return;
    selectedIndex = idx;
    // Row click is the other half of "the user is interacting" —
    // cancel the auto-rip countdown so the timer doesn't pre-empt
    // their Start button click.
    cancelled = true;
    clearInterval(timer);
  }
  // The manual-search backend dispatches on disc.type — MB for audio
  // CDs, IGDB for game discs, TMDB for video — so the placeholder +
  // button label must follow the same split or the user is told the
  // wrong story.
  $: searchPlaceholder =
    liveDisc.type === 'AUDIO_CD'
      ? 'Album or artist…'
      : isGameDisc
        ? 'Game title…'
        : 'Movie or show title…';
  $: searchButtonLabel =
    liveDisc.type === 'AUDIO_CD'
      ? 'Search MusicBrainz'
      : isGameDisc
        ? 'Search IGDB'
        : 'Search TMDB';
  // A disc whose previous rip ended in a terminal failure must NOT be
  // auto-ripped again: AwaitingDecisionList keeps the card visible for
  // these states (retry intent), but batch auto-confirm would otherwise
  // re-rip on its 8 s countdown — an unreadable/scratched disc then loops
  // forever (fail → card returns → auto-rip → fail → …). The same disc
  // can still be retried manually via the Start button. Mirrors the
  // terminal-failure set AwaitingDecisionList uses to keep the card.
  // A scan job is not a rip and never counts.
  $: priorFailedRip = $jobs.some(
    (j) =>
      j.disc_id === liveDisc.id &&
      (j.kind ?? 'rip') !== 'scan' &&
      (j.state === 'failed' || j.state === 'cancelled' || j.state === 'interrupted'),
  );

  // Auto-confirm fires in batch mode for two cases:
  //   1. A high-confidence candidate (Redump 100, BootCodeIndex 90, MB ≥50).
  //   2. A DATA disc — title is the ISO9660 volume label, which the user
  //      chose when burning. Skip if they want to bail.
  // …but never for a disc that already failed a rip (loop guard above).
  $: autoConfirmAllowed =
    $operationMode === 'batch' &&
    !priorFailedRip &&
    (topConfidence >= AUTO_CONFIRM_MIN_CONFIDENCE || liveDisc.type === 'DATA');

  function profileForCandidate(c: Candidate): string {
    const enabled = $profiles.filter((p) => p.enabled);
    if (liveDisc.type === 'AUDIO_CD') {
      return enabled.find((p) => p.disc_type === 'AUDIO_CD')?.id ?? '';
    }
    if (liveDisc.type === 'DVD') {
      const wantName = c.media_type === 'tv' ? 'DVD-Series' : 'DVD-Movie';
      return enabled.find((p) => p.disc_type === 'DVD' && p.name === wantName)?.id ?? '';
    }
    return enabled.find((p) => p.disc_type === liveDisc.type)?.id ?? '';
  }

  function dataProfileID(): string {
    return $profiles.find((p) => p.disc_type === 'DATA' && p.enabled)?.id ?? '';
  }

  // showsTitlePicker reports whether a profile opts into the pre-rip
  // title picker. Used by both the manual pick + auto-confirm paths to
  // route through scanDisc instead of startDisc.
  function showsTitlePicker(profileID: string): boolean {
    const p = $profiles.find((x) => x.id === profileID);
    const v = p?.options?.['show_title_picker'];
    return v === true;
  }

  async function ripAsData(): Promise<void> {
    if (starting) return;
    cancelled = true;
    clearInterval(timer);
    // DATA discs respect the user's per-rip pick when they touched
    // the dropdown (e.g. picked a custom DATA profile); otherwise
    // fall back to the first enabled DATA profile.
    const profileId = profileTouched && chosenProfileID ? chosenProfileID : dataProfileID();
    if (!profileId) return;
    starting = true;
    try {
      await startDisc(liveDisc.id, profileId);
    } catch (_e) {
      starting = false;
    }
  }

  const timer: ReturnType<typeof setInterval> = setInterval(() => {
    if (cancelled || !autoConfirmAllowed) return;
    countdownSec--;
    if (countdownSec <= 0) {
      clearInterval(timer);
      autoConfirm();
    }
  }, 1000);

  onDestroy(() => clearInterval(timer));

  async function pick(idx: number): Promise<void> {
    if (starting) return;
    cancelled = true;
    clearInterval(timer);
    const c = candidates[idx];
    if (!c) return;
    // User's per-rip dropdown pick wins over the auto-pick-by-name
    // (profileForCandidate). When untouched, chosenProfileID tracks
    // the default so movie/series resolution still works.
    const profileId = profileTouched && chosenProfileID ? chosenProfileID : profileForCandidate(c);
    if (!profileId) return;
    starting = true;
    try {
      // Profiles opted into the title picker route through scanDisc
      // instead of startDisc — the user wants the chance to confirm
      // titles before the rip kicks off.
      if (showsTitlePicker(profileId)) {
        await scanDisc(liveDisc.id, profileId);
      } else {
        await startDisc(liveDisc.id, profileId, idx);
      }
    } catch (_e) {
      // Daemon refuses duplicate starts with 409 since 0.4.1; if we
      // hit any error, release the lock so the user can retry from
      // the same card rather than being stuck behind a disabled button.
      starting = false;
    }
  }

  async function autoConfirm(): Promise<void> {
    if (starting || cancelled || !autoConfirmAllowed) return;
    cancelled = true;
    let profileId: string;
    let candidateIndex: number | undefined;
    if (liveDisc.type === 'DATA') {
      profileId = profileTouched && chosenProfileID ? chosenProfileID : dataProfileID();
      candidateIndex = undefined;
    } else {
      const c = candidates[selectedIndex] ?? candidates[0];
      if (!c) return;
      profileId = profileTouched && chosenProfileID ? chosenProfileID : profileForCandidate(c);
      candidateIndex = candidates[selectedIndex] ? selectedIndex : 0;
    }
    if (!profileId) return;
    starting = true;
    try {
      if (showsTitlePicker(profileId)) {
        await scanDisc(liveDisc.id, profileId);
      } else {
        await startDisc(liveDisc.id, profileId, candidateIndex);
      }
    } catch (_e) {
      starting = false;
    }
  }

  let scanning = false;
  let scanError: string | null = null;

  async function pickTitles(): Promise<void> {
    if (scanning || starting || !scanProfileID) return;
    cancelled = true;
    clearInterval(timer);
    scanning = true;
    scanError = null;
    try {
      // SubmitScan creates a kind='scan' job; on completion the
      // disc.changed SSE event arrives with metadata_json.scan
      // populated and AwaitingDecisionList swaps this card out for
      // the TitlePicker. No further action needed from this function.
      await scanDisc(liveDisc.id, scanProfileID);
    } catch (e) {
      scanError = (e as Error).message;
      scanning = false;
      cancelled = false;
    }
  }

  let skipping = false;

  async function skip(): Promise<void> {
    if (skipping) return;
    skipping = true;
    cancelled = true;
    clearInterval(timer);
    // Clearing pendingDiscID hides the legacy bottom-sheet path too.
    pendingDiscID.update((cur) => (cur === liveDisc.id ? null : cur));
    try {
      await skipDisc(liveDisc.id);
      // disc.deleted SSE removes the row from the store; the
      // AwaitingDecisionList rerender drops the card.
    } catch (_err) {
      // Server-side delete failed (e.g. the disc has a job after all).
      // Leave the card visible so the user can retry — no UX-corrupt
      // optimistic delete.
      skipping = false;
    }
  }

  let searching = false;
  let searchQuery = '';
  let searchPending = false;
  let searchError: string | null = null;
  let searchedNoResults = false;

  function openSearch(): void {
    searching = true;
    searchQuery = '';
    searchError = null;
    searchedNoResults = false;
    cancelled = true;
    clearInterval(timer);
  }

  function cancelSearch(): void {
    searching = false;
    searchQuery = '';
    searchError = null;
    searchedNoResults = false;
  }

  async function submitSearch(): Promise<void> {
    const q = searchQuery.trim();
    if (!q) return;
    cancelled = true;
    clearInterval(timer);
    searchPending = true;
    searchError = null;
    searchedNoResults = false;
    try {
      const cands = await manualIdentify(liveDisc.id, q);
      if (cands.length === 0) {
        searchedNoResults = true;
      } else {
        searching = false;
      }
    } catch (e) {
      searchError = (e as Error).message;
    } finally {
      searchPending = false;
    }
  }

  // ----- Disc-type override (misclassification safety net) --------------------
  // Bidirectional: any awaiting-decision disc can be switched to any other valid
  // target. The backend (validOverrideTarget) accepts any type with a registered
  // pipeline handler, rejecting only the same-as-current type and AUDIO_CD.

  const OVERRIDE_TYPES: { value: string; label: string }[] = [
    { value: 'DATA', label: 'Data disc' },
    { value: 'PSX', label: 'PlayStation' },
    { value: 'PS2', label: 'PlayStation 2' },
    { value: 'SAT', label: 'Sega Saturn' },
    { value: 'DC', label: 'Dreamcast' },
    { value: 'XBOX', label: 'Xbox' },
    { value: 'SEGACD', label: 'Sega CD' },
    { value: '3DO', label: '3DO' },
    { value: 'PCFX', label: 'PC-FX' },
    { value: 'JAGCD', label: 'Atari Jaguar CD' },
    { value: 'CDI', label: 'Philips CD-i' },
    { value: 'PCECD', label: 'PC Engine CD' },
    { value: 'NEOCD', label: 'Neo Geo CD' },
    { value: 'CD32', label: 'Amiga CD32' },
    { value: 'FMTOWNS', label: 'FM Towns' },
    { value: 'PIPPIN', label: 'Bandai Pippin' },
    { value: 'DVD', label: 'DVD-Video' },
    { value: 'BDMV', label: 'Blu-ray' },
    { value: 'UHD', label: 'UHD Blu-ray' },
    { value: 'VCD', label: 'Video CD' },
  ];
  // Never offer a self-set (the backend 422s it) — drop the disc's current type.
  $: overrideOptions = OVERRIDE_TYPES.filter((t) => t.value !== liveDisc.type);
  // Controlled value: binding re-asserts the selected option on every reactive
  // update, so filtering an option out of the (non-keyed) list can't leave the
  // selection pointing at a relabeled neighbour. Reset to '' after each pick.
  let overrideValue = '';
  let overrideError = '';
  let overrideBusy = false;

  async function onOverrideType(): Promise<void> {
    const value = overrideValue;
    if (!value) return;
    overrideBusy = true;
    overrideError = '';
    try {
      await setDiscType(liveDisc.id, value);
    } catch (err) {
      overrideError = err instanceof Error ? err.message : 'failed to set type';
    } finally {
      overrideBusy = false;
      overrideValue = '';
    }
  }
</script>

<div
  class="rounded-2xl border p-5"
  style="border-color: rgba(var(--accent-rgb),0.35); background: var(--surface-1)"
  data-disc-id={liveDisc.id}
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
          Awaiting decision
        </span>
      </div>
      <div class="mt-1 truncate text-[15px] font-semibold text-text">
        {candidates.length} match{candidates.length === 1 ? '' : 'es'}
      </div>
      {#if !searching}
        {#if autoConfirmAllowed && !cancelled}
          <div class="mt-1 font-mono text-[11px] text-text-3">
            {`Auto-rip in ${countdownSec}s`}
          </div>
        {:else if autoConfirmAllowed && cancelled && candidates.length > 0}
          <div class="mt-1 text-[11px] text-text-3">Pick a title to rip</div>
        {:else if priorFailedRip && candidates.length > 0}
          <div class="mt-1 text-[11px] text-warn">Previous rip failed · pick a title to retry</div>
        {:else if $operationMode === 'manual' && candidates.length > 0}
          <div class="mt-1 text-[11px] text-text-3">Manual mode · pick a title to rip</div>
        {:else if candidates.length > 0}
          <div class="mt-1 text-[11px] text-warn">No confident match · pick a title or search</div>
        {:else if liveDisc.type === 'AUDIO_CD'}
          <div class="mt-1 text-[11px] text-warn">
            No MusicBrainz match · eject and retry, or skip
          </div>
        {:else if liveDisc.type === 'DATA'}
          <div class="mt-1 text-[11px] text-text-3">
            {liveDisc.title ?? 'Data disc'} · rip as-is or skip
          </div>
        {:else}
          <div class="mt-1 text-[11px] text-warn">No match found · search manually</div>
        {/if}
        {#if isGameDisc && candidates.length === 0 && ($bootCodeCounts[liveDisc.type] ?? 0) === 0}
          <div class="mt-2 text-[11px] text-text-3">
            Tip: add a Redump dat-file for {liveDisc.type} to
            <code class="rounded bg-surface-2 px-1"
              >/var/lib/discecho/redump/{liveDisc.type.toLowerCase()}/</code
            >
            to enable auto-match.
          </div>
        {/if}
      {/if}
    </div>
  </div>

  <div class="mt-2 text-sm">
    <label class="text-zinc-400" for="disc-type-override-{liveDisc.id}">Wrong type?</label>
    <select
      id="disc-type-override-{liveDisc.id}"
      data-testid="disc-type-override"
      class="ml-2 rounded bg-zinc-800 px-2 py-1 text-text"
      disabled={overrideBusy}
      bind:value={overrideValue}
      on:change={onOverrideType}
    >
      <option value="">Set type…</option>
      {#each overrideOptions as t}
        <option value={t.value}>{t.label}</option>
      {/each}
    </select>
    {#if overrideError}<p class="mt-1 text-rose-400">{overrideError}</p>{/if}
  </div>

  {#if searching}
    <div class="space-y-3">
      <input
        type="text"
        bind:value={searchQuery}
        placeholder={searchPlaceholder}
        class="min-h-[44px] w-full rounded-xl border border-border bg-surface-2 px-3 text-[15px] text-text"
        on:keydown={(e) => e.key === 'Enter' && submitSearch()}
      />
      {#if searchError}
        <div class="text-[12px] text-error">{searchError}</div>
      {/if}
      {#if searchedNoResults}
        <div class="text-[12px] text-warn">No matches found — try a different title.</div>
      {/if}
      <div class="flex flex-col gap-2 sm:flex-row">
        <button
          class="min-h-[40px] flex-1 rounded-xl bg-accent text-[14px] font-semibold text-black disabled:opacity-50"
          on:click={submitSearch}
          disabled={searchPending || !searchQuery.trim()}
        >
          {searchPending ? 'Searching…' : searchButtonLabel}
        </button>
        <button class="min-h-[40px] text-[13px] text-text-3" on:click={cancelSearch}>
          Cancel search
        </button>
      </div>
    </div>
  {:else}
    <div class="space-y-2">
      {#each candidates as c, i}
        <button
          class="flex w-full min-h-[44px] items-center gap-3 rounded-xl border p-3 text-left"
          style="
            border-color: {i === selectedIndex ? 'rgba(0,214,143,0.35)' : 'var(--border)'};
            background: {i === selectedIndex ? 'rgba(0,214,143,0.04)' : 'transparent'};
          "
          on:click={() => select(i)}
          disabled={starting}
          aria-pressed={i === selectedIndex}
          data-testid={`candidate-row-${i}`}
        >
          <span
            class="relative flex h-5 w-5 items-center justify-center rounded-full border"
            style="border-color: {i === selectedIndex ? 'var(--accent)' : '#3f3f46'}"
          >
            {#if i === selectedIndex}<span class="h-2.5 w-2.5 rounded-full bg-accent"></span>{/if}
          </span>
          <div class="min-w-0 flex-1">
            <div class="flex items-center gap-2">
              {#if c.media_type === 'movie'}
                <span
                  class="rounded px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-[0.14em]"
                  style="background: rgba(94,163,255,0.15); color: var(--info)"
                >
                  FILM
                </span>
              {:else if c.media_type === 'tv'}
                <span
                  class="rounded px-1.5 py-0.5 text-[9px] font-bold uppercase tracking-[0.14em]"
                  style="background: rgba(94,163,255,0.15); color: var(--info)"
                >
                  TV
                </span>
              {/if}
              <span class="truncate text-[14px] font-medium text-text">{c.title}</span>
            </div>
            <div class="mt-0.5 font-mono text-[11px] text-text-3">
              {c.source}{c.year ? ` · ${c.year}` : ''}{c.artist ? ` · ${c.artist}` : ''}
            </div>
          </div>
          <span
            class="font-mono text-[14px] font-semibold"
            style="color: {c.confidence > 90 ? 'var(--accent)' : 'var(--warn)'}"
          >
            {c.confidence}%
          </span>
        </button>
      {/each}
    </div>

    {#if availableProfiles.length > 1}
      <div class="mt-4 flex items-center gap-2">
        <label
          class="text-[11px] uppercase tracking-[0.14em] text-text-3"
          for={`profile-${liveDisc.id}`}
        >
          Profile
        </label>
        <select
          id={`profile-${liveDisc.id}`}
          class="min-h-[36px] flex-1 rounded-xl border border-border bg-surface-2 px-2 text-[13px] text-text"
          value={chosenProfileID}
          on:change={onProfileChange}
          data-testid="profile-select"
        >
          {#each availableProfiles as p (p.id)}
            <option value={p.id}>{p.name}</option>
          {/each}
        </select>
      </div>
    {/if}

    <div class="mt-5 flex flex-col gap-2 sm:flex-row">
      {#if liveDisc.type === 'DATA'}
        <button
          class="min-h-[44px] flex-1 rounded-xl bg-accent text-[14px] font-semibold text-black disabled:opacity-50"
          on:click={ripAsData}
          disabled={starting}
        >
          Rip as data
        </button>
      {:else}
        {#if candidates.length > 0}
          <button
            class="min-h-[44px] flex-1 rounded-xl bg-accent text-[14px] font-semibold text-black disabled:opacity-50"
            on:click={() => pick(selectedIndex)}
            disabled={starting || scanning}
            data-testid="start-rip"
          >
            Start rip{selectedCandidate ? ` · ${selectedCandidate.title}` : ''}
          </button>
        {/if}
        {#if titleScanSupported && scanProfileID}
          <button
            class="min-h-[44px] flex-1 rounded-xl border border-border text-[14px] text-text-2 disabled:opacity-50"
            on:click={pickTitles}
            disabled={starting || scanning}
            data-testid="pick-titles"
          >
            {scanning ? 'Scanning titles…' : 'Pick titles…'}
          </button>
        {/if}
        <button
          class="min-h-[44px] flex-1 rounded-xl border border-border text-[14px] text-text-2"
          on:click={openSearch}
        >
          Search manually
        </button>
      {/if}
      <button
        class="min-h-[44px] rounded-xl border border-border px-4 text-[14px] text-text-2 disabled:opacity-50 sm:w-auto"
        on:click={skip}
        disabled={skipping}
      >
        Skip
      </button>
    </div>
    {#if scanError}
      <div class="mt-2 text-[12px] text-error">{scanError}</div>
    {/if}
  {/if}
</div>
