<script lang="ts">
  import { authFetch } from '../lib/auth'
  import type { Stats } from '../lib/api'
  import { getStats } from '../lib/api'
  import { formatDuration, formatBytes } from '../lib/utils'

  let stats: Stats | null = $state(null)
  let config: unknown = $state(null)
  let loading = $state(true)
  let error: string | null = $state(null)

  async function load() {
    try {
      loading = true
      error = null
      const [s, c] = await Promise.all([
        getStats(),
        authFetch('/api/config').then(r => r.json()),
      ])
      stats = s
      config = c
    } catch (e) {
      error = e instanceof Error ? e.message : 'Failed to load settings'
    } finally {
      loading = false
    }
  }

  $effect(() => {
    load()
  })
</script>

<div class="p-4 sm:p-6 max-w-screen-2xl mx-auto">
  <div class="flex items-center justify-between mb-5">
    <h1 class="text-xl font-semibold text-white">Settings</h1>
    <button class="btn-ghost text-xs" onclick={load} disabled={loading}>
      <svg class="w-3.5 h-3.5 {loading ? 'animate-spin' : ''}" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
        <path stroke-linecap="round" stroke-linejoin="round" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
      </svg>
      Refresh
    </button>
  </div>

  {#if loading}
    <div class="space-y-4">
      {#each { length: 3 } as _}
        <div class="card p-4 animate-pulse space-y-2">
          <div class="h-4 bg-surface-3 rounded w-1/4"></div>
          <div class="h-3 bg-surface-3 rounded w-1/2"></div>
          <div class="h-3 bg-surface-3 rounded w-1/3"></div>
        </div>
      {/each}
    </div>
  {:else if error}
    <div class="flex flex-col items-center justify-center h-48 gap-3 text-gray-500">
      <p class="text-sm">{error}</p>
      <button class="btn-ghost" onclick={load}>Try again</button>
    </div>
  {:else}
    <div class="grid grid-cols-1 md:grid-cols-2 gap-4">

      <!-- Service Info -->
      {#if stats}
        <div class="card p-4">
          <h2 class="text-sm font-semibold text-gray-200 mb-3 flex items-center gap-2">
            <svg class="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <path stroke-linecap="round" stroke-linejoin="round" d="M5 12h14M12 5l7 7-7 7" />
            </svg>
            Service
          </h2>
          <dl class="space-y-2">
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Version</dt>
              <dd class="text-gray-200 font-mono">{stats.service.version || 'dev'}</dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Uptime</dt>
              <dd class="text-gray-200">{formatDuration(stats.service.uptime)}</dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Go version</dt>
              <dd class="text-gray-200 font-mono">{stats.service.go_version}</dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">CPU cores</dt>
              <dd class="text-gray-200">{stats.service.num_cpu}</dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Goroutines</dt>
              <dd class="text-gray-200">{stats.service.num_goroutines}</dd>
            </div>
          </dl>
        </div>

        <!-- Detection stats -->
        <div class="card p-4">
          <h2 class="text-sm font-semibold text-gray-200 mb-3 flex items-center gap-2">
            <svg class="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
              <circle cx="11" cy="11" r="8"/>
              <path stroke-linecap="round" stroke-linejoin="round" d="M21 21l-4.35-4.35"/>
            </svg>
            Detection
          </h2>
          <dl class="space-y-2">
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Status</dt>
              <dd>
                {#if stats.detection.detection_enabled}
                  <span class="badge px-2 py-0.5 text-[10px] bg-green-500/20 text-green-400">ENABLED</span>
                {:else}
                  <span class="badge px-2 py-0.5 text-[10px] bg-red-500/20 text-red-400">DISABLED</span>
                {/if}
              </dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Detection FPS</dt>
              <dd class="text-gray-200">{stats.detection.detection_fps.toFixed(1)}</dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Avg inference</dt>
              <dd class="text-gray-200">
                {stats.detection.avg_inference_speed > 0
                  ? `${stats.detection.avg_inference_speed.toFixed(1)} ms`
                  : '—'}
              </dd>
            </div>
            <div class="flex justify-between text-sm">
              <dt class="text-gray-500">Total frames</dt>
              <dd class="text-gray-200">{stats.detection.total_frames.toLocaleString()}</dd>
            </div>
          </dl>
        </div>

        <!-- Camera FPS table -->
        {#if Object.keys(stats.cameras).length > 0}
          <div class="card p-4 md:col-span-2">
            <h2 class="text-sm font-semibold text-gray-200 mb-3 flex items-center gap-2">
              <svg class="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
                <path stroke-linecap="round" stroke-linejoin="round" d="M15 10l4.553-2.069A1 1 0 0121 8.868V15.132a1 1 0 01-1.447.9L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
              </svg>
              Camera Performance
            </h2>
            <div class="overflow-x-auto">
              <table class="w-full text-sm">
                <thead>
                  <tr class="text-xs text-gray-500 border-b border-surface-3">
                    <th class="text-left pb-2 pr-4 font-medium">Camera</th>
                    <th class="text-right pb-2 px-4 font-medium">Capture FPS</th>
                    <th class="text-right pb-2 px-4 font-medium">Detect FPS</th>
                    <th class="text-right pb-2 px-4 font-medium">Process FPS</th>
                    <th class="text-right pb-2 pl-4 font-medium">Skipped FPS</th>
                  </tr>
                </thead>
                <tbody class="divide-y divide-surface-3">
                  {#each Object.entries(stats.cameras) as [name, cam]}
                    <tr class="text-gray-300">
                      <td class="py-2 pr-4 font-medium">{name}</td>
                      <td class="py-2 px-4 text-right tabular-nums">{cam.camera_fps.toFixed(1)}</td>
                      <td class="py-2 px-4 text-right tabular-nums">{cam.detect_fps.toFixed(1)}</td>
                      <td class="py-2 px-4 text-right tabular-nums">{cam.process_fps.toFixed(1)}</td>
                      <td class="py-2 pl-4 text-right tabular-nums text-yellow-400">
                        {cam.skipped_fps > 0 ? cam.skipped_fps.toFixed(1) : '—'}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          </div>
        {/if}
      {/if}

      <!-- Config dump -->
      <div class="card p-4 md:col-span-2">
        <h2 class="text-sm font-semibold text-gray-200 mb-3 flex items-center gap-2">
          <svg class="w-4 h-4 text-accent" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
            <path stroke-linecap="round" stroke-linejoin="round" d="M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
          </svg>
          Configuration
          <span class="text-xs text-gray-500 font-normal ml-1">(read-only)</span>
        </h2>
        <pre class="bg-surface-3 rounded-lg p-4 overflow-x-auto text-xs text-gray-300 font-mono leading-relaxed max-h-96">{JSON.stringify(config, null, 2)}</pre>
      </div>

    </div>
  {/if}
</div>
