<script lang="ts">
  import StatusDot from './StatusDot.svelte'
  import type { ConnectionState } from '../lib/ws'

  interface Props {
    currentRoute: string
    wsState: ConnectionState
    newEventCount?: number
    onNavigate: (route: string) => void
  }

  let { currentRoute, wsState, newEventCount = 0, onNavigate }: Props = $props()

  const links = [
    { route: '/live',       label: 'Live',       icon: 'M15 10l4.553-2.069A1 1 0 0121 8.868V15.132a1 1 0 01-1.447.9L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z' },
    { route: '/events',     label: 'Events',     icon: 'M12 8v4l3 3m6-3a9 9 0 11-18 0 9 9 0 0118 0z' },
    { route: '/recordings', label: 'Recordings', icon: 'M7 4v16M17 4v16M3 8h4m10 0h4M3 12h18M3 16h4m10 0h4M4 20h16a1 1 0 001-1V5a1 1 0 00-1-1H4a1 1 0 00-1 1v14a1 1 0 001 1z' },
    { route: '/faces',      label: 'Faces',      icon: 'M16 7a4 4 0 11-8 0 4 4 0 018 0zM12 14a7 7 0 00-7 7h14a7 7 0 00-7-7z' },
    { route: '/settings',   label: 'Settings',   icon: 'M10.325 4.317c.426-1.756 2.924-1.756 3.35 0a1.724 1.724 0 002.573 1.066c1.543-.94 3.31.826 2.37 2.37a1.724 1.724 0 001.065 2.572c1.756.426 1.756 2.924 0 3.35a1.724 1.724 0 00-1.066 2.573c.94 1.543-.826 3.31-2.37 2.37a1.724 1.724 0 00-2.572 1.065c-.426 1.756-2.924 1.756-3.35 0a1.724 1.724 0 00-2.573-1.066c-1.543.94-3.31-.826-2.37-2.37a1.724 1.724 0 00-1.065-2.572c-1.756-.426-1.756-2.924 0-3.35a1.724 1.724 0 001.066-2.573c-.94-1.543.826-3.31 2.37-2.37.996.608 2.296.07 2.572-1.065z M15 12a3 3 0 11-6 0 3 3 0 016 0z' },
  ]

  function isActive(route: string): boolean {
    return currentRoute === route
  }

  function handleNav(route: string): void {
    onNavigate(route)
  }
</script>

<nav class="sticky top-0 z-50 bg-surface-1 border-b border-surface-3 px-4">
  <div class="max-w-screen-2xl mx-auto flex items-center h-14 gap-2">

    <!-- Logo -->
    <button
      onclick={() => handleNav('/live')}
      class="flex items-center gap-2.5 mr-4 shrink-0 group"
    >
      <div class="w-8 h-8 bg-accent rounded-lg flex items-center justify-center">
        <svg class="w-5 h-5 text-white" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="2">
          <path stroke-linecap="round" stroke-linejoin="round" d="M15 10l4.553-2.069A1 1 0 0121 8.868V15.132a1 1 0 01-1.447.9L15 14M5 18h8a2 2 0 002-2V8a2 2 0 00-2-2H5a2 2 0 00-2 2v8a2 2 0 002 2z" />
        </svg>
      </div>
      <span class="font-semibold text-white text-lg tracking-tight hidden sm:inline">
        Sentinel <span class="font-normal text-gray-400">NVR</span>
      </span>
    </button>

    <!-- Nav links -->
    <div class="flex items-center gap-1 flex-1">
      {#each links as link}
        <button
          onclick={() => handleNav(link.route)}
          class="relative flex items-center gap-1.5 px-3 py-1.5 rounded-lg text-sm font-medium transition-colors duration-150
                 {isActive(link.route)
                   ? 'bg-accent/10 text-accent'
                   : 'text-gray-400 hover:text-gray-200 hover:bg-surface-3'}"
        >
          <svg class="w-4 h-4 shrink-0" fill="none" viewBox="0 0 24 24" stroke="currentColor" stroke-width="1.75">
            <path stroke-linecap="round" stroke-linejoin="round" d={link.icon} />
          </svg>
          <span class="hidden md:inline">{link.label}</span>

          {#if link.route === '/events' && newEventCount > 0}
            <span class="absolute -top-0.5 -right-0.5 min-w-[16px] h-4 bg-accent text-white text-[10px] font-bold rounded-full flex items-center justify-center px-1">
              {newEventCount > 99 ? '99+' : newEventCount}
            </span>
          {/if}
        </button>
      {/each}
    </div>

    <!-- Right side: WS status -->
    <div class="shrink-0">
      <StatusDot state={wsState} />
    </div>
  </div>
</nav>
