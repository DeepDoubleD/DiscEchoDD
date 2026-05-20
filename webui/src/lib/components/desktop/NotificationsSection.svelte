<script lang="ts">
  import { notifications } from '$lib/store';
  import NotificationEditor from './NotificationEditor.svelte';
  import Callout from '$lib/components/Callout.svelte';

  let creating = false;

  function onCreated(): void {
    creating = false;
  }
</script>

<section data-section="notifications" class="rounded-2xl border border-border bg-surface-1 p-5">
  <div class="flex items-center justify-between">
    <h2 class="text-[14px] font-semibold text-text">Notifications</h2>
    <button
      on:click={() => (creating = true)}
      class="rounded-md border border-border px-3 py-1.5 text-[12px] text-text-2"
    >
      + New notification
    </button>
  </div>

  <div class="mt-4">
    <Callout tone="info" title="Powered by Apprise">
      <p>
        DiscEcho sends notifications through <strong>Apprise</strong>, which fans out to 100+
        services from a single URL. Paste an Apprise <strong>service URL</strong> into a
        notification — e.g.
        <code class="rounded bg-surface-2 px-1 font-mono text-[11px]">discord://…</code>,
        <code class="rounded bg-surface-2 px-1 font-mono text-[11px]">ntfy://…</code>, or
        <code class="rounded bg-surface-2 px-1 font-mono text-[11px]">tgram://…</code>.
      </p>
      <p class="mt-1.5">
        Use <strong>Triggers</strong> to choose which events fire — rich
        <strong>rip-complete</strong> messages and, now, <strong>failure</strong> alerts.
      </p>
      <div class="mt-2">
        <a
          href="https://appriseit.com/services/"
          target="_blank"
          rel="noopener noreferrer"
          class="underline decoration-dotted underline-offset-2 hover:text-text"
        >
          Browse supported services ↗
        </a>
      </div>
    </Callout>
  </div>

  <div class="mt-4 space-y-3">
    {#each $notifications as n (n.id)}
      <NotificationEditor notification={n} creating={false} />
    {/each}
    {#if creating}
      <NotificationEditor notification={null} creating={true} on:saved={onCreated} />
    {/if}
    {#if $notifications.length === 0 && !creating}
      <div class="text-[12px] text-text-3">
        No notifications. Click "+ New notification" to add one.
      </div>
    {/if}
  </div>
</section>
