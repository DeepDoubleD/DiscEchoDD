<script lang="ts">
  import { onMount, onDestroy } from 'svelte';
  import {
    drives,
    bootCodeCounts,
    integrations as integrationsStore,
    fetchIntegration,
  } from '$lib/store';
  import { apiGet, apiPut, apiPatch, apiPost } from '$lib/api';
  import { pushToast } from '$lib/toasts';
  import type { Drive } from '$lib/wire';
  import FormSection from './FormSection.svelte';
  import FormRow from './FormRow.svelte';
  import PathField from './PathField.svelte';
  import ApiRow from './ApiRow.svelte';
  import IntegrationEditor, { type FieldSpec } from './IntegrationEditor.svelte';

  type VersionInfo = { version?: string; commit?: string; build_date?: string };

  type HostInfo = {
    hostname: string;
    kernel: string;
    cpu_count: number;
    uptime_seconds: number;
  };

  type LibrarySize = {
    media: string;
    label: string;
    path: string;
    bytes: number;
    exists: boolean;
  };

  type LibrariesInfo = {
    libraries: LibrarySize[];
    measured_at: string;
    measuring: boolean;
    array: { used_bytes: number; total_bytes: number; available_bytes: number };
  };

  type SubItem = {
    label: string;
    status: string;
    detail?: string;
  };

  type IntegrationStatus = {
    name: string;
    hint?: string;
    status: string;
    detail?: string;
    editable?: string;
    sub_items?: SubItem[];
  };

  type IntegrationsInfo = {
    tmdb: { configured: boolean; language: string };
    musicbrainz: { base_url: string; user_agent: string };
    apprise: { bin: string; version?: string };
    library_roots?: Record<string, string>;
    items?: IntegrationStatus[];
    boot_code_counts?: Record<string, number>;
  };

  type MediaRoot = 'movies' | 'tv' | 'music' | 'games' | 'data';
  type LibraryRoots = Record<MediaRoot, string>;

  const INTEGRATION_FIELDS: Record<'igdb' | 'tmdb' | 'makemkv', FieldSpec[]> = {
    igdb: [
      { name: 'client_id', label: 'Client ID', secret: false },
      { name: 'client_secret', label: 'Client secret', secret: true },
    ],
    tmdb: [
      { name: 'key', label: 'API key (v3)', secret: true },
      { name: 'lang', label: 'Language (e.g. en-US)', secret: false },
    ],
    makemkv: [{ name: 'beta_key', label: 'License key (T- beta or M- purchased)', secret: true }],
  };

  const INTEGRATION_ENV_HINTS: Record<'igdb' | 'tmdb' | 'makemkv', string> = {
    igdb: 'DISCECHO_IGDB_CLIENT_ID / DISCECHO_IGDB_CLIENT_SECRET',
    tmdb: 'DISCECHO_TMDB_KEY',
    makemkv: 'DISCECHO_MAKEMKV_BETA_KEY',
  };

  const INTEGRATION_TESTABLE: Record<'igdb' | 'tmdb' | 'makemkv', boolean> = {
    igdb: true,
    tmdb: true,
    makemkv: false,
  };

  function asIntegrationName(n: string): 'igdb' | 'tmdb' | 'makemkv' {
    return n as 'igdb' | 'tmdb' | 'makemkv';
  }

  // Used in template to unwrap the non-null IntegrationDetail once we've
  // confirmed it is defined in the {#if} guard above.
  function getIntegration(n: string): import('$lib/wire').IntegrationDetail {
    return $integrationsStore[asIntegrationName(n)] as import('$lib/wire').IntegrationDetail;
  }

  const ROOT_LABELS: Record<MediaRoot, string> = {
    movies: 'Movies',
    tv: 'TV',
    music: 'Music',
    games: 'Games',
    data: 'Data archive',
  };
  const ROOT_ORDER: MediaRoot[] = ['movies', 'tv', 'music', 'games', 'data'];

  let version: VersionInfo | null = null;
  let host: HostInfo | null = null;
  let integrations: IntegrationsInfo | null = null;
  let libraries: LibrariesInfo | null = null;
  let recalcing = false;

  // Encoding section state. The cap is stored as bytes server-side but
  // displayed in GiB for usability. concurrentEncodes maps directly to
  // the int setting compute.concurrent_encodes — changing it takes
  // effect on the next daemon restart (worker-pool size is fixed at
  // boot; live resize is a future enhancement).
  type SpoolInfo = { usage_bytes: number; cap_bytes: number; blocked: boolean };
  let spool: SpoolInfo | null = null;
  let concurrentEncodes = 1;
  let spoolCapGiB = 100;
  let encodingDirty = false;
  let savingEncoding = false;
  let encodingError: string | null = null;
  let spoolPoll: ReturnType<typeof setInterval> | null = null;

  let roots: LibraryRoots = { movies: '', tv: '', music: '', games: '', data: '' };
  let dirty: Partial<LibraryRoots> = {};
  let savingRoots = false;
  let rootsError: string | null = null;

  $: hasDirty = Object.keys(dirty).length > 0;

  onMount(async () => {
    const [v, h, i, s, lib, settings] = await Promise.allSettled([
      apiGet<VersionInfo>('/api/version'),
      apiGet<HostInfo>('/api/system/host'),
      apiGet<IntegrationsInfo>('/api/system/integrations'),
      apiGet<SpoolInfo>('/api/system/spool'),
      apiGet<LibrariesInfo>('/api/system/libraries'),
      apiGet<Record<string, string>>('/api/settings'),
    ]);
    version = v.status === 'fulfilled' ? v.value : null;
    host = h.status === 'fulfilled' ? h.value : null;
    integrations = i.status === 'fulfilled' ? i.value : null;
    spool = s.status === 'fulfilled' ? s.value : null;
    libraries = lib.status === 'fulfilled' ? lib.value : null;
    if (settings.status === 'fulfilled') {
      const c = parseInt(settings.value['compute.concurrent_encodes'] ?? '1', 10);
      if (Number.isInteger(c) && c > 0) concurrentEncodes = c;
      const capBytes = parseInt(settings.value['spool.cap_bytes'] ?? '', 10);
      if (Number.isFinite(capBytes) && capBytes > 0) {
        spoolCapGiB = Math.max(1, Math.round(capBytes / (1024 * 1024 * 1024)));
      }
    }
    if (integrations?.library_roots) {
      for (const m of ROOT_ORDER) {
        roots[m] = integrations.library_roots[m] ?? '';
      }
    }
    if (integrations?.boot_code_counts) {
      bootCodeCounts.set(integrations.boot_code_counts);
    }
    await Promise.allSettled([
      fetchIntegration('igdb'),
      fetchIntegration('tmdb'),
      fetchIntegration('makemkv'),
    ]);
    // Poll spool usage every 10s so the user sees compress jobs
    // drain in real time. 5s server-side cache keeps this cheap.
    spoolPoll = setInterval(async () => {
      try {
        spool = await apiGet<SpoolInfo>('/api/system/spool');
      } catch {
        // Best-effort; transient errors leave the last-known value.
      }
    }, 10_000);
  });

  onDestroy(() => {
    if (spoolPoll) clearInterval(spoolPoll);
  });

  function onEncodingChange(): void {
    encodingDirty = true;
    encodingError = null;
  }

  async function saveEncoding(): Promise<void> {
    if (!Number.isInteger(concurrentEncodes) || concurrentEncodes < 1 || concurrentEncodes > 8) {
      encodingError = 'Concurrent encodes must be a whole number 1-8.';
      return;
    }
    if (!Number.isInteger(spoolCapGiB) || spoolCapGiB < 1 || spoolCapGiB > 10_000) {
      encodingError = 'Spool cap must be a whole number 1-10000 GiB.';
      return;
    }
    savingEncoding = true;
    encodingError = null;
    try {
      await apiPut('/api/settings', {
        'compute.concurrent_encodes': String(concurrentEncodes),
        'spool.cap_bytes': String(spoolCapGiB * 1024 * 1024 * 1024),
      });
      encodingDirty = false;
      pushToast('success', 'Encoding settings saved.');
    } catch (e) {
      encodingError = (e as Error).message;
    } finally {
      savingEncoding = false;
    }
  }

  function onRootChange(media: MediaRoot, e: CustomEvent<string>): void {
    dirty = { ...dirty, [media]: e.detail };
  }

  async function saveRoots(): Promise<void> {
    rootsError = null;
    savingRoots = true;
    try {
      const body: Record<string, string> = {};
      for (const [media, path] of Object.entries(dirty)) {
        body[`library.${media}`] = path as string;
      }
      await apiPut('/api/settings', body);
      roots = { ...roots, ...dirty };
      dirty = {};
      pushToast('success', 'Library paths saved');
    } catch (e) {
      rootsError = (e as Error).message;
    } finally {
      savingRoots = false;
    }
  }

  function discardRoots(): void {
    dirty = {};
    rootsError = null;
  }

  function onIntegrationEdit(item: IntegrationStatus): void {
    if (item.name === 'Apprise') {
      // Scroll the user to the notifications section, which is where
      // Apprise URLs are managed.
      const target = document.querySelector('[data-section="notifications"]');
      if (target) target.scrollIntoView({ behavior: 'smooth', block: 'start' });
      return;
    }
    if (item.editable) {
      window.alert(
        `Configure ${item.name} by setting ${item.editable} in .env, then restart the container.`,
      );
    }
  }

  function formatBytes(n: number): string {
    if (!Number.isFinite(n) || n <= 0) return '0 B';
    const units = ['B', 'KB', 'MB', 'GB', 'TB', 'PB'];
    let u = 0;
    let v = n;
    while (v >= 1024 && u < units.length - 1) {
      v /= 1024;
      u += 1;
    }
    return `${v.toFixed(v < 10 ? 1 : 0)} ${units[u]}`;
  }

  function formatUptime(s: number): string {
    if (!Number.isFinite(s) || s <= 0) return '—';
    const d = Math.floor(s / 86400);
    const h = Math.floor((s % 86400) / 3600);
    const m = Math.floor((s % 3600) / 60);
    if (d > 0) return `${d}d ${h}h`;
    if (h > 0) return `${h}h ${m}m`;
    return `${m}m`;
  }

  function arrayPercent(a: LibrariesInfo['array']): number {
    if (!a.total_bytes) return 0;
    return Math.min(100, Math.round((a.used_bytes / a.total_bytes) * 100));
  }

  // "12m ago" / "3h ago" / "2d ago" from an RFC3339 timestamp. Empty
  // string (never measured) renders as a dash by the caller.
  function relativeTime(iso: string): string {
    const t = Date.parse(iso);
    if (!Number.isFinite(t)) return '—';
    const s = Math.max(0, Math.floor((Date.now() - t) / 1000));
    if (s < 60) return 'just now';
    const m = Math.floor(s / 60);
    if (m < 60) return `${m}m ago`;
    const h = Math.floor(m / 60);
    if (h < 24) return `${h}h ago`;
    return `${Math.floor(h / 24)}d ago`;
  }

  async function recalcLibraries() {
    if (recalcing) return;
    recalcing = true;
    try {
      libraries = await apiPost<LibrariesInfo>('/api/system/libraries/recalc');
    } catch {
      pushToast('error', 'Failed to start recalculation');
    } finally {
      recalcing = false;
    }
  }

  // Per-drive offset editor state. Keyed by drive ID so multiple drives
  // can be edited independently. The draft uses `number | null` because
  // `<input type="number" bind:value>` writes a JavaScript number (or
  // null when the field is empty), not a string. Saving guards against
  // concurrent PATCH bursts.
  let offsetEditing: Record<string, boolean> = {};
  let offsetDraft: Record<string, number | null> = {};
  let offsetSaving: Record<string, boolean> = {};
  let offsetError: Record<string, string> = {};

  function offsetSourceLabel(d: Drive): string {
    if (d.read_offset_source === 'manual') return 'manual';
    if (d.read_offset_source === 'auto') return 'auto';
    return 'uncalibrated';
  }

  function beginOffsetEdit(d: Drive): void {
    offsetEditing = { ...offsetEditing, [d.id]: true };
    offsetDraft = { ...offsetDraft, [d.id]: d.read_offset ?? 0 };
    offsetError = { ...offsetError, [d.id]: '' };
  }

  function cancelOffsetEdit(d: Drive): void {
    offsetEditing = { ...offsetEditing, [d.id]: false };
    offsetError = { ...offsetError, [d.id]: '' };
  }

  async function saveOffset(d: Drive): Promise<void> {
    const draft = offsetDraft[d.id];
    if (draft === null || draft === undefined || Number.isNaN(draft)) {
      offsetError = { ...offsetError, [d.id]: 'Offset must be a whole number.' };
      return;
    }
    if (!Number.isInteger(draft)) {
      offsetError = { ...offsetError, [d.id]: 'Offset must be a whole number.' };
      return;
    }
    if (draft < -3000 || draft > 3000) {
      offsetError = { ...offsetError, [d.id]: 'Offset must be within ±3000 samples.' };
      return;
    }
    const parsed = draft;
    offsetSaving = { ...offsetSaving, [d.id]: true };
    offsetError = { ...offsetError, [d.id]: '' };
    try {
      await apiPatch(`/api/drives/${encodeURIComponent(d.id)}/offset`, {
        read_offset: parsed,
      });
      drives.update((list) =>
        list.map((row) =>
          row.id === d.id ? { ...row, read_offset: parsed, read_offset_source: 'manual' } : row,
        ),
      );
      offsetEditing = { ...offsetEditing, [d.id]: false };
      pushToast('success', `Saved read-offset ${parsed} for ${d.model || d.dev_path}`);
    } catch (e) {
      offsetError = { ...offsetError, [d.id]: (e as Error).message };
    } finally {
      offsetSaving = { ...offsetSaving, [d.id]: false };
    }
  }
