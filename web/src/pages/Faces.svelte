<script lang="ts">
  import {
    getFaces, enrollFace, deleteFace, getEvents, snapshotUrl,
    type FaceIdentity, type SentinelEvent,
  } from '../lib/api'
  import { timeAgo } from '../lib/utils'

  let faces = $state<FaceIdentity[]>([])
  let personEvents = $state<SentinelEvent[]>([])
  let loading = $state(true)
  let error = $state<string | null>(null)
  let notice = $state<string | null>(null)

  let selected = $state<SentinelEvent | null>(null)
  let nameInput = $state('')
  let busy = $state(false)

  // Group identities by name (a person can have several enrolled embeddings).
  const grouped = $derived.by(() => {
    const m = new Map<string, FaceIdentity[]>()
    for (const f of faces) {
      const l = m.get(f.name) ?? []
      l.push(f)
      m.set(f.name, l)
    }
    return [...m.entries()].sort((a, b) => a[0].localeCompare(b[0]))
  })

  async function load() {
    try {
      loading = true
      error = null
      const [f, ev] = await Promise.all([
        getFaces(),
        getEvents({ label: 'person', has_snapshot: true, limit: 30 } as any),
      ])
      faces = f ?? []
      personEvents = ev ?? []
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load faces'
    } finally {
      loading = false
    }
  }

  $effect(() => { load() })

  async function doEnroll() {
    if (!selected || !nameInput.trim()) return
    busy = true
    error = null
    notice = null
    try {
      const r = await enrollFace(nameInput.trim(), selected.id)
      notice = `Enrolled ${r.name}.`
      selected = null
      nameInput = ''
      faces = (await getFaces()) ?? []
    } catch (e) {
      error = e instanceof Error ? e.message : 'Enroll failed'
    } finally {
      busy = false
    }
  }

  async function removeIdentity(name: string, ids: FaceIdentity[]) {
    if (!confirm(`Remove all ${ids.length} enrolled image(s) for "${name}"?`)) return
    busy = true
    try {
      for (const f of ids) await deleteFace(f.id)
      faces = (await getFaces()) ?? []
    } catch (e) {
      error = e instanceof Error ? e.message : 'Delete failed'
    } finally {
      busy = false
    }
  }
</script>

<div class="p-4 sm:p-6 max-w-screen-2xl mx-auto flex flex-col gap-6">
  <h1 class="text-xl font-semibold text-white">Faces</h1>

  {#if error}
    <div class="card p-3 text-sm text-red-400">{error}</div>
  {/if}
  {#if notice}
    <div class="card p-3 text-sm text-green-400">{notice}</div>
  {/if}

  <!-- Enrolled identities -->
  <section class="card p-4">
    <h2 class="text-sm font-medium text-gray-300 mb-3">Enrolled people</h2>
    {#if loading}
      <p class="text-sm text-gray-500">Loading…</p>
    {:else if grouped.length === 0}
      <p class="text-sm text-gray-500">No faces enrolled yet. Pick a snapshot below to enroll someone.</p>
    {:else}
      <div class="flex flex-wrap gap-2">
        {#each grouped as [name, ids]}
          <div class="flex items-center gap-2 bg-surface-3 rounded-lg pl-3 pr-2 py-1.5">
            <span class="text-sm text-white font-medium">{name}</span>
            <span class="text-xs text-gray-500">×{ids.length}</span>
            <button
              class="text-gray-500 hover:text-red-400 transition-colors"
              title="Remove {name}"
              disabled={busy}
              onclick={() => removeIdentity(name, ids)}
              aria-label="Remove {name}"
            >
              <svg class="w-4 h-4" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
              </svg>
            </button>
          </div>
        {/each}
      </div>
    {/if}
  </section>

  <!-- Enroll from a recent person snapshot -->
  <section class="card p-4">
    <h2 class="text-sm font-medium text-gray-300 mb-1">Enroll from a recent person event</h2>
    <p class="text-xs text-gray-500 mb-3">Click a snapshot that clearly shows the person's face, then give them a name.</p>

    {#if selected}
      <div class="flex items-center gap-3 mb-4 p-3 rounded-lg bg-surface-3">
        <img src={snapshotUrl(selected.id)} alt="selected" class="w-20 h-20 object-cover rounded" />
        <div class="flex-1 flex flex-col sm:flex-row gap-2 sm:items-center">
          <input
            class="input flex-1"
            placeholder="Name (e.g. Bo)"
            bind:value={nameInput}
            onkeydown={(e) => e.key === 'Enter' && doEnroll()}
          />
          <div class="flex gap-2">
            <button class="btn" disabled={busy || !nameInput.trim()} onclick={doEnroll}>
              {busy ? 'Enrolling…' : 'Enroll'}
            </button>
            <button class="btn-ghost" disabled={busy} onclick={() => { selected = null; nameInput = '' }}>Cancel</button>
          </div>
        </div>
      </div>
    {/if}

    {#if loading}
      <div class="grid grid-cols-3 sm:grid-cols-6 gap-2">
        {#each { length: 12 } as _}
          <div class="aspect-square rounded-lg bg-surface-3 animate-pulse"></div>
        {/each}
      </div>
    {:else if personEvents.length === 0}
      <p class="text-sm text-gray-500">No recent person events with snapshots.</p>
    {:else}
      <div class="grid grid-cols-3 sm:grid-cols-6 gap-2">
        {#each personEvents as ev (ev.id)}
          <button
            class="relative aspect-square rounded-lg overflow-hidden border-2 transition-colors
                   {selected?.id === ev.id ? 'border-accent' : 'border-transparent hover:border-surface-3'}"
            onclick={() => { selected = ev; notice = null }}
            title={new Date(ev.start_time * 1000).toLocaleString()}
          >
            <img src={snapshotUrl(ev.id)} alt="person" class="w-full h-full object-cover" loading="lazy" />
            <span class="absolute bottom-0 inset-x-0 bg-black/60 text-[10px] text-gray-300 px-1 py-0.5 text-left">
              {ev.camera} · {timeAgo(ev.start_time)}
            </span>
          </button>
        {/each}
      </div>
    {/if}
  </section>
</div>
