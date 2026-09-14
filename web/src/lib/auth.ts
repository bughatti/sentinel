// auth.ts — API key handling for the dashboard.
//
// When the server has api.auth_enabled turned on, every data request needs
// the key. fetch() calls send it as a Bearer token. Things the browser loads
// on its own (<img>, <video>, HLS segments, the WebSocket) cannot carry
// headers, so their URLs carry ?api_key= instead.
//
// The key lives in this browser's localStorage only. When the server answers
// 401, listeners are told so the app can ask for a key.

const STORAGE_KEY = 'sentinel.apiKey'

type Listener = () => void
const listeners = new Set<Listener>()

export function getApiKey(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? ''
  } catch {
    return ''
  }
}

export function setApiKey(key: string): void {
  try {
    if (key) localStorage.setItem(STORAGE_KEY, key)
    else localStorage.removeItem(STORAGE_KEY)
  } catch {
    // Storage blocked (private mode): the key will not persist across reloads.
  }
}

export function authHeaders(extra?: HeadersInit): Headers {
  const headers = new Headers(extra)
  const key = getApiKey()
  if (key) headers.set('Authorization', `Bearer ${key}`)
  return headers
}

// withKey returns url with any extra query params and, when a key is saved,
// ?api_key=. Same-origin URLs stay relative.
export function withKey(url: string, params?: Record<string, string | number>): string {
  const u = new URL(url, window.location.href)
  if (params) {
    for (const [k, v] of Object.entries(params)) u.searchParams.set(k, String(v))
  }
  const key = getApiKey()
  if (key) u.searchParams.set('api_key', key)
  return u.origin === window.location.origin ? u.pathname + u.search : u.toString()
}

export function onAuthRequired(fn: Listener): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

export function notifyAuthRequired(): void {
  for (const fn of listeners) fn()
}

// authFetch is fetch with the key attached; a 401 raises the key prompt.
export async function authFetch(input: string, init: RequestInit = {}): Promise<Response> {
  const res = await fetch(input, { ...init, headers: authHeaders(init.headers) })
  if (res.status === 401) notifyAuthRequired()
  return res
}
