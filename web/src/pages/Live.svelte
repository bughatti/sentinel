<script lang="ts">
  import type { Camera } from '../lib/api'
  import { getCameras, vodPlaylistUrl } from '../lib/api'
  import type { WSMessage } from '../lib/ws'
  import { ws } from '../lib/ws'
  import CameraCard from '../components/CameraCard.svelte'
  import VideoPlayer from '../components/VideoPlayer.svelte'

  // Camera state
  let cameras: Camera[] = $state([])
  let loading = $state(true)
  let error: string | null = $state(null)

  // Per-camera motion/detection state (from WS)
  let motionCameras = $state(new Set<string>())
  let detectionCameras = $state(new Set<string>())

  // Fullscreen modal
  let fullscreenCamera: Camera | null = $state(null)

  // go2rtc live HLS URL
  function liveHlsUrl(cameraName: string): string {
    const host = window.location.hostname
    return `http://${host}:1984/api/stream.mp4?src=${encodeURIComponent(cameraName)}`
  }

  async function load() {
    try {
      loading = true
      error = null
      cameras = await getCameras()
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load cameras'
    } finally {
      loading = false
    }
  }

  function openFullscreen(camera: Camera) {
    fullscreenCamera = camera
  }

  function closeFullscreen() {
    fullscreenCamera = null
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') closeFullscreen()
  }

  // Listen for WS motion/detection events
  function handleWS(msg: WSMessage) {
    if (!msg.payload) return
    const cam = msg.payload.camera
    if (msg.type === 'new' || msg.type === 'update') {
      detectionCameras = new Set([...detectionCameras, cam])
    } else if (msg.type === 'end') {
      detectionCameras = new Set([...detectionCameras].filter(c => c !== cam))
    }
  }

  $effect(() => {
    load()
    const unsub = ws.onMessage(handleWS)
    return unsub
  })
</script>

<svelte:window onkeydown={handleKeydown} />

<div class="p-4 sm:p-6 max-w-screen-2xl mx-auto">
  <div class="flex items-center justify-between mb-5">
    <div>
      <h1 class="text-xl font-semibold text-white">Live Cameras</h1>
      {#if !loading}
        <p class="text-sm text-gray-500 mt-0.5">{cameras.length} camera{cameras.length !== 1 ? 's' : ''}</p>
      {/if}
    </div>
    <button
      class="btn-ghost text-xs"
      onclick={load}
      disabled={loading}
    >
      <svg class="w-3.5 h-3.5 {loading ? 'animate-spin' : ''}" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
      </svg>
      Refresh
    </button>
  </div>

  {#if loading}
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
      {#each { length: 4 } as _}
        <div class="card animate-pulse">
          <div class="aspect-video bg-surface-3 rounded-t-xl"></div>
          <div class="px-3 py-2">
            <div class="h-3 bg-surface-3 rounded w-2/3"></div>
          </div>
        </div>
      {/each}
    </div>
  {:else if error}
    <div class="flex flex-col items-center justify-center h-48 gap-3 text-gray-500">
      <svg class="w-10 h-10" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
        <path stroke-linecap="round" stroke-linejoin="round" d="M12 9v3.75m9-.75a9 9 0 11-18 0 9 9 0 0118 0zm-9 3.75h.008v.008H12v-.008z" />
      </svg>
      <p class="text-sm">{error}</p>
      <button class="btn-ghost" onclick={load}>Try again</button>
    </div>
  {:else if cameras.length === 0}
    <div class="flex flex-col items-center justify-center h-48 gap-3 text-gray-500">
      <svg class="w-10 h-10" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
        <path stroke-linecap="round" stroke-linejoin="round" d="M15 10l4.553-2.069A1 1 0 0121 8.868V15.132a1 1 0 01-1.447.9L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
      </svg>
      <p class="text-sm">No cameras configured</p>
    </div>
  {:else}
    <div class="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-3 xl:grid-cols-4 gap-4">
      {#each cameras as camera (camera.name)}
        <CameraCard
          {camera}
          hasMotion={motionCameras.has(camera.name)}
          hasDetection={detectionCameras.has(camera.name)}
          onFullscreen={openFullscreen}
        />
      {/each}
    </div>
  {/if}
</div>

<!-- Fullscreen modal -->
{#if fullscreenCamera}
  <div
    class="fixed inset-0 z-50 bg-black/90 backdrop-blur-sm flex flex-col"
    onclick={(e) => e.target === e.currentTarget && closeFullscreen()}
    onkeydown={(e) => e.key === 'Escape' && closeFullscreen()}
    role="dialog"
    tabindex="-1"
    aria-modal="true"
    aria-label="{fullscreenCamera.name} live stream"
  >
    <!-- Modal header -->
    <div class="flex items-center justify-between px-4 py-3 bg-surface-1/80 border-b border-surface-3">
      <div class="flex items-center gap-3">
        <span class="badge px-2 py-0.5 text-[10px] bg-green-500/20 text-green-400">LIVE</span>
        <h2 class="font-semibold text-white">{fullscreenCamera.name}</h2>
      </div>
      <button
        onclick={closeFullscreen}
        aria-label="Close fullscreen"
        class="w-8 h-8 flex items-center justify-center rounded-lg hover:bg-surface-3 text-gray-400 hover:text-white transition-colors"
      >
        <svg class="w-5 h-5" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M6 18L18 6M6 6l12 12" />
        </svg>
      </button>
    </div>

    <!-- Video -->
    <div class="flex-1 flex items-center justify-center p-4 min-h-0">
      <div class="w-full max-w-5xl">
        <VideoPlayer
          src={liveHlsUrl(fullscreenCamera.name)}
          autoplay={true}
          muted={true}
          class="w-full aspect-video"
        />
      </div>
    </div>

    <p class="text-center text-xs text-gray-500 pb-3">
      Press <kbd class="px-1.5 py-0.5 rounded bg-surface-3 text-gray-300 text-[10px] font-mono">Esc</kbd> to close
    </p>
  </div>
{/if}
