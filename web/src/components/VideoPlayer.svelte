<script lang="ts">
  /**
   * Live stream player using go2rtc's MSE WebSocket endpoint.
   *
   * Connects to ws://host:1984/api/ws?src=NAME, receives continuous fMP4 chunks,
   * and feeds them into the browser's MediaSource API.  No HLS sessions, no
   * segment polling, no keepalive timers, no external libraries.
   *
   * Codec is auto-detected from the MP4 initialization segment.
   */

  interface Props {
    src: string
    autoplay?: boolean
    controls?: boolean
    muted?: boolean
    class?: string
  }

  let { src, autoplay = true, controls = true, muted = true, class: className = '' }: Props = $props()

  let videoEl: HTMLVideoElement | null = $state(null)
  let loading = $state(true)
  let unavailable = $state(false)

  let ctrl: AbortController | null = null
  let retryTimer: ReturnType<typeof setTimeout> | null = null

  // ── MP4 box parsing ──────────────────────────────────────────────────────

  function u32(b: Uint8Array, o: number): number {
    return ((b[o] << 24) | (b[o + 1] << 16) | (b[o + 2] << 8) | b[o + 3]) >>> 0
  }

  function boxTag(b: Uint8Array, o: number): string {
    return String.fromCharCode(b[o], b[o + 1], b[o + 2], b[o + 3])
  }

  /** Return content of the first matching top-level box (strips 8-byte header). */
  function findBox(data: Uint8Array, type: string): Uint8Array | null {
    let o = 0
    while (o + 8 <= data.length) {
      const size = u32(data, o)
      if (size < 8 || o + size > data.length) break
      if (boxTag(data, o + 4) === type) return data.subarray(o + 8, o + size)
      o += size
    }
    return null
  }

  /** Return the byte offset of the first matching top-level box, or -1. */
  function findBoxAt(data: Uint8Array, type: string): number {
    let o = 0
    while (o + 8 <= data.length) {
      const size = u32(data, o)
      if (size < 8) break
      if (boxTag(data, o + 4) === type) return o
      o += size
    }
    return -1
  }

  /**
   * Walk moov → trak → mdia → minf → stbl → stsd → avc1 → avcC to read the
   * H.264 profile/level bytes and return the RFC 6381 codec string.
   */
  function extractVideoCodec(init: Uint8Array): string {
    let box: Uint8Array | null = findBox(init, 'moov')
    for (const name of ['trak', 'mdia', 'minf', 'stbl']) {
      box = box ? findBox(box, name) : null
    }
    const stsd = box ? findBox(box, 'stsd') : null

    // stsd: version(1)+flags(3)+entry_count(4) = 8 bytes, then first entry
    if (!stsd || stsd.length < 20) return 'avc1.640029'

    const entry = boxTag(stsd, 12)

    if (entry === 'avc1' || entry === 'avc3') {
      // SampleEntry(6+2) + VisualSampleEntry(70) = 78 bytes before nested boxes
      const avcC = findBox(stsd.subarray(16 + 78), 'avcC')
      if (avcC && avcC.length >= 4) {
        const h = (n: number) => n.toString(16).padStart(2, '0')
        return `avc1.${h(avcC[1])}${h(avcC[2])}${h(avcC[3])}`
      }
      return 'avc1.640029'
    }

    if (entry === 'hev1' || entry === 'hvc1') return 'hev1.1.6.L93.B0'

    return 'avc1.640029'
  }

  // ── Streaming ────────────────────────────────────────────────────────────

  /**
   * Open one WebSocket connection to go2rtc's MSE endpoint and pipe fMP4
   * data into a MediaSource.  Resolves when the stream ends for any reason.
   *
   * go2rtc protocol:
   *   1. Client → JSON: {"type":"mse","value":["video/mp4; codecs=\"...\"", ...]}
   *   2. Server → JSON: {"type":"mse","value":"video/mp4; codecs=\"...\""}  (accepted codec)
   *   3. Server → ArrayBuffer: continuous fMP4 — init segment first, then media segments
   */
  async function runStream(el: HTMLVideoElement, url: string, signal: AbortSignal): Promise<void> {
    const ms = new MediaSource()
    const msUrl = URL.createObjectURL(ms)
    el.src = msUrl

    try {
      await new Promise<void>((resolve, reject) => {
        ms.addEventListener('sourceopen', () => resolve(), { once: true })
        signal.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')), { once: true })
      })
      if (signal.aborted) return

      // Convert http(s)://host:port/api/stream.mp4?src=NAME
      //      to   ws(s)://host:port/api/ws?src=NAME
      const wsUrl = new URL(url)
      wsUrl.protocol = wsUrl.protocol === 'https:' ? 'wss:' : 'ws:'
      wsUrl.pathname = '/api/ws'

      const ws = new WebSocket(wsUrl.toString())
      ws.binaryType = 'arraybuffer'
      signal.addEventListener('abort', () => ws.close(), { once: true })

      let sb: SourceBuffer | null = null
      let initAccum = new Uint8Array(0)
      const pending: Uint8Array[] = []
      let flushing = false

      // Cancel the stream if go2rtc sends no data within 10 s (offline camera).
      let stallTimer: ReturnType<typeof setTimeout> | null = setTimeout(() => ws.close(), 10_000)
      const clearStall = () => { if (stallTimer !== null) { clearTimeout(stallTimer); stallTimer = null } }

      const flush = () => {
        if (flushing || !sb || sb.updating || pending.length === 0) return
        flushing = true
        try { sb.appendBuffer(pending.shift()!) } catch { flushing = false }
      }

      const onUpdateEnd = () => {
        flushing = false
        try {
          if (sb && sb.buffered.length > 0) {
            const trim = el.currentTime - 30
            if (trim > 0 && trim > sb.buffered.start(0)) {
              try { sb.remove(0, trim) } catch {}
              return
            }
          }
        } catch {}
        flush()
      }

      ws.onopen = () => {
        ws.send(JSON.stringify({
          type: 'mse',
          value: [
            'video/mp4; codecs="avc1.640029,mp4a.40.2"',
            'video/mp4; codecs="avc1.640029"',
          ],
        }))
      }

      await new Promise<void>(resolve => {
        ws.onclose = () => resolve()
        ws.onerror = () => resolve()

        ws.onmessage = (ev) => {
          // String messages are JSON control frames (codec negotiation etc.) — ignore.
          if (typeof ev.data === 'string') return

          clearStall()

          const value = new Uint8Array(ev.data as ArrayBuffer)

          if (sb) {
            pending.push(value)
            flush()
          } else {
            // Accumulate chunks until we see the first moof box — everything
            // before it is the fMP4 initialization segment (ftyp + moov).
            const merged = new Uint8Array(initAccum.length + value.length)
            merged.set(initAccum)
            merged.set(value, initAccum.length)
            initAccum = merged

            const moofAt = findBoxAt(initAccum, 'moof')
            if (moofAt < 0) return

            const initSeg = initAccum.subarray(0, moofAt)
            const videoCodec = extractVideoCodec(initSeg)
            const withAudio = `video/mp4; codecs="${videoCodec},mp4a.40.2"`
            const videoOnly = `video/mp4; codecs="${videoCodec}"`

            sb = ms.addSourceBuffer(MediaSource.isTypeSupported(withAudio) ? withAudio : videoOnly)
            sb.mode = 'sequence'
            sb.addEventListener('updateend', onUpdateEnd)

            pending.push(initSeg, initAccum.subarray(moofAt))
            flush()

            unavailable = false
            loading = false
            if (autoplay) el.play().catch(() => {})
          }
        }
      })

      clearStall()
    } catch {
      // AbortError on signal abort — expected.
    } finally {
      URL.revokeObjectURL(msUrl)
      if (ms.readyState === 'open') try { ms.endOfStream() } catch {}
    }
  }

  function stop() {
    if (retryTimer !== null) { clearTimeout(retryTimer); retryTimer = null }
    ctrl?.abort()
    ctrl = null
  }

  async function connect(el: HTMLVideoElement, url: string) {
    stop()
    loading = true
    unavailable = false

    ctrl = new AbortController()
    const { signal } = ctrl

    let hasPlayed = false
    let delay = 2_000
    let attempts = 0

    while (!signal.aborted) {
      await runStream(el, url, signal)
      if (signal.aborted) break

      if (!hasPlayed) {
        hasPlayed = !loading
      }

      attempts++

      if (!hasPlayed && attempts >= 3) {
        unavailable = true
        loading = false
        delay = 30_000
      } else if (hasPlayed) {
        loading = true
        delay = 2_000
      }

      await new Promise<void>(resolve => {
        retryTimer = setTimeout(resolve, delay)
        signal.addEventListener('abort', resolve, { once: true })
      })
      delay = Math.min(delay * 2, 30_000)
    }
  }

  $effect(() => {
    const el = videoEl
    const url = src
    if (el && url) connect(el, url)
    return stop
  })
</script>

<div class="relative bg-black rounded-lg overflow-hidden {className}">
  {#if loading}
    <div class="absolute inset-0 flex items-center justify-center bg-black/60 z-10">
      <div class="flex flex-col items-center gap-2 text-gray-400">
        <svg class="w-8 h-8 animate-spin" fill="none" viewBox="0 0 24 24">
          <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4"/>
          <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4z"/>
        </svg>
        <span class="text-xs">Loading stream…</span>
      </div>
    </div>
  {/if}

  {#if unavailable}
    <div class="absolute inset-0 flex items-center justify-center bg-black/80 z-10">
      <div class="text-center text-gray-400 p-4">
        <svg class="w-8 h-8 mx-auto mb-2 text-red-400" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M12 8v4m0 4h.01M21 12a9 9 0 11-18 0 9 9 0 0118 0z" />
        </svg>
        <p class="text-sm">Stream unavailable</p>
      </div>
    </div>
  {/if}

  <!-- svelte-ignore a11y_media_has_caption -->
  <video
    bind:this={videoEl}
    {controls}
    {muted}
    playsinline
    class="w-full h-full"
  ></video>
</div>
