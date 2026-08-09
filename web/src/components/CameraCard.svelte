<script lang="ts">
  import type { Camera } from '../lib/api'
  import { latestFrameUrl } from '../lib/api'

  interface Props {
    camera: Camera
    hasMotion?: boolean
    hasDetection?: boolean
    onFullscreen?: (camera: Camera) => void
  }

  let { camera, hasMotion = false, hasDetection = false, onFullscreen }: Props = $props()

  let imgError = $state(false)
  let tick = $state(Date.now())

  // Refresh thumbnail every 5 seconds
  $effect(() => {
    const id = setInterval(() => { tick = Date.now() }, 5000)
    return () => clearInterval(id)
  })

  function handleClick() {
    onFullscreen?.(camera)
  }
</script>

<div
  class="card group cursor-pointer hover:border-accent/50 transition-all duration-200 hover:shadow-lg hover:shadow-black/40"
  onclick={handleClick}
  role="button"
  tabindex="0"
  onkeydown={(e) => e.key === 'Enter' && handleClick()}
  title="Click to view live stream"
>
  <!-- Thumbnail preview — static JPEG refreshed every 5s, live stream loads on click -->
  <div class="relative aspect-video bg-surface-3 overflow-hidden rounded-t-xl">
    {#if !imgError}
      <img
        src="{latestFrameUrl(camera.name)}?t={Math.floor(tick / 5000)}"
        alt="{camera.name} preview"
        class="absolute inset-0 w-full h-full object-cover"
        onerror={() => (imgError = true)}
      />
    {:else}
      <div class="absolute inset-0 flex items-center justify-center text-gray-600">
        <svg class="w-10 h-10" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 10.5l4.72-4.72a.75.75 0 011.28.53v11.38a.75.75 0 01-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 002.25-2.25v-9a2.25 2.25 0 00-2.25-2.25h-9A2.25 2.25 0 002.25 7.5v9a2.25 2.25 0 002.25 2.25z" />
        </svg>
      </div>
    {/if}

    <!-- Play icon on hover -->
    <div class="absolute inset-0 flex items-center justify-center opacity-0 group-hover:opacity-100 transition-opacity z-20 pointer-events-none">
      <div class="w-12 h-12 bg-black/60 rounded-full flex items-center justify-center backdrop-blur-sm">
        <svg class="w-6 h-6 text-white ml-1" fill="currentColor" viewBox="0 0 24 24">
          <path d="M8 5v14l11-7z"/>
        </svg>
      </div>
    </div>

    <!-- Overlay gradient -->
    <div class="absolute inset-0 bg-gradient-to-t from-black/60 via-transparent to-transparent pointer-events-none z-10"></div>

    <!-- Status badges -->
    <div class="absolute top-2 left-2 flex gap-1.5 z-30">
      {#if !camera.online}
        <span class="badge px-2 py-0.5 text-[10px] bg-red-500/80 text-white">OFFLINE</span>
      {:else if hasDetection}
        <span class="badge px-2 py-0.5 text-[10px] bg-blue-500/80 text-white animate-pulse-fast">DETECTING</span>
      {:else if hasMotion}
        <span class="badge px-2 py-0.5 text-[10px] bg-yellow-500/80 text-black">MOTION</span>
      {:else}
        <span class="badge px-2 py-0.5 text-[10px] bg-green-500/20 text-green-400">LIVE</span>
      {/if}
    </div>

    <!-- Expand icon -->
    <div class="absolute top-2 right-2 opacity-0 group-hover:opacity-100 transition-opacity z-30">
      <div class="w-7 h-7 bg-black/60 rounded-md flex items-center justify-center backdrop-blur-sm">
        <svg class="w-3.5 h-3.5 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M4 8V4m0 0h4M4 4l5 5m11-1V4m0 0h-4m4 0l-5 5M4 16v4m0 0h4m-4 0l5-5m11 5l-5-5m5 5v-4m0 4h-4" />
        </svg>
      </div>
    </div>

    <!-- Camera name overlay -->
    <div class="absolute bottom-2 left-2 right-2 z-30">
      <p class="text-white text-sm font-semibold drop-shadow-md truncate">{camera.name}</p>
    </div>
  </div>

  <!-- Card footer -->
  <div class="px-3 py-2 flex items-center justify-between text-xs text-gray-500">
    <span class="flex items-center gap-1">
      <svg class="w-3 h-3" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <circle cx="12" cy="12" r="10"/>
      </svg>
      {camera.detect_width > 0 ? `${camera.detect_width}×${camera.detect_height}` : '—'}
    </span>
    <span>{camera.detect_fps > 0 ? `${camera.detect_fps} FPS` : '—'}</span>
  </div>
</div>
