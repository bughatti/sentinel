// ws.ts — WebSocket event stream client for Sentinel NVR
// Connects to ws://<host>/ws and emits typed events.

import type { SentinelEvent } from './api'

export type ConnectionState = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface WSMessage {
  type: 'new' | 'update' | 'end' | 'ping' | 'stats'
  after?: string
  before?: string
  payload?: SentinelEvent
  [key: string]: unknown
}

type EventHandler = (msg: WSMessage) => void
type StateHandler = (state: ConnectionState) => void

const RECONNECT_BASE_MS = 1_000
const RECONNECT_MAX_MS = 30_000

export class SentinelWS {
  private ws: WebSocket | null = null
  private url: string
  private handlers: EventHandler[] = []
  private stateHandlers: StateHandler[] = []
  private reconnectDelay = RECONNECT_BASE_MS
  private stopped = false
  private pingInterval: ReturnType<typeof setInterval> | null = null

  constructor(path = '/ws') {
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
    this.url = `${proto}//${window.location.host}${path}`
  }

  connect(): void {
    if (this.ws && this.ws.readyState < WebSocket.CLOSING) return
    this.stopped = false
    this._connect()
  }

  disconnect(): void {
    this.stopped = true
    this._clearPing()
    if (this.ws) {
      this.ws.close(1000, 'client disconnect')
      this.ws = null
    }
    this._setState('disconnected')
  }

  onMessage(handler: EventHandler): () => void {
    this.handlers.push(handler)
    return () => {
      this.handlers = this.handlers.filter(h => h !== handler)
    }
  }

  onStateChange(handler: StateHandler): () => void {
    this.stateHandlers.push(handler)
    return () => {
      this.stateHandlers = this.stateHandlers.filter(h => h !== handler)
    }
  }

  private _connect(): void {
    this._setState('connecting')
    try {
      this.ws = new WebSocket(this.url)
    } catch {
      this._scheduleReconnect()
      return
    }

    this.ws.onopen = () => {
      this.reconnectDelay = RECONNECT_BASE_MS
      this._setState('connected')
      this._startPing()
    }

    this.ws.onmessage = (ev) => {
      try {
        const msg = JSON.parse(ev.data as string) as WSMessage
        for (const h of this.handlers) h(msg)
      } catch {
        // ignore malformed messages
      }
    }

    this.ws.onclose = () => {
      this._clearPing()
      if (!this.stopped) {
        this._setState('disconnected')
        this._scheduleReconnect()
      }
    }

    this.ws.onerror = () => {
      this._setState('error')
      // onclose will fire next and handle reconnect
    }
  }

  private _scheduleReconnect(): void {
    if (this.stopped) return
    setTimeout(() => {
      if (!this.stopped) this._connect()
    }, this.reconnectDelay)
    this.reconnectDelay = Math.min(this.reconnectDelay * 2, RECONNECT_MAX_MS)
  }

  private _startPing(): void {
    this._clearPing()
    this.pingInterval = setInterval(() => {
      if (this.ws?.readyState === WebSocket.OPEN) {
        this.ws.send(JSON.stringify({ type: 'ping' }))
      }
    }, 30_000)
  }

  private _clearPing(): void {
    if (this.pingInterval !== null) {
      clearInterval(this.pingInterval)
      this.pingInterval = null
    }
  }

  private _setState(state: ConnectionState): void {
    for (const h of this.stateHandlers) h(state)
  }
}

// Singleton instance shared across pages
export const ws = new SentinelWS('/ws')
