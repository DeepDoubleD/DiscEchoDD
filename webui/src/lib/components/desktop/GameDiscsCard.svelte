<script lang="ts" context="module">
  export type GameDiscSystem = {
    system: string;
    label: string;
    subdir: string; // Redump folder name under dat_dir, e.g. "psx", "dc"
    boot_code: string; // ok | missing | na
    boot_code_count: number;
    redump_dat: string; // loaded | missing
  };
  export type GameDiscsInfo = {
    redumper_status: string;
    redumper_bin: string;
    dat_dir: string;
    status: string; // connected | partial | error: …
    systems: GameDiscSystem[];
  };
</script>

<script lang="ts">
  export let info: GameDiscsInfo;
  export let igdbConfigured = false;

  // tone mirrors ApiRow: connected→accent (green), error→error (red),
  // anything else (partial)→warn (amber) so missing dats don't look fine.
  $: tone =
    info.status === 'connected' ? 'accent' : info.status.startsWith('error:') ? 'error' : 'warn';
  $: badgeClass =
    tone === 'accent'
      ? 'border-accent bg-accent-soft text-accent'
      : tone === 'error'
        ? 'border-error text-error'
        : 'border-warn bg-warn/10 text-warn';
  $: badgeLabel = info.status.startsWith('error:')
    ? info.status.slice('error:'.length).trim()
    : info.status;
</script>

<div class="rounded-2xl border border-border bg-surface-1 p-5">
  <div class="flex items-center justify-between gap-3">
    <h2 class="text-[14px] font-semibold text-text">Game disc identification</h2>
    <span
      class="inline-flex items-center gap-1.5 rounded-md border px-2 py-[3px] text-[11px] font-medium tracking-wide {badgeClass}"
    >
      {#if tone === 'accent'}
        <span class="h-1.5 w-1.5 rounded-full" style="background: currentColor"></span>
      {/if}
      {badgeLabel}
    </span>
  </div>

  <p class="mt-1 text-[12px] text-text-3">
    Pre-rip naming uses built-in <strong>boot-code maps</strong>; post-rip verification uses
    <strong>Redump .dat files</strong> you provide. Manual search falls back to
    <strong>IGDB</strong> ({igdbConfigured ? 'configured above' : 'not configured'}).
  </p>

  <div class="mt-4 overflow-hidden rounded-md border border-border">
    <div
      class="grid items-center gap-3 border-b border-border bg-surface-2 px-3 py-2 text-[11px] font-medium uppercase tracking-[0.12em] text-text-3"
      style="grid-template-columns: 1fr auto auto"
    >
      <div>System</div>
      <div class="w-28 text-right">Boot-code</div>
      <div class="w-28 text-right">Redump dat</div>
    </div>
    {#each info.systems as s (s.system)}
      <div
        class="grid items-center gap-3 border-b border-border px-3 py-2 text-[12px] last:border-b-0"
        style="grid-template-columns: 1fr auto auto"
      >
        <div class="min-w-0 text-text">
          {s.label}
          <span class="ml-1.5 font-mono text-[11px] text-text-3">{s.subdir}/</span>
        </div>
        <div class="w-28 text-right">
          {#if s.boot_code === 'ok'}
            <span class="text-accent">✓ built-in</span>
          {:else if s.boot_code === 'na'}
            <span class="text-text-3" title="not applicable for this system">— n/a</span>
          {:else}
            <span class="text-text-3">✗ missing</span>
          {/if}
        </div>
        <div class="w-28 text-right">
          {#if s.redump_dat === 'loaded'}
            <span class="text-accent">✓ loaded</span>
          {:else}
            <span class="text-warn">✗ missing</span>
          {/if}
        </div>
      </div>
    {/each}
  </div>

  <div class="mt-3 space-y-1 text-[11px] text-text-3">
    {#if info.redumper_status.startsWith('error:')}
      <div class="text-error">redumper: {info.redumper_status.slice('error:'.length).trim()}</div>
    {/if}
    <div>
      Drop each system's Redump <code class="rounded bg-surface-2 px-1 font-mono">.dat</code> into
      its folder (shown above) under
      <code class="rounded bg-surface-2 px-1 font-mono text-text-2">{info.dat_dir || '—'}/</code>,
      then restart to load them.
    </div>
    <div>
      <a
        href="http://redump.org/downloads/"
        target="_blank"
        rel="noopener noreferrer"
        class="underline decoration-dotted underline-offset-2 hover:text-text"
      >
        Get dat-files → redump.org ↗
      </a>
    </div>
  </div>
</div>
