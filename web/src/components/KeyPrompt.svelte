<script lang="ts">
  import { getApiKey, setApiKey } from '../lib/auth'

  let key = $state('')
  const hadKey = getApiKey() !== ''

  function submit(e: SubmitEvent) {
    e.preventDefault()
    const value = key.trim()
    if (!value) return
    setApiKey(value)
    // Reload so every image, video and the event stream pick the key up.
    window.location.reload()
  }
</script>

<div
  class="fixed inset-0 z-[60] bg-black/80 backdrop-blur-sm flex items-center justify-center p-4"
  role="dialog"
  aria-modal="true"
  aria-labelledby="key-prompt-title"
>
  <form onsubmit={submit} class="card w-full max-w-sm p-6 space-y-4 animate-fade-in">
    <h2 id="key-prompt-title" class="text-lg font-semibold text-white">API key required</h2>
    <p class="text-sm text-gray-400">
      {#if hadKey}
        The saved key was not accepted. Enter the current API key.
      {:else}
        This Sentinel has authentication turned on. Enter its API key to continue.
      {/if}
    </p>
    <!-- svelte-ignore a11y_autofocus -->
    <input
      type="password"
      bind:value={key}
      autocomplete="current-password"
      placeholder="API key"
      aria-label="API key"
      class="input w-full"
      autofocus
    />
    <p class="text-xs text-gray-500">Stored in this browser only.</p>
    <button type="submit" class="btn btn-primary w-full" disabled={!key.trim()}>Continue</button>
  </form>
</div>
