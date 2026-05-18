<script lang="ts" context="module">
  // FieldSpec drives the form rows. Defined by the parent so each
  // integration can declare its own field set without IntegrationEditor
  // hard-coding per-integration knowledge.
  export type FieldSpec = {
    name: string;
    label: string;
    secret: boolean; // mask in display + use type="password" while editing
  };
</script>

<script lang="ts">
  import { apiGet, apiPut, apiDelete, apiPost } from '$lib/api';
  import { pushToast } from '$lib/toasts';
  import type { IntegrationDetail, IntegrationTestResult } from '$lib/wire';

  export let detail: IntegrationDetail;
  export let fields: FieldSpec[];
  // testable controls whether the Test button is rendered. MakeMKV
  // passes false; IGDB and TMDB pass true.
  export let testable: boolean = true;
  // envHint is the env var name shown in the tooltip when source==='env'.
  export let envHint: string = '';

  let editing = false;
  let draft: Record<string, string> = {};
  let touched: Record<string, boolean> = {};
  let saving = false;
  let testResult: IntegrationTestResult | null = null;
  let confirmingClear = false;

  $: sourceLabel = (() => {
    if (detail.source === 'ui') return 'Configured (UI)';
    if (detail.source === 'env') return 'Configured (env)';
    return 'Unset';
  })();

  $: sourceTone = detail.source === 'unset' ? 'unset' : 'set';

  function openEditor(): void {
    draft = { ...detail.values };
    touched = {};
    testResult = null;
    editing = true;
  }

  function cancelEditor(): void {
    editing = false;
    confirmingClear = false;
  }

  function onFieldInput(name: string, ev: Event): void {
    const value = (ev.target as HTMLInputElement).value;
    draft = { ...draft, [name]: value };
    touched = { ...touched, [name]: true };
  }

  async function runTest(): Promise<void> {
    testResult = null;
    try {
      const body: Record<string, string> = {};
      for (const f of fields) body[f.name] = draft[f.name] ?? '';
      testResult = await apiPost<IntegrationTestResult>(
        `/api/integrations/${detail.name}/test`,
        body,
      );
    } catch (e) {
      testResult = { ok: false, error: (e as Error).message };
    }
  }

  async function saveEditor(): Promise<void> {
    saving = true;
    try {
      const body: Record<string, string> = {};
      for (const f of fields) {
        if (touched[f.name]) body[f.name] = draft[f.name] ?? '';
      }
      if (Object.keys(body).length === 0) {
        editing = false;
        return;
      }
      const updated = await apiPut<IntegrationDetail>(`/api/integrations/${detail.name}`, body);
      detail = updated;
      pushToast('success', `Saved ${detail.name.toUpperCase()} credentials`);
      editing = false;
    } catch (e) {
      pushToast('error', (e as Error).message);
    } finally {
      saving = false;
    }
  }

  async function clearCredentials(): Promise<void> {
    saving = true;
    try {
      const updated = await apiDelete(`/api/integrations/${detail.name}`).then(() =>
        apiGet<IntegrationDetail>(`/api/integrations/${detail.name}`),
      );
      detail = updated;
      const tail =
        detail.source === 'env'
          ? 'Falling back to env.'
          : detail.source === 'unset'
            ? 'Integration is now unset.'
            : '';
      pushToast('success', `Cleared ${detail.name.toUpperCase()} credentials. ${tail}`);
      editing = false;
      confirmingClear = false;
    } catch (e) {
      pushToast('error', (e as Error).message);
    } finally {
      saving = false;
    }
  }
</script>

