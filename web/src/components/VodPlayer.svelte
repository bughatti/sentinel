<script lang="ts">
  import { authFetch } from '../lib/auth'
  /**
   * VOD (recording) player.
   *
   * The Recordings view produces an HLS VOD playlist URL (/vod/.../index.m3u8),
   * but the continuous recording segments are plain (non-fragmented) 10-second
   * MP4 files — every browser plays those natively, and only Safari plays raw
   * .m3u8. So instead of pulling in hls.js, this player fetches the playlist,
   * parses the ordered segment list, and plays the segment MP4s back-to-back in
   * a single <video>, auto-advancing on `ended`. A global scrubber lets you seek
   * across the whole hour (it maps a global time to the right segment + offset).
   *
   * No external libraries, works in Chrome/Edge/Firefox/Safari.
   */

  interface Props {
    playlistUrl: string
    class?: string
  }
  let { playlistUrl, class: className = '' }: Props = $props()

  type Seg = { url: string; dur: number; start: number }

  let videoEl: HTMLVideoElement | null = $state(null)
  let segments: Seg[] = $state([])
  let total = $state(0)
  let index = $state(0)
  let globalTime = $state(0) // seconds since start of the hour
  let playing = $state(false)
  let loading = $state(true)
  let error = $state(false)

  let ctrl: AbortController | null = null
  let tornDown = false
  let errStreak = 0 // consecutive segment load errors (stop skipping after a few)

  const absUrl = (u: string) => new URL(u, location.origin).href

  async function loadPlaylist(url: string) {
    teardown()
    tornDown = false
    loading = true
    error = false
    segments = []
    index = 0
    globalTime = 0
    total = 0

    ctrl = new AbortController()
    try {
      const res = await authFetch(url, { signal: ctrl.signal })
      if (!res.ok) throw new Error(`playlist ${res.status}`)
      const text = await res.text()

      const segs: Seg[] = []
      let dur = 10
      let acc = 0
      for (const raw of text.split('\n')) {
        const line = raw.trim()
        if (!line) continue
        if (line.startsWith('#EXTINF:')) {
          const n = parseFloat(line.slice(8))
          if (!isNaN(n) && n > 0) dur = n
          continue
        }
        if (line.startsWith('#')) continue
        segs.push({ url: line, dur, start: acc })
        acc += dur
        dur = 10
      }
      if (segs.length === 0) throw new Error('no segments')

      segments = segs
      total = acc
      loading = false
      setSegment(0, 0, true)
    } catch (e) {
      if ((e as any)?.name === 'AbortError') return
      loading = false
      error = true
    }
  }

  function setSegment(i: number, offset: number, autoplay: boolean) {
    if (!videoEl || i < 0 || i >= segments.length) return
    index = i
    const want = absUrl(segments[i].url)
    const apply = () => {
      if (!videoEl) return
      try { videoEl.currentTime = offset } catch {}
      if (autoplay) videoEl.play().catch(() => {})
    }
    if (videoEl.src !== want) {
      videoEl.src = segments[i].url
      videoEl.addEventListener('loadedmetadata', apply, { once: true })
    } else {
      apply()
    }
  }

  function seekGlobal(t: number) {
    t = Math.max(0, Math.min(t, total))
    let i = segments.findIndex((s) => t < s.start + s.dur)
    if (i < 0) i = segments.length - 1
    globalTime = t
    setSegment(i, Math.max(0, t - segments[i].start), playing)
  }

  function togglePlay() {
    if (!videoEl) return
    if (videoEl.paused) videoEl.play().catch(() => {})
    else videoEl.pause()
  }

  function onTimeUpdate() {
    if (!videoEl || !segments[index]) return
    errStreak = 0 // playing fine
    globalTime = segments[index].start + videoEl.currentTime
  }
  function onEnded() {
    if (tornDown) return
    if (index + 1 < segments.length) setSegment(index + 1, 0, true)
    else playing = false
  }
  function onError() {
    // Skip a missing/corrupt segment rather than stalling — but cap it, so a
    // gap (e.g. the tail of an in-progress hour whose playlist went stale)
    // can't cascade through the whole remainder to the end.
    if (tornDown) return
    errStreak++
    if (errStreak > 3 || index + 1 >= segments.length) {
      playing = false
      return
    }
    setSegment(index + 1, 0, true)
  }

  function fmt(s: number) {
    s = Math.max(0, Math.floor(s))
    const m = Math.floor(s / 60)
    const ss = s % 60
    return `${m}:${String(ss).padStart(2, '0')}`
  }

  function teardown() {
    tornDown = true
    ctrl?.abort()
    ctrl = null
    if (videoEl) {
      try { videoEl.pause() } catch {}
      videoEl.removeAttribute('src')
      try { videoEl.load() } catch {}
    }
  }

  $effect(() => {
    const url = playlistUrl
    const el = videoEl
    if (url && el) loadPlaylist(url)
    return teardown
  })
</script>

<div class="relative bg-black rounded-lg overflow-hidden flex flex-col {className}">
  <div class="relative flex-1 min-h-0">
    {#if loading}
      <div class="absolute inset-0 flex items-center justify-center bg-black/60 z-10">
        <div class="flex flex-col items-center gap-2 text-gray-400">
          <svg class="w-8 h-8 animate-spin" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z" />
          </svg>
          <span class="text-xs">Loading recording…</span>
        </div>
      </div>
    {/if}

    {#if error}
      <div class="absolute inset-0 flex items-center justify-center bg-black/80 z-10">
        <div class="text-center text-gray-400 p-4">
          <svg class="w-8 h-8 mx-auto mb-2 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
          </svg>
          <p class="text-sm">Recording unavailable</p>
        </div>
      </div>
    {/if}

    <!-- svelte-ignore a11y_media_has_caption -->
    <video
      bind:this={videoEl}
      class="w-full h-full bg-black"
      playsinline
      preload="auto"
      ontimeupdate={onTimeUpdate}
      onended={onEnded}
      onerror={onError}
      onplay={() => (playing = true)}
      onpause={() => (playing = false)}
    ></video>
  </div>

  {#if segments.length > 0 && !error}
    <div class="flex items-center gap-3 px-3 py-2 bg-surface-2 border-t border-surface-3">
      <button
        onclick={togglePlay}
        class="text-gray-300 hover:text-white transition-colors shrink-0"
        aria-label={playing ? 'Pause' : 'Play'}
      >
        {#if playing}
          <svg class="w-5 h-5" fill="currentColor" viewBox="0 0 24 24"><path d="M6 5h4v14H6zM14 5h4v14h-4z" /></svg>
        {:else}
          <svg class="w-5 h-5" fill="currentColor" viewBox="0 0 24 24"><path d="M8 5v14l11-7z" /></svg>
        {/if}
      </button>
      <span class="text-xs text-gray-400 tabular-nums shrink-0">{fmt(globalTime)}</span>
      <input
        type="range"
        min="0"
        max={total}
        step="1"
        value={globalTime}
        oninput={(e) => seekGlobal(+e.currentTarget.value)}
        class="flex-1 accent-accent cursor-pointer"
        aria-label="Seek"
      />
      <span class="text-xs text-gray-400 tabular-nums shrink-0">{fmt(total)}</span>
    </div>
  {/if}
</div>
