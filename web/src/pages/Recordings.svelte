<script lang="ts">
  import type { Camera, RecordingSummary } from '../lib/api'
  import { getCameras, getRecordingsSummary, vodPlaylistUrl } from '../lib/api'
  import { toDateString, fromDateString } from '../lib/utils'
  import VodPlayer from '../components/VodPlayer.svelte'

  let cameras: Camera[] = $state([])
  let summary: RecordingSummary[] = $state([])
  let loading = $state(true)
  let error: string | null = $state(null)

  let selectedCamera = $state('')
  let selectedDate = $state(toDateString(new Date()))
  let selectedHour: number | null = $state(null)
  let playlistUrl = $state('')

  // Hours 0–23, filled from summary data
  const HOURS = Array.from({ length: 24 }, (_, i) => i)

  // Build a quick lookup: "camera:date:hour" → duration
  const summaryMap = $derived((() => {
    const map = new Map<string, number>()
    for (const s of summary) {
      if (!selectedCamera || s.camera === selectedCamera) {
        const date = s.day
        // Duration is total seconds for the whole day — not per-hour, so we
        // mark every hour on that date as having data.
        for (let h = 0; h < 24; h++) {
          const key = `${s.camera}:${date}:${h}`
          map.set(key, (map.get(key) ?? 0) + s.duration / 24)
        }
      }
    }
    return map
  })())

  function hasRecording(hour: number): boolean {
    if (!selectedCamera) return false
    return summaryMap.has(`${selectedCamera}:${selectedDate}:${hour}`)
  }

  function selectHour(hour: number) {
    if (!selectedCamera || !hasRecording(hour)) return
    selectedHour = hour
    playlistUrl = vodPlaylistUrl(selectedDate, hour, selectedCamera)
  }

  function formatHour(h: number): string {
    const suffix = h < 12 ? 'AM' : 'PM'
    const h12 = h % 12 === 0 ? 12 : h % 12
    return `${h12}:00 ${suffix}`
  }

  async function load() {
    try {
      loading = true
      error = null
      const [cams, sums] = await Promise.all([getCameras(), getRecordingsSummary()])
      cameras = cams
      summary = sums
      if (cams.length > 0 && !selectedCamera) {
        selectedCamera = cams[0].name
      }
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load recordings'
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })

  // Clear selection when camera/date changes
  $effect(() => {
    selectedCamera
    selectedDate
    selectedHour = null
    playlistUrl = ''
  })
</script>

<div class="p-4 sm:p-6 max-w-screen-2xl mx-auto">
  <h1 class="text-xl font-semibold text-white mb-5">Recordings</h1>

  {#if error}
    <div class="flex flex-col items-center justify-center h-48 gap-3 text-gray-500">
      <p class="text-sm">{error}</p>
      <button class="btn-ghost" onclick={load}>Try again</button>
    </div>
  {:else}
    <!-- Controls -->
    <div class="card p-4 mb-5">
      <div class="flex flex-wrap gap-3">
        <!-- Camera select -->
        <div class="flex-1 min-w-[160px]">
          <label for="rec-camera" class="block text-xs text-gray-500 mb-1">Camera</label>
          <select id="rec-camera" class="select w-full" bind:value={selectedCamera}>
            <option value="">Select camera</option>
            {#each cameras as cam}
              <option value={cam.name}>{cam.name}</option>
            {/each}
          </select>
        </div>

        <!-- Date picker -->
        <div class="flex-1 min-w-[160px]">
          <label for="rec-date" class="block text-xs text-gray-500 mb-1">Date</label>
          <input id="rec-date" type="date" class="input w-full" bind:value={selectedDate} max={toDateString(new Date())} />
        </div>
      </div>
    </div>

    <div class="grid grid-cols-1 lg:grid-cols-3 gap-5">
      <!-- Hourly grid -->
      <div class="lg:col-span-1">
        <div class="card p-4">
          <h2 class="text-sm font-medium text-gray-300 mb-4">
            {selectedDate}
            {#if selectedCamera}— <span class="text-gray-400">{selectedCamera}</span>{/if}
          </h2>

          {#if loading}
            <div class="grid grid-cols-4 gap-1.5">
              {#each { length: 24 } as _}
                <div class="h-10 rounded-lg bg-surface-3 animate-pulse"></div>
              {/each}
            </div>
          {:else if !selectedCamera}
            <p class="text-sm text-gray-500 text-center py-8">Select a camera to view recordings</p>
          {:else}
            <!-- 4-column hour grid -->
            <div class="grid grid-cols-4 gap-1.5">
              {#each HOURS as hour}
                {@const has = hasRecording(hour)}
                {@const active = selectedHour === hour}
                <button
                  onclick={() => selectHour(hour)}
                  disabled={!has}
                  title={formatHour(hour)}
                  class="
                    h-12 rounded-lg text-xs font-medium flex flex-col items-center justify-center gap-0.5
                    transition-all duration-150
                    {active
                      ? 'bg-accent text-white shadow-lg shadow-accent/30'
                      : has
                        ? 'bg-blue-500/20 text-blue-400 hover:bg-blue-500/30 cursor-pointer border border-blue-500/30'
                        : 'bg-surface-3 text-gray-600 cursor-not-allowed'}
                  "
                >
                  <span>{hour % 12 === 0 ? 12 : hour % 12}</span>
                  <span class="text-[9px] opacity-70">{hour < 12 ? 'AM' : 'PM'}</span>
                </button>
              {/each}
            </div>

            <div class="flex items-center gap-4 mt-4 pt-3 border-t border-surface-3 text-xs text-gray-500">
              <span class="flex items-center gap-1.5">
                <span class="w-3 h-3 rounded bg-blue-500/30 border border-blue-500/30"></span>
                Has recordings
              </span>
              <span class="flex items-center gap-1.5">
                <span class="w-3 h-3 rounded bg-surface-3"></span>
                No recordings
              </span>
            </div>
          {/if}
        </div>
      </div>

      <!-- Video player panel -->
      <div class="lg:col-span-2">
        <div class="card p-4 h-full flex flex-col">
          {#if selectedHour !== null && playlistUrl}
            <div class="flex items-center justify-between mb-3">
              <h2 class="text-sm font-medium text-gray-300">
                {selectedCamera} — {selectedDate} {formatHour(selectedHour)}
              </h2>
              <button
                class="text-xs text-gray-500 hover:text-gray-300 transition-colors"
                onclick={() => { selectedHour = null; playlistUrl = '' }}
              >
                Close
              </button>
            </div>
            <VodPlayer
              playlistUrl={playlistUrl}
              class="flex-1 min-h-[200px]"
            />
          {:else}
            <div class="flex-1 flex flex-col items-center justify-center text-gray-500 gap-3 min-h-[200px]">
              <svg class="w-12 h-12 opacity-40" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1">
                <path stroke-linecap="round" stroke-linejoin="round" d="M7 4v16M17 4v16M3 8h4m10 0h4M3 12h18M3 16h4m10 0h4M4 20h16a1 1 0 001-1V5a1 1 0 00-1-1H4a1 1 0 00-1 1v14a1 1 0 001 1z" />
              </svg>
              <p class="text-sm">Select a time block to play recordings</p>
              <p class="text-xs text-gray-600">Blue blocks have recorded footage</p>
            </div>
          {/if}
        </div>
      </div>
    </div>
  {/if}
</div>
