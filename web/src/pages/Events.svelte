<script lang="ts">
  import type { SentinelEvent, Camera } from '../lib/api'
  import { getEvents, getCameras } from '../lib/api'
  import type { WSMessage } from '../lib/ws'
  import { ws } from '../lib/ws'
  import EventCard from '../components/EventCard.svelte'

  const PAGE_SIZE = 50

  let events: SentinelEvent[] = $state([])
  let cameras: Camera[] = $state([])
  let loading = $state(true)
  let loadingMore = $state(false)
  let error: string | null = $state(null)
  let hasMore = $state(true)
  let expandedId = $state('')

  // Filters
  let filterCamera = $state('')
  let filterLabel = $state('')
  let filterAfter = $state('')
  let filterBefore = $state('')
  let filterHasClip = $state(false)

  const KNOWN_LABELS = ['person', 'car', 'truck', 'dog', 'cat', 'bike', 'motorcycle', 'bird', 'package']

  async function load(reset = true) {
    if (reset) {
      loading = true
      error = null
    } else {
      loadingMore = true
    }

    try {
      const params: Record<string, string | number | boolean | undefined> = {
        limit: PAGE_SIZE,
        skip: reset ? 0 : events.length,
      }
      if (filterCamera) params.camera = filterCamera
      if (filterLabel) params.label = filterLabel
      if (filterAfter) params.after = new Date(filterAfter).getTime() / 1000
      if (filterBefore) params.before = new Date(filterBefore).getTime() / 1000
      if (filterHasClip) params.has_clip = true

      const newEvents = await getEvents(params as Parameters<typeof getEvents>[0])
      if (reset) {
        events = newEvents
      } else {
        events = [...events, ...newEvents]
      }
      hasMore = newEvents.length === PAGE_SIZE
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load events'
    } finally {
      loading = false
      loadingMore = false
    }
  }

  async function loadCameras() {
    try {
      cameras = await getCameras()
    } catch {
      // non-fatal
    }
  }

  function handleExpand(id: string) {
    expandedId = id
  }

  function handleWS(msg: WSMessage) {
    if (!msg.payload) return
    if (msg.type === 'new') {
      // Prepend new event if it passes the current filters
      const ev = msg.payload
      const matchesCamera = !filterCamera || ev.camera === filterCamera
      const matchesLabel = !filterLabel || ev.label === filterLabel
      if (matchesCamera && matchesLabel) {
        events = [ev, ...events]
      }
    } else if (msg.type === 'update' || msg.type === 'end') {
      const ev = msg.payload
      events = events.map(e => e.id === ev.id ? ev : e)
    }
  }

  function applyFilters() {
    load(true)
  }

  function clearFilters() {
    filterCamera = ''
    filterLabel = ''
    filterAfter = ''
    filterBefore = ''
    filterHasClip = false
    load(true)
  }

  $effect(() => {
    loadCameras()
    load(true)
    const unsub = ws.onMessage(handleWS)
    return unsub
  })
</script>