<div class="px-4 py-3" data-testid="integration-row" data-integration={detail.name}>
  <div class="grid items-center gap-4" style="grid-template-columns: 1fr auto auto">
    <div>
      <div class="text-[13px] font-medium text-text uppercase tracking-wide">
        {detail.name}
      </div>
      <div class="text-[11px] text-text-3">
        {#if !editing}
          {#if detail.configured}
            {#each fields as f, i (f.name)}
              {#if f.secret}
                ••• <span class="text-text-3">({f.label})</span>{#if i < fields.length - 1},
                {/if}
              {:else}
                <span class="font-mono">{detail.values[f.name] ?? ''}</span>
                {#if i < fields.length - 1},
                {/if}
              {/if}
            {/each}
          {:else}
            <span class="text-text-3">no credentials set</span>
          {/if}
        {/if}
      </div>
    </div>
    <span
      class="inline-flex items-center rounded-md border border-border px-2 py-[3px]
             text-[11px] font-medium uppercase tracking-wide"
      class:bg-surface-2={sourceTone === 'unset'}
      class:text-text-2={sourceTone === 'unset'}
      class:bg-accent-soft={sourceTone === 'set'}
      class:text-accent={sourceTone === 'set'}
      title={detail.source === 'env' && envHint ? `Set via ${envHint} — edit here to override` : ''}
      data-testid="integration-source"
    >
      {sourceLabel}
    </span>
    {#if !editing}
      <button
        type="button"
        on:click={openEditor}
        class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2"
        data-testid="integration-edit"
      >
        Edit
      </button>
    {/if}
  </div>

  {#if editing}
    <div class="mt-3 space-y-2 rounded-md border border-border bg-surface-2 p-3">
      {#each fields as f (f.name)}
        <label class="flex flex-col gap-1 text-[12px] text-text-2">
          <span>{f.label}</span>
          <input
            type={f.secret ? 'password' : 'text'}
            value={draft[f.name] ?? ''}
            on:input={(e) => onFieldInput(f.name, e)}
            class="rounded border border-border bg-surface-1 px-2 py-1 font-mono text-text"
            data-testid={`integration-field-${f.name}`}
          />
        </label>
      {/each}

      {#if testResult}
        <div
          class="text-[12px]"
          class:text-accent={testResult.ok}
          class:text-error={!testResult.ok}
        >
          {#if testResult.ok}
            ✓ Credentials valid
          {:else}
            ✗ {testResult.error ?? 'Test failed'}
            {#if testResult.http_status}
              (HTTP {testResult.http_status})
            {/if}
          {/if}
        </div>
      {/if}

      {#if !testable}
        <div class="text-[11px] text-text-3">Validated on next rip.</div>
      {/if}

      <div class="flex items-center justify-between gap-2 pt-2">
        <div class="flex items-center gap-2">
          {#if testable}
            <button
              type="button"
              on:click={runTest}
              disabled={saving}
              class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2 disabled:opacity-50"
              data-testid="integration-test"
            >
              Test
            </button>
          {/if}
          {#if confirmingClear}
            <button
              type="button"
              on:click={clearCredentials}
              disabled={saving}
              class="rounded-md bg-error px-3 py-1.5 text-[12px] font-semibold text-black disabled:opacity-50"
              data-testid="integration-clear-confirm"
            >
              Yes, clear credentials
            </button>
            <button
              type="button"
              on:click={() => (confirmingClear = false)}
              class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2"
            >
              Back
            </button>
          {:else if detail.configured && detail.source === 'ui'}
            <button
              type="button"
              on:click={() => (confirmingClear = true)}
              class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2"
              data-testid="integration-clear"
            >
              Clear credentials
            </button>
          {/if}
        </div>
        <div class="flex items-center gap-2">
          <button
            type="button"
            on:click={cancelEditor}
            disabled={saving}
            class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2 disabled:opacity-50"
          >
            Cancel
          </button>
          <button
            type="button"
            on:click={saveEditor}
            disabled={saving}
            class="rounded-md bg-accent px-3 py-1.5 text-[12px] font-semibold text-black disabled:opacity-50"
            data-testid="integration-save"
          >
            Save
          </button>
        </div>
      </div>
    </div>
  {/if}
</div>
