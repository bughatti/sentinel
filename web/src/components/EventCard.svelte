<script lang="ts">
  import type { SentinelEvent, Recording } from '../lib/api'
  import { snapshotUrl, clipUrl, latestFrameUrl, getRecordings, recordingVideoUrl } from '../lib/api'
  import { timeAgo, absoluteTime, formatScore } from '../lib/utils'
  import EventBadge from './EventBadge.svelte'

  interface Props {
    event: SentinelEvent
    expanded?: boolean
    onExpand?: (id: string) => void
  }

  let { event, expanded = false, onExpand }: Props = $props()

  let imgError = $state(false)
  let recording = $state<Recording | null>(null)
  let recordingLoading = $state(false)

  $effect(() => {
    if (expanded && !event.has_clip && !event.has_snapshot) {
      recordingLoading = true
      recording = null
      const after = event.start_time - 30
      const before = (event.end_time ?? event.start_time) + 30
      getRecordings({ camera: event.camera, after, before, limit: 5 })
        .then(recs => { recording = recs[0] ?? null })
        .catch(() => { recording = null })
        .finally(() => { recordingLoading = false })
    }
  })

  function handleClick() {
    onExpand?.(expanded ? '' : event.id)
  }

  function handleKeydown(e: KeyboardEvent) {
    if (e.key === 'Enter' || e.key === ' ') handleClick()
  }
</script>