<div class="p-4 sm:p-6 max-w-screen-2xl mx-auto">
  <!-- Header -->
  <div class="flex items-center justify-between mb-5">
    <div>
      <h1 class="text-xl font-semibold text-white">Events</h1>
      {#if !loading}
        <p class="text-sm text-gray-500 mt-0.5">{events.length} event{events.length !== 1 ? 's' : ''} loaded</p>
      {/if}
    </div>
  </div>

  <!-- Filters -->
  <div class="card mb-5 p-4">
    <div class="flex flex-wrap gap-3">
      <!-- Camera filter -->
      <div class="flex-1 min-w-[140px]">
        <label for="filter-camera" class="block text-xs text-gray-500 mb-1">Camera</label>
        <select id="filter-camera" class="select w-full" bind:value={filterCamera}>
          <option value="">All cameras</option>
          {#each cameras as cam}
            <option value={cam.name}>{cam.name}</option>
          {/each}
        </select>
      </div>

      <!-- Label filter -->
      <div class="flex-1 min-w-[140px]">
        <label for="filter-label" class="block text-xs text-gray-500 mb-1">Label</label>
        <select id="filter-label" class="select w-full" bind:value={filterLabel}>
          <option value="">All labels</option>
          {#each KNOWN_LABELS as lbl}
            <option value={lbl}>{lbl.charAt(0).toUpperCase() + lbl.slice(1)}</option>
          {/each}
        </select>
      </div>

      <!-- After date -->
      <div class="flex-1 min-w-[160px]">
        <label for="filter-after" class="block text-xs text-gray-500 mb-1">After</label>
        <input id="filter-after" type="datetime-local" class="input w-full" bind:value={filterAfter} />
      </div>

      <!-- Before date -->
      <div class="flex-1 min-w-[160px]">
        <label for="filter-before" class="block text-xs text-gray-500 mb-1">Before</label>
        <input id="filter-before" type="datetime-local" class="input w-full" bind:value={filterBefore} />
      </div>

      <!-- Has clip checkbox -->
      <div class="flex items-end pb-0.5">
        <label class="flex items-center gap-2 cursor-pointer text-sm text-gray-300">
          <input
            type="checkbox"
            bind:checked={filterHasClip}
            class="w-4 h-4 rounded bg-surface-3 border-surface-4 accent-accent"
          />
          Has clip
        </label>
      </div>
    </div>

    <!-- Filter actions -->
    <div class="flex gap-2 mt-3 pt-3 border-t border-surface-3">
      <button class="btn-primary" onclick={applyFilters}>
        <svg class="w-3.5 h-3.5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M3 4a1 1 0 011-1h16a1 1 0 011 1v2.586a1 1 0 01-.293.707l-6.414 6.414a1 1 0 00-.293.707V17l-4 4v-6.586a1 1 0 00-.293-.707L3.293 7.293A1 1 0 013 6.586V4z" />
        </svg>
        Apply
      </button>
      <button class="btn-ghost" onclick={clearFilters}>Clear</button>
    </div>
  </div>

  <!-- Event list -->
  {#if loading}
    <div class="space-y-2">
      {#each { length: 5 } as _}
        <div class="rounded-xl bg-surface-2 border border-surface-3 p-3 flex gap-3 animate-pulse">
          <div class="w-28 h-20 bg-surface-3 rounded-lg shrink-0"></div>
          <div class="flex-1 space-y-2 pt-1">
            <div class="h-4 bg-surface-3 rounded w-1/4"></div>
            <div class="h-3 bg-surface-3 rounded w-1/3"></div>
            <div class="h-3 bg-surface-3 rounded w-1/5"></div>
          </div>
        </div>
      {/each}
    </div>
  {:else if error}
    <div class="flex flex-col items-center justify-center h-48 gap-3 text-gray-500">
      <p class="text-sm">{error}</p>
      <button class="btn-ghost" onclick={() => load(true)}>Try again</button>
    </div>
  {:else if events.length === 0}
    <div class="flex flex-col items-center justify-center h-48 gap-3 text-gray-500">
      <svg class="w-10 h-10" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
      </svg>
      <p class="text-sm">No events found</p>
      {#if filterCamera || filterLabel || filterAfter || filterBefore || filterHasClip}
        <button class="btn-ghost" onclick={clearFilters}>Clear filters</button>
      {/if}
    </div>
  {:else}
    <div class="space-y-1.5">
      {#each events as event (event.id)}
        <EventCard
          {event}
          expanded={expandedId === event.id}
          onExpand={handleExpand}
        />
      {/each}
    </div>

    <!-- Load more -->
    {#if hasMore}
      <div class="flex justify-center mt-4">
        <button
          class="btn-ghost"
          onclick={() => load(false)}
          disabled={loadingMore}
        >
          {#if loadingMore}
            <svg class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
              <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
              <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/>
            </svg>
            Loading…
          {:else}
            Load more
          {/if}
        </button>
      </div>
    {:else if events.length > 0}
      <p class="text-center text-xs text-gray-600 mt-4">All events loaded</p>
    {/if}
  {/if}
</div>
