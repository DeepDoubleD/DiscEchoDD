<script lang="ts">
  import type { Profile, DiscType } from '$lib/wire';
  import { createProfile, updateProfile, deleteProfile } from '$lib/store';
  import { parseValidationErrors, type ValidationErrors } from '$lib/api';
  import { pushToast } from '$lib/toasts';
  import {
    ENGINES,
    DISC_TYPES,
    DISC_TYPE_DEFAULTS,
    HDR_PIPELINES,
    HDR_PIPELINE_LABELS,
    DRIVE_POLICIES,
    DRIVE_POLICY_LABELS,
    QUALITY_TIER_SLUGS,
    QUALITY_TIER_LABELS,
    enginesFor,
    defaultsFor,
    resolveTier,
    varsFor,
    specFor,
    requirementFor,
  } from '$lib/profile_schema';
  import { createEventDispatcher } from 'svelte';
  import DiscTypeBadge, { DISC_TYPE_META } from '$lib/components/DiscTypeBadge.svelte';
  import Callout from '$lib/components/Callout.svelte';
  import FormSection from './FormSection.svelte';
  import FormRow from './FormRow.svelte';
  import PathField from './PathField.svelte';

  // Splits a requirement line into plain / **bold** / `code` segments so the
  // template can render emphasis without {@html} (content is static, but this
  // keeps it injection-proof and matches the design's inline styling).
  type Seg = { text: string; kind: 'plain' | 'bold' | 'code' };
  function segments(s: string): Seg[] {
    const out: Seg[] = [];
    const re = /\*\*([^*]+)\*\*|`([^`]+)`/g;
    let last = 0;
    let m: RegExpExecArray | null;
    while ((m = re.exec(s)) !== null) {
      if (m.index > last) out.push({ text: s.slice(last, m.index), kind: 'plain' });
      if (m[1] !== undefined) out.push({ text: m[1], kind: 'bold' });
      else out.push({ text: m[2], kind: 'code' });
      last = re.lastIndex;
    }
    if (last < s.length) out.push({ text: s.slice(last), kind: 'plain' });
    return out;
  }

  export let profile: Profile | null = null;
  export let creating: boolean = false;
  // `chromeless` strips the in-form header (name input + duplicate / save /
  // enabled checkbox) and the in-form Delete button. The mobile drill-down
  // route (`/profiles/[id]`) renders its own AppBar and sticky action bar
  // and drives onSave / onDelete via component bindings instead.
  export let chromeless: boolean = false;

  const dispatch = createEventDispatcher<{ saved: void; duplicate: Profile }>();

  let working: Profile = blank();
  let fieldErrors: ValidationErrors = {};
  let genericError: string | null = null;
  let saving = false;
  let confirmingDelete = false;

  // Re-seed working copy whenever the parent swaps profile or toggles
  // creating mode. Keyed on profile?.id + creating so mid-edit
  // re-renders of the same profile don't blow away the working copy.
  let lastKey = '';
  $: {
    const key = (profile?.id ?? '') + ':' + (creating ? '1' : '0');
    if (key !== lastKey) {
      lastKey = key;
      if (profile) {
        working = clone(profile);
      } else if (creating) {
        working = blank();
      }
      fieldErrors = {};
      genericError = null;
      confirmingDelete = false;
    }
  }

  $: spec = specFor(working.engine);
  $: validEngines = enginesFor(working.disc_type);
  $: validContainers = spec?.containers ?? [working.container || working.format || ''];
  $: validVideoCodecs = spec?.videoCodecs ?? [];
  // quality_rf + encoder_preset are owned by the quality-tier control, not
  // the generic engine-options dump.
  $: optionKeys = spec
    ? Object.keys(spec.options).filter((k) => k !== 'quality_rf' && k !== 'encoder_preset')
    : [];
  $: discType = working.disc_type as DiscType;
  $: discMeta = DISC_TYPE_META[discType] ?? null;
  $: requirement = requirementFor(discType);
  // A quality tier only applies when the codec actually encodes ("copy" is
  // a lossless passthrough). selectedTier coerces any unrecognised stored
  // value (legacy free-text) to "custom" so the select stays valid.
  $: isEncodeBearing = !!working.video_codec && working.video_codec !== 'copy';
  $: selectedTier = QUALITY_TIER_SLUGS.includes(working.quality_preset)
    ? working.quality_preset
    : 'custom';
  $: pathVars = varsFor(working.disc_type);
  $: previewPath = renderPreview(working.output_path_template, working.disc_type);

  function blank(): Profile {
    const d = DISC_TYPE_DEFAULTS.AUDIO_CD;
    return {
      id: '',
      disc_type: 'AUDIO_CD',
      name: '',
      engine: d.engine,
      format: d.container,
      preset: d.qualityPreset,
      container: d.container,
      video_codec: d.videoCodec,
      quality_preset: d.qualityPreset,
      hdr_pipeline: '',
      drive_policy: 'any',
      options: {},
      output_path_template: d.outputPathTemplate,
      enabled: true,
      step_count: ENGINES[d.engine].stepCount,
      created_at: '',
      updated_at: '',
    };
  }

  function clone(p: Profile): Profile {
    return JSON.parse(JSON.stringify(p));
  }

  function onEngineChange(): void {
    const s = specFor(working.engine);
    if (s) {
      if (!s.containers.includes(working.container)) {
        working.container = s.containers[0] ?? '';
      }
      working.format = working.container;
      if (s.videoCodecs.length === 0) {
        working.video_codec = '';
      } else if (!s.videoCodecs.includes(working.video_codec)) {
        working.video_codec = s.videoCodecs[0];
      }
      working.step_count = s.stepCount;
      // Drop options not in the new schema.
      const filtered: Record<string, unknown> = {};
      for (const k of Object.keys(working.options)) {
        if (s.options[k]) filtered[k] = working.options[k];
      }
      working.options = filtered;
      applyTierForCodec();
    }
  }

  // onDiscTypeChange (create-only) pre-fills a valid, runnable profile from
  // the per-disc-type defaults table, then normalises engine-derived state.
  function onDiscTypeChange(): void {
    const d = defaultsFor(working.disc_type);
    if (!d) return;
    working.engine = d.engine;
    working.container = d.container;
    working.format = d.container;
    working.video_codec = d.videoCodec;
    working.quality_preset = d.qualityPreset;
    working.output_path_template = d.outputPathTemplate;
    const s = specFor(working.engine);
    working.step_count = s?.stepCount ?? working.step_count;
    // Drop options that don't belong to the new engine, then let the tier
    // control repopulate quality_rf / encoder_preset.
    const filtered: Record<string, unknown> = {};
    for (const k of Object.keys(working.options)) {
      if (s?.options[k]) filtered[k] = working.options[k];
    }
    working.options = filtered;
    applyTierForCodec();
  }

  function onContainerChange(): void {
    // Keep the deprecated `format` mirror in sync for the one-release
    // window where the daemon still falls back to it.
    working.format = working.container;
  }

  function onVideoCodecChange(): void {
    applyTierForCodec();
  }

  // applyTierForCodec keeps quality_preset + options.quality_rf/encoder_preset
  // consistent after an engine/codec change. Lossless codecs carry no tier;
  // encode-bearing codecs default to "high" and resolve the RF + preset for
  // the current codec. "custom" keeps whatever raw values the user set.
  function applyTierForCodec(): void {
    if (!working.video_codec || working.video_codec === 'copy') {
      working.quality_preset = '';
      const opts = { ...working.options };
      delete opts.quality_rf;
      delete opts.encoder_preset;
      working.options = opts;
      return;
    }
    let tier = working.quality_preset;
    if (!QUALITY_TIER_SLUGS.includes(tier)) tier = 'high';
    if (tier === 'custom') {
      working.quality_preset = 'custom';
      return;
    }
    working.quality_preset = tier;
    const r = resolveTier(working.video_codec, tier);
    if (r) {
      working.options = {
        ...working.options,
        quality_rf: r.quality_rf,
        encoder_preset: r.encoder_preset,
      };
    }
  }

  function onTierChange(e: Event): void {
    const tier = (e.currentTarget as HTMLSelectElement).value;
    working.quality_preset = tier;
    if (tier !== 'custom') {
      const r = resolveTier(working.video_codec, tier);
      if (r) {
        working.options = {
          ...working.options,
          quality_rf: r.quality_rf,
          encoder_preset: r.encoder_preset,
        };
      }
    }
  }

  function appendVar(name: string): void {
    working.output_path_template = `${working.output_path_template}{{.${name}}}`;
  }

  // renderPreview does a best-effort client-side render of an output-path
  // template against sample values, covering the {{.X}}, {{printf "%0Nd"
  // .X}} and {{if .X}}…{{end}} forms the seeder templates use. Display only.
  function renderPreview(tmpl: string, dt: string): string {
    if (!tmpl) return '';
    const f = sampleFor(dt);
    let s = tmpl;
    s = s.replace(/\{\{if \.(\w+)\}\}(.*?)\{\{end\}\}/g, (_m, k, inner) => (f[k] ? inner : ''));
    s = s.replace(/\{\{printf "%0?(\d*)d" \.(\w+)\}\}/g, (_m, width, k) => {
      const v = f[k];
      if (v === undefined) return '';
      return String(v).padStart(Number(width) || 0, '0');
    });
    s = s.replace(/\{\{\.(\w+)\}\}/g, (_m, k) => (f[k] !== undefined ? String(f[k]) : ''));
    return s;
  }

  function sampleFor(dt: string): Record<string, string | number> {
    if (dt === 'AUDIO_CD') {
      return {
        Artist: 'Radiohead',
        Album: 'OK Computer',
        Year: 1997,
        TrackNumber: 3,
        Title: 'Paranoid Android',
        DiscNumber: 1,
      };
    }
    if (dt === 'PSX' || dt === 'PS2' || dt === 'SAT' || dt === 'DC' || dt === 'XBOX') {
      return { Title: 'Final Fantasy VII', Year: 1997, Region: 'USA' };
    }
    if (dt === 'DATA') {
      return { Title: 'BACKUP_DISC' };
    }
    // DVD / BDMV / UHD — populate both movie + series fields.
    return {
      Title: 'Inception',
      Year: 2010,
      Show: 'The Office',
      Season: 2,
      EpisodeNumber: 5,
      EpisodeTitle: 'Halloween',
    };
  }

  export async function onSave(): Promise<void> {
    saving = true;
    fieldErrors = {};
    genericError = null;
    try {
      const payload: Omit<Profile, 'id' | 'created_at' | 'updated_at'> = {
        disc_type: working.disc_type,
        name: working.name,
        engine: working.engine,
        format: working.container,
        preset: working.quality_preset,
        container: working.container,
        video_codec: working.video_codec,
        quality_preset: working.quality_preset,
        hdr_pipeline: working.hdr_pipeline,
        drive_policy: working.drive_policy,
        options: working.options,
        output_path_template: working.output_path_template,
        enabled: working.enabled,
        step_count: working.step_count,
      };
      if (creating) {
        await createProfile(payload);
      } else {
        await updateProfile(working.id, { ...payload, id: working.id } as Profile);
      }
      pushToast('success', creating ? 'Profile created' : 'Profile saved');
      dispatch('saved');
    } catch (e) {
      const fe = parseValidationErrors(e);
      if (fe) {
        fieldErrors = fe;
        // Surface any field errors that don't map to a control the form
        // actually renders (e.g. an options.<key> the engine schema
        // mirror is missing). Without this the save silently "does
        // nothing" — the 422 is parsed into fieldErrors but no visible
        // field shows it. See the MakeMKV+HandBrake schema-drift bug.
        const unmapped = unmappedErrors(fe);
        if (unmapped.length > 0) {
          genericError = `Save rejected: ${unmapped.join('; ')}`;
        }
      } else {
        genericError = (e as Error).message;
      }
    } finally {
      saving = false;
    }
  }

  // Field-error keys the form renders a control for. Anything else from
  // a 422 has no inline home and must fall through to the generic
  // banner so the user isn't left staring at a no-op Save button.
  function unmappedErrors(fe: ValidationErrors): string[] {
    const visible = new Set<string>([
      'name',
      'disc_type',
      'engine',
      'drive_policy',
      'container',
      'format',
      'video_codec',
      'quality_preset',
      'preset',
      'hdr_pipeline',
      'output_path_template',
    ]);
    for (const k of optionKeys) visible.add(`options.${k}`);
    return Object.entries(fe)
      .filter(([k]) => !visible.has(k))
      .map(([k, msg]) => `${k.replace(/^options\./, '')} — ${msg}`);
  }

  export async function onDelete(): Promise<void> {
    if (!confirmingDelete) {
      confirmingDelete = true;
      return;
    }
    saving = true;
    genericError = null;
    try {
      await deleteProfile(working.id);
      confirmingDelete = false;
      pushToast('success', 'Profile deleted');
      dispatch('saved');
    } catch (e) {
      genericError = (e as Error).message;
    } finally {
      saving = false;
    }
  }

  function onDuplicate(): void {
    const copy = clone(working);
    copy.id = '';
    copy.name = working.name ? `${working.name} (copy)` : '';
    copy.created_at = '';
    copy.updated_at = '';
    dispatch('duplicate', copy);
  }

  function setOption(key: string, value: unknown): void {
    working.options = { ...working.options, [key]: value };
  }

  function onOptionBoolChange(key: string, e: Event): void {
    setOption(key, (e.currentTarget as HTMLInputElement).checked);
  }

  function onOptionIntInput(key: string, e: Event): void {
    setOption(key, Number((e.currentTarget as HTMLInputElement).value));
  }

  function onOptionStringInput(key: string, e: Event): void {
    setOption(key, (e.currentTarget as HTMLInputElement).value);
  }

  function onOutputPathChange(e: CustomEvent<string>): void {
    working.output_path_template = e.detail;
  }

  const inputClass =
    'w-full rounded-md border border-border bg-surface-2 px-2 py-1.5 text-[13px] text-text';
  const inputDisabledClass = inputClass + ' disabled:opacity-60';
</script>

{#if !profile && !creating}
  <div class="rounded-2xl border border-border bg-surface-1 p-12 text-center">
    <div class="text-[14px] font-medium text-text-2">Select a profile to edit</div>
    <div class="mt-2 text-[12px] text-text-3">or click "+ New profile" to create one.</div>
  </div>
{:else}
  <form on:submit|preventDefault={onSave}>
    {#if genericError}
      <div
        class="mb-4 rounded-md border border-error/30 bg-error/10 px-3 py-2 text-[12px] text-error"
      >
        {genericError}
      </div>
    {/if}

    {#if !chromeless}
      <header class="mb-7 flex items-start justify-between gap-4">
        <div class="min-w-0">
          <div class="mb-1 flex items-center gap-2">
            {#if discMeta}
              <DiscTypeBadge type={discType} />
            {/if}
            <span class="text-[11px] font-medium uppercase tracking-[0.14em] text-text-3">
              profile
            </span>
          </div>
          {#if creating}
            <label class="block">
              <span class="sr-only">Name</span>
              <input
                name="name"
                type="text"
                bind:value={working.name}
                placeholder="New profile"
                class="w-full bg-transparent text-[24px] font-bold tracking-tight text-text outline-none placeholder:text-text-3"
              />
            </label>
          {:else}
            <label class="block">
              <span class="sr-only">Name</span>
              <input
                name="name"
                type="text"
                bind:value={working.name}
                class="w-full bg-transparent text-[24px] font-bold tracking-tight text-text outline-none"
              />
            </label>
          {/if}
          <div class="mt-1 font-mono text-[12px] text-text-3">
            applies to all {discMeta?.label ?? working.disc_type} discs
          </div>
          {#if fieldErrors.name}
            <div class="mt-1 text-[11px] text-error">{fieldErrors.name}</div>
          {/if}
        </div>

        <div class="flex flex-shrink-0 items-center gap-3">
          <label class="flex items-center gap-2">
            <input type="checkbox" bind:checked={working.enabled} />
            <span class="text-[12px] text-text-2">Enabled</span>
          </label>
          {#if !creating}
            <button
              type="button"
              on:click={onDuplicate}
              disabled={saving}
              class="rounded-md border border-border px-3 py-1.5 text-[13px] text-text-2 transition-colors hover:border-border-strong disabled:opacity-50"
            >
              Duplicate
            </button>
          {/if}
          <button
            type="submit"
            disabled={saving}
            class="rounded-md bg-accent px-4 py-1.5 text-[13px] font-semibold text-black disabled:opacity-50"
          >
            {creating ? 'Create' : 'Save changes'}
          </button>
        </div>
      </header>
    {/if}

    <div class="space-y-7">
      {#if requirement}
        <Callout tone={requirement.tone} title={requirement.title}>
          <ul class="space-y-1">
            {#each requirement.items as item}
              <li class="flex gap-1.5">
                <span aria-hidden="true" class="select-none text-text-3">•</span>
                <span>
                  {#each segments(item) as seg}
                    {#if seg.kind === 'bold'}<strong class="font-semibold text-text"
                        >{seg.text}</strong
                      >{:else if seg.kind === 'code'}<code
                        class="rounded bg-surface-2 px-1 font-mono text-[11px]">{seg.text}</code
                      >{:else}{seg.text}{/if}
                  {/each}
                </span>
              </li>
            {/each}
          </ul>
          {#if requirement.links?.length}
            <div class="mt-2 flex flex-wrap gap-x-4 gap-y-1">
              {#each requirement.links as link}
                <a
                  href={link.href}
                  target="_blank"
                  rel="noopener noreferrer"
                  class="underline decoration-dotted underline-offset-2 hover:text-text"
                >
                  {link.label} ↗
                </a>
              {/each}
            </div>
          {/if}
        </Callout>
      {/if}
      {#if creating}
        <FormSection title="Identity" sub="Disc type and engine lock once the profile is created.">
          <FormRow label="Disc type" error={fieldErrors.disc_type}>
            <select
              name="disc_type"
              bind:value={working.disc_type}
              on:change={onDiscTypeChange}
              class={inputClass}
            >
              {#each DISC_TYPES as dt}
                <option value={dt}>{dt}</option>
              {/each}
            </select>
          </FormRow>
        </FormSection>
      {/if}

      <FormSection title="Engine" sub="Which tool reads the disc and produces the source.">
        <FormRow label="Reader" error={fieldErrors.engine}>
          <select
            name="engine"
            bind:value={working.engine}
            on:change={onEngineChange}
            disabled={!creating}
            class={inputDisabledClass}
          >
            {#each validEngines as e}
              <option value={e}>{e}</option>
            {/each}
          </select>
        </FormRow>
        <FormRow label="Drive policy" error={fieldErrors.drive_policy}>
          <select name="drive_policy" bind:value={working.drive_policy} class={inputClass}>
            {#each DRIVE_POLICIES as dp}
              <option value={dp}>{DRIVE_POLICY_LABELS[dp] ?? dp}</option>
            {/each}
          </select>
        </FormRow>
      </FormSection>

      <FormSection title="Encoding" sub="Format and codec applied during transcode/compress.">
        <FormRow label="Container" error={fieldErrors.container ?? fieldErrors.format}>
          <select
            name="container"
            bind:value={working.container}
            on:change={onContainerChange}
            class={inputClass}
          >
            {#each validContainers as c}
              <option value={c}>{c}</option>
            {/each}
          </select>
        </FormRow>
        {#if validVideoCodecs.length > 0}
          <FormRow label="Video codec" error={fieldErrors.video_codec}>
            <select
              name="video_codec"
              bind:value={working.video_codec}
              on:change={onVideoCodecChange}
              class={inputClass}
            >
              {#each validVideoCodecs as v}
                <option value={v}>{v}</option>
              {/each}
            </select>
          </FormRow>
        {/if}
        {#if isEncodeBearing}
          <FormRow label="Quality" error={fieldErrors.quality_preset ?? fieldErrors.preset}>
            <select
              name="quality_preset"
              value={selectedTier}
              on:change={onTierChange}
              class={inputClass}
            >
              {#each QUALITY_TIER_SLUGS as slug}
                <option value={slug}>{QUALITY_TIER_LABELS[slug] ?? slug}</option>
              {/each}
            </select>
          </FormRow>
          {#if selectedTier === 'custom'}
            <FormRow label="RF (quality)">
              <input
                type="number"
                value={Number(working.options.quality_rf ?? 20)}
                on:input={(e) => onOptionIntInput('quality_rf', e)}
                class={inputClass}
              />
            </FormRow>
            <FormRow label="Encoder preset">
              <input
                type="text"
                value={String(working.options.encoder_preset ?? 'slow')}
                on:input={(e) => onOptionStringInput('encoder_preset', e)}
                class={inputClass}
              />
            </FormRow>
          {/if}
        {/if}
        {#if validVideoCodecs.length > 0}
          <FormRow label="HDR pipeline" error={fieldErrors.hdr_pipeline}>
            <select name="hdr_pipeline" bind:value={working.hdr_pipeline} class={inputClass}>
              {#each HDR_PIPELINES as h}
                <option value={h}>{HDR_PIPELINE_LABELS[h] ?? h}</option>
              {/each}
            </select>
          </FormRow>
        {/if}
      </FormSection>

      <FormSection title="Library">
        <FormRow label="Output path" error={fieldErrors.output_path_template}>
          <PathField value={working.output_path_template} on:change={onOutputPathChange} />
          {#if pathVars.length > 0}
            <div class="mt-2 flex flex-wrap gap-1.5">
              {#each pathVars as v}
                <button
                  type="button"
                  title={v.desc}
                  on:click={() => appendVar(v.name)}
                  class="rounded border border-border bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-text-2 transition-colors hover:border-accent hover:text-text"
                >
                  {'{{.' + v.name + '}}'}
                </button>
              {/each}
            </div>
          {/if}
          <div class="mt-2 text-[11px] text-text-3">
            Preview: <span class="font-mono text-text-2">{previewPath || '—'}</span>
          </div>
        </FormRow>
      </FormSection>

      {#if optionKeys.length > 0}
        <FormSection title="Engine options" sub="Engine-specific knobs not covered above.">
          {#each optionKeys as k}
            {@const opt = spec?.options[k]}
            <FormRow label={k} error={fieldErrors[`options.${k}`]}>
              {#if opt?.type === 'bool'}
                <input
                  type="checkbox"
                  checked={working.options[k] === undefined
                    ? Boolean(opt.defaultBool)
                    : Boolean(working.options[k])}
                  on:change={(e) => onOptionBoolChange(k, e)}
                />
              {:else if opt?.type === 'int'}
                <input
                  type="number"
                  value={Number(working.options[k] ?? 0)}
                  on:input={(e) => onOptionIntInput(k, e)}
                  class={inputClass}
                />
              {:else}
                <input
                  type="text"
                  value={String(working.options[k] ?? '')}
                  on:input={(e) => onOptionStringInput(k, e)}
                  class={inputClass}
                />
              {/if}
            </FormRow>
          {/each}
        </FormSection>
      {/if}

      <div class="text-[11px] text-text-3">Step count: {working.step_count}</div>

      {#if !creating && !chromeless}
        <div class="flex justify-end">
          <button
            type="button"
            on:click={onDelete}
            disabled={saving}
            class="rounded-md border border-border px-4 py-2 text-[13px] text-text-2 transition-colors hover:border-error hover:text-error disabled:opacity-50"
          >
            {confirmingDelete ? 'Confirm delete' : 'Delete profile'}
          </button>
        </div>
      {/if}
    </div>
  </form>
{/if}