</script>

<div class="space-y-7">
  <FormSection title="Drives" sub="Connected optical drives. DiscEcho watches /dev/sr* by default.">
    {#if $drives.length === 0}
      <div class="px-4 py-3 text-[12px] text-text-3">No drives detected.</div>
    {:else}
      {#each $drives as d (d.id)}
        <div class="px-4 py-3" data-testid="drive-row" data-drive-id={d.id}>
          <div class="grid items-center gap-4" style="grid-template-columns: 1fr auto auto">
            <div>
              <div class="text-[13px] font-medium text-text">{d.model || '—'}</div>
              <div class="font-mono text-[11px] text-text-3">
                {d.dev_path}
              </div>
            </div>
            <span
              class="inline-flex items-center rounded-md border border-border bg-surface-2
                     px-2 py-[3px] text-[11px] font-medium uppercase tracking-wide text-text-2"
            >
              {d.state}
            </span>
          </div>

          <!--
            AccurateRip read-offset row. Inline edit affordance — number
            entry only; auto-detect against a calibration disc is a
            follow-up (needs whipper offset find stdout parsing
            captured against the homelab drive first). Disabled while
            drive is busy: changing offset mid-rip would silently mean
            the in-flight checksums don't reflect the configured offset.
          -->
          <div
            class="mt-2 flex items-center gap-3 text-[11px] text-text-3"
            data-testid="drive-offset-row"
          >
            <span class="font-mono">offset:</span>
            {#if offsetEditing[d.id]}
              <input
                type="number"
                bind:value={offsetDraft[d.id]}
                min={-3000}
                max={3000}
                step="1"
                class="w-24 rounded border border-border bg-surface-2 px-2 py-1 font-mono text-text"
                data-testid="drive-offset-input"
              />
              <button
                type="button"
                on:click={() => saveOffset(d)}
                disabled={offsetSaving[d.id]}
                class="rounded-md bg-accent px-2 py-1 text-[11px] font-semibold text-black disabled:opacity-50"
                data-testid="drive-offset-save"
              >
                Save
              </button>
              <button
                type="button"
                on:click={() => cancelOffsetEdit(d)}
                disabled={offsetSaving[d.id]}
                class="rounded-md border border-border px-2 py-1 text-[11px] text-text-2 disabled:opacity-50"
              >
                Cancel
              </button>
              {#if offsetError[d.id]}
                <span class="text-error" data-testid="drive-offset-error">{offsetError[d.id]}</span>
              {/if}
            {:else}
              <span class="font-mono text-text" data-testid="drive-offset-value">
                {d.read_offset ?? 0}
              </span>
              <span
                class="inline-flex items-center rounded-md border border-border bg-surface-2 px-2 py-[2px] text-[10px] uppercase tracking-wide"
                class:text-text-3={offsetSourceLabel(d) === 'uncalibrated'}
                data-testid="drive-offset-source"
              >
                {offsetSourceLabel(d)}
              </span>
              <button
                type="button"
                on:click={() => beginOffsetEdit(d)}
                disabled={d.state !== 'idle'}
                title={d.state !== 'idle'
                  ? 'Drive is busy — wait for it to return to idle before changing the offset.'
                  : 'Set the per-drive read-offset to enable AccurateRip verification.'}
                class="rounded-md border border-border px-2 py-1 text-[11px] text-text-2 disabled:opacity-50"
                data-testid="drive-offset-edit"
              >
                Edit
              </button>
            {/if}
          </div>
        </div>
      {/each}
    {/if}
  </FormSection>

  <FormSection title="Library paths" sub="Where ripped media lands.">
    {#each ROOT_ORDER as m (m)}
      <FormRow label={ROOT_LABELS[m]}>
        <PathField value={dirty[m] ?? roots[m]} on:change={(e) => onRootChange(m, e)} />
      </FormRow>
    {/each}
    {#if hasDirty}
      <div class="flex items-center justify-between gap-3 px-4 py-3">
        <div class="text-[11px] text-text-3">
          {#if rootsError}
            <span class="text-error">{rootsError}</span>
          {:else}
            Unsaved changes. Container restart required for in-flight rips to pick up new paths.
          {/if}
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            on:click={discardRoots}
            disabled={savingRoots}
            class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2 disabled:opacity-50"
          >
            Discard
          </button>
          <button
            type="button"
            on:click={saveRoots}
            disabled={savingRoots}
            class="rounded-md bg-accent px-3 py-1.5 text-[12px] font-semibold text-black disabled:opacity-50"
          >
            Save changes
          </button>
        </div>
      </div>
    {/if}
  </FormSection>

  <FormSection
    title="Encoding"
    sub="Compute queue concurrency and spool soft cap. Spool stores ripped output between the drive-bound rip and the compute-bound transcode."
  >
    <FormRow label="Concurrent encodes">
      <div class="flex items-center gap-3">
        <input
          type="number"
          min="1"
          max="8"
          step="1"
          bind:value={concurrentEncodes}
          on:input={onEncodingChange}
          class="w-24 rounded-md border border-border bg-surface-1 px-2 py-1.5 text-[13px]"
        />
        <span class="text-[11px] text-text-3">
          Transcodes that may run in parallel. Takes effect immediately.
        </span>
      </div>
    </FormRow>

    <FormRow label="Spool cap (GiB)">
      <div class="flex items-center gap-3">
        <input
          type="number"
          min="1"
          max="10000"
          step="1"
          bind:value={spoolCapGiB}
          on:input={onEncodingChange}
          class="w-24 rounded-md border border-border bg-surface-1 px-2 py-1.5 text-[13px]"
        />
        <span class="text-[11px] text-text-3">
          New rips pause when the spool dir reaches this size.
        </span>
      </div>
    </FormRow>

    <FormRow label="Spool usage">
      {#if spool}
        <div class="flex items-center gap-3 text-[12px] text-text-2">
          <span class="font-mono">
            {formatBytes(spool.usage_bytes)} / {formatBytes(spool.cap_bytes)}
          </span>
          {#if spool.blocked}
            <span
              class="rounded-md border border-error/30 bg-error/10 px-2 py-0.5 text-[11px] text-error"
            >
              CAP REACHED — pausing new rips
            </span>
          {/if}
        </div>
      {:else}
        <div class="text-[12px] text-text-3">—</div>
      {/if}
    </FormRow>

    {#if encodingDirty}
      <div class="flex items-center justify-between gap-3 px-4 py-3">
        <div class="text-[11px] text-text-3">
          {#if encodingError}
            <span class="text-error">{encodingError}</span>
          {:else}
            Unsaved changes.
          {/if}
        </div>
        <button
          type="button"
          on:click={saveEncoding}
          disabled={savingEncoding}
          class="rounded-md bg-accent px-3 py-1.5 text-[12px] font-semibold text-black disabled:opacity-50"
        >
          Save changes
        </button>
      </div>
    {/if}
  </FormSection>

  <FormSection title="API keys & connections">
    {#each ['igdb', 'tmdb', 'makemkv'] as name (name)}
      {#if $integrationsStore[asIntegrationName(name)]}
        <IntegrationEditor
          detail={getIntegration(name)}
          fields={INTEGRATION_FIELDS[asIntegrationName(name)]}
          envHint={INTEGRATION_ENV_HINTS[asIntegrationName(name)]}
          testable={INTEGRATION_TESTABLE[asIntegrationName(name)]}
        />
      {/if}
    {/each}

    {#if integrations?.items && integrations.items.length > 0}
      {#each integrations.items as item (item.name)}
        <ApiRow
          name={item.name}
          hint={item.hint ?? ''}
          status={item.status}
          detail={item.detail ?? ''}
          editable={item.editable ?? ''}
          on:edit={() => onIntegrationEdit(item)}
        />
      {/each}
    {/if}
  </FormSection>

  <FormSection title="Host">
    {#if host}
      <div class="px-4 py-3 grid gap-2 text-[12px]" style="grid-template-columns: 120px 1fr">
        <div class="text-text-2">Hostname</div>
        <div class="font-mono text-text">{host.hostname || '—'}</div>
        <div class="text-text-2">Kernel</div>
        <div class="font-mono text-text">{host.kernel || '—'}</div>
        <div class="text-text-2">CPUs</div>
        <div class="text-text">{host.cpu_count}</div>
        <div class="text-text-2">Uptime</div>
        <div class="text-text">{formatUptime(host.uptime_seconds)}</div>
        <div class="text-text-2">Build</div>
        <div class="font-mono text-text">{version?.version ?? '—'}</div>
      </div>
    {:else}
      <div class="px-4 py-3 text-[12px] text-text-3">Loading…</div>
    {/if}
  </FormSection>

  <FormSection title="Libraries" sub="On-disk size per library root.">
    {#if libraries}
      <div class="px-4 py-3 space-y-2" data-testid="libraries-panel">
        <div class="flex items-center justify-between text-[11px] text-text-3">
          <span>
            {#if libraries.measuring}
              measuring…
            {:else if libraries.measured_at}
              last measured {relativeTime(libraries.measured_at)}
            {:else}
              not yet measured
            {/if}
          </span>
          <button
            type="button"
            class="rounded-md px-2 py-0.5 text-[11px] text-text-2 hover:bg-surface-2 disabled:opacity-50"
            data-testid="library-recalc"
            disabled={recalcing || libraries.measuring}
            on:click={recalcLibraries}
          >
            ⟳ Recalculate
          </button>
        </div>
        {#each libraries.libraries as lib (lib.media)}
          <div
            class="flex items-baseline justify-between gap-3 text-[12px]"
            data-testid="library-row-{lib.media}"
          >
            <span class="w-16 shrink-0 text-text">{lib.label}</span>
            <span class="flex-1 truncate font-mono text-[11px] text-text-3">{lib.path || '—'}</span>
            <span class="shrink-0 tabular-nums text-text">
              {lib.exists ? formatBytes(lib.bytes) : '—'}
            </span>
          </div>
        {/each}
        {#if libraries.array.total_bytes > 0}
          <div class="border-t border-border pt-2" data-testid="library-array">
            <div class="flex justify-between text-[11px] text-text-3">
              <span>Array</span>
              <span>
                {formatBytes(libraries.array.used_bytes)} / {formatBytes(
                  libraries.array.total_bytes,
                )}
                · {formatBytes(libraries.array.available_bytes)} free
              </span>
            </div>
            <div class="mt-1 h-1.5 overflow-hidden rounded-full bg-surface-2">
              <div class="h-full bg-accent" style="width: {arrayPercent(libraries.array)}%"></div>
            </div>
          </div>
        {/if}
      </div>
    {:else}
      <div class="px-4 py-3 text-[12px] text-text-3">Loading…</div>
    {/if}
  </FormSection>
</div>
