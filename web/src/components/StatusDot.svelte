<script lang="ts">
  import type { ConnectionState } from '../lib/ws'

  interface Props {
    state: ConnectionState
  }

  let { state }: Props = $props()

  const label: Record<ConnectionState, string> = {
    connecting: 'Connecting',
    connected: 'Connected',
    disconnected: 'Disconnected',
    error: 'Error',
  }

  const dotClass: Record<ConnectionState, string> = {
    connecting: 'bg-yellow-400 animate-pulse',
    connected: 'bg-green-400',
    disconnected: 'bg-gray-500',
    error: 'bg-red-400 animate-pulse',
  }
</script>

<div class="flex items-center gap-1.5" title={label[state]}>
  <div class="w-2 h-2 rounded-full {dotClass[state]}"></div>
  <span class="text-xs text-gray-400 hidden sm:inline">{label[state]}</span>
</div>
