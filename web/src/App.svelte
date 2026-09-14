<script lang="ts">
  import Nav from './components/Nav.svelte'
  import Live from './pages/Live.svelte'
  import Events from './pages/Events.svelte'
  import Recordings from './pages/Recordings.svelte'
  import Faces from './pages/Faces.svelte'
  import Settings from './pages/Settings.svelte'
  import KeyPrompt from './components/KeyPrompt.svelte'
  import { onAuthRequired } from './lib/auth'
  import type { ConnectionState } from './lib/ws'
  import { ws } from './lib/ws'

  type Route = '/live' | '/events' | '/recordings' | '/faces' | '/settings'

  function getRoute(): Route {
    const hash = window.location.hash.replace('#', '') || '/live'
    if (['/live', '/events', '/recordings', '/faces', '/settings'].includes(hash)) {
      return hash as Route
    }
    return '/live'
  }

  let currentRoute: Route = $state(getRoute())
  let wsState: ConnectionState = $state('connecting')
  let newEventCount = $state(0)
  let needKey = $state(false)

  function navigate(route: string) {
    window.location.hash = route
    currentRoute = route as Route
    if (route === '/events') newEventCount = 0
  }

  function handleHashChange() {
    const next = getRoute()
    currentRoute = next
    if (next === '/events') newEventCount = 0
  }

  $effect(() => {
    // Connect WebSocket
    ws.connect()

    const unsubState = ws.onStateChange((s) => {
      wsState = s
    })

    const unsubMsg = ws.onMessage((msg) => {
      if (msg.type === 'new' && currentRoute !== '/events') {
        newEventCount++
      }
    })

    window.addEventListener('hashchange', handleHashChange)
    const unsubAuth = onAuthRequired(() => {
      needKey = true
    })

    return () => {
      unsubAuth()
      unsubState()
      unsubMsg()
      window.removeEventListener('hashchange', handleHashChange)
      ws.disconnect()
    }
  })
</script>

<div class="min-h-screen bg-surface flex flex-col">
  <Nav
    {currentRoute}
    {wsState}
    {newEventCount}
    onNavigate={navigate}
  />

  <main class="flex-1">
    {#if currentRoute === '/live'}
      <Live />
    {:else if currentRoute === '/events'}
      <Events />
    {:else if currentRoute === '/recordings'}
      <Recordings />
    {:else if currentRoute === '/faces'}
      <Faces />
    {:else if currentRoute === '/settings'}
      <Settings />
    {/if}
  </main>
</div>

{#if needKey}
  <KeyPrompt />
{/if}