<div class="animate-fade-in">
  <!-- Main row -->
  <div
    class="flex items-start gap-3 p-3 rounded-xl border transition-all duration-150 cursor-pointer
           {expanded
             ? 'bg-surface-2 border-accent/40'
             : 'bg-surface-1 border-surface-3 hover:bg-surface-2 hover:border-surface-4'}"
    onclick={handleClick}
    role="button"
    tabindex="0"
    onkeydown={handleKeydown}
  >
    <!-- Snapshot thumbnail — prefer event snapshot, fall back to camera live preview -->
    <div class="w-20 h-14 sm:w-28 sm:h-20 shrink-0 rounded-lg overflow-hidden bg-surface-3">
      {#if !imgError}
        <img
          src={event.has_snapshot ? snapshotUrl(event.id) : latestFrameUrl(event.camera)}
          alt="{event.label} on {event.camera}"
          class="w-full h-full object-cover"
          onerror={() => (imgError = true)}
          loading="lazy"
        />
      {:else}
        <div class="w-full h-full flex items-center justify-center text-gray-600">
          <svg class="w-6 h-6" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.5">
            <path stroke-linecap="round" stroke-linejoin="round" d="M2.25 15.75l5.159-5.159a2.25 2.25 0 013.182 0l5.159 5.159m-1.5-1.5l1.409-1.409a2.25 2.25 0 013.182 0l2.909 2.909m-18 3.75h16.5a1.5 1.5 0 001.5-1.5V6a1.5 1.5 0 00-1.5-1.5H3.75A1.5 1.5 0 002.25 6v12a1.5 1.5 0 001.5 1.5zm10.5-11.25h.008v.008h-.008V8.25zm.375 0a.375.375 0 11-.75 0 .375.375 0 01.75 0z" />
          </svg>
        </div>
      {/if}
    </div>

    <!-- Info -->
    <div class="flex-1 min-w-0">
      <div class="flex flex-wrap items-center gap-2 mb-1">
        <EventBadge label={event.label} />
        {#if event.false_positive}
          <span class="badge px-1.5 py-0.5 text-[10px] bg-gray-500/20 text-gray-400">FALSE POSITIVE</span>
        {/if}
        {#if event.retain_indefinitely}
          <span class="badge px-1.5 py-0.5 text-[10px] bg-amber-500/20 text-amber-400">RETAINED</span>
        {/if}
      </div>

      <div class="flex flex-wrap items-center gap-x-3 gap-y-0.5 text-xs text-gray-400">
        <span class="font-medium text-gray-300 truncate">{event.camera}</span>
        <span title={absoluteTime(event.start_time)}>{timeAgo(event.start_time)}</span>
        <span class="text-gray-500">Score: {formatScore(event.top_score)}</span>
      </div>

      {#if event.current_zones?.length > 0}
        <div class="flex flex-wrap gap-1 mt-1.5">
          {#each event.current_zones as zone}
            <span class="badge px-1.5 py-0.5 text-[10px] bg-surface-4 text-gray-400">{zone}</span>
          {/each}
        </div>
      {/if}
    </div>

    <!-- Right side: media indicators + chevron -->
    <div class="shrink-0 flex items-center gap-2 text-gray-500">
      {#if event.has_clip}
        <svg class="w-4 h-4 text-gray-400" title="Has clip" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15.75 10.5l4.72-4.72a.75.75 0 011.28.53v11.38a.75.75 0 01-1.28.53l-4.72-4.72M4.5 18.75h9a2.25 2.25 0 002.25-2.25v-9a2.25 2.25 0 00-2.25-2.25h-9A2.25 2.25 0 002.25 7.5v9a2.25 2.25 0 002.25 2.25z" />
        </svg>
      {/if}
      <svg
        class="w-4 h-4 transition-transform duration-200 {expanded ? 'rotate-180' : ''}"
        fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2"
      >
        <path stroke-linecap="round" stroke-linejoin="round" d="M19 9l-7 7-7-7" />
      </svg>
    </div>
  </div>

  <!-- Expanded panel -->
  {#if expanded}
    <div class="mt-1 mb-2 ml-3 mr-0 rounded-xl bg-surface-2 border border-surface-3 overflow-hidden animate-fade-in">
      <div class="p-3 grid grid-cols-1 sm:grid-cols-2 gap-3">
        <!-- Full snapshot -->
        {#if event.has_snapshot}
          <div class="rounded-lg overflow-hidden bg-surface-3">
            <img
              src={snapshotUrl(event.id)}
              alt="{event.label} snapshot"
              class="w-full object-contain max-h-64"
            />
          </div>
        {/if}

        <!-- Clip player — plain MP4 served by the API, no go2rtc involved -->
        {#if event.has_clip}
          <!-- svelte-ignore a11y_media_has_caption -->
          <video
            src={clipUrl(event.id)}
            controls
            class="w-full max-h-64 rounded-lg bg-black"
          ></video>
        {:else if !event.has_snapshot}
          <!-- Motion events: play recorded segment covering the event time -->
          {#if recordingLoading}
            <div class="w-full max-h-64 rounded-lg bg-surface-3 flex items-center justify-center h-40 text-gray-500 text-sm">
              Loading recording…
            </div>
          {:else if recording && recordingVideoUrl(recording.path)}
            <!-- svelte-ignore a11y_media_has_caption -->
            <video
              src={recordingVideoUrl(recording.path)}
              controls
              class="w-full max-h-64 rounded-lg bg-black"
            ></video>
          {:else}
            <div class="w-full max-h-64 rounded-lg bg-surface-3 flex items-center justify-center h-40 text-gray-500 text-sm">
              No recording available
            </div>
          {/if}
        {/if}
      </div>

      <!-- Event metadata -->
      <div class="px-3 pb-3 grid grid-cols-2 sm:grid-cols-3 gap-2 text-xs text-gray-400">
        <div>
          <p class="text-gray-500 mb-0.5">ID</p>
          <p class="text-gray-300 font-mono text-[10px] truncate" title={event.id}>{event.id}</p>
        </div>
        <div>
          <p class="text-gray-500 mb-0.5">Started</p>
          <p class="text-gray-300">{absoluteTime(event.start_time)}</p>
        </div>
        {#if event.end_time}
          <div>
            <p class="text-gray-500 mb-0.5">Duration</p>
            <p class="text-gray-300">{((event.end_time - event.start_time)).toFixed(1)}s</p>
          </div>
        {/if}
        {#if event.detector_type}
          <div>
            <p class="text-gray-500 mb-0.5">Detector</p>
            <p class="text-gray-300">{event.detector_type}</p>
          </div>
        {/if}
        <div>
          <p class="text-gray-500 mb-0.5">Score</p>
          <p class="text-gray-300">{formatScore(event.score)} (top: {formatScore(event.top_score)})</p>
        </div>
      </div>
    </div>
  {/if}
</div>
