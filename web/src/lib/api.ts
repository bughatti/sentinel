// api.ts — typed API client for Sentinel NVR
// All requests use relative URLs — Go binary serves API + static files from the same origin.

const BASE = ''

// ─── Types ────────────────────────────────────────────────────────────────────

export interface Camera {
  name: string
  enabled: boolean
  detect_width: number
  detect_height: number
  detect_fps: number
  online: boolean
}

export interface Box {
  x1: number
  y1: number
  x2: number
  y2: number
}

export interface Region {
  x1: number
  y1: number
  x2: number
  y2: number
}

export interface SentinelEvent {
  id: string
  camera: string
  type: 'new' | 'update' | 'end'
  label: string
  sub_label: string[]
  score: number
  top_score: number
  start_time: number    // Unix epoch with fractional seconds
  end_time: number | null
  box: Box
  region: Region
  area: number
  entered_zones: string[]
  current_zones: string[]
  has_clip: boolean
  has_snapshot: boolean
  retain_indefinitely: boolean
  false_positive: boolean
  model_hash?: string
  detector_type?: string
  model_type?: string
  data?: Record<string, unknown>
}

export interface Recording {
  id: string
  camera: string
  path: string
  start_time: number
  end_time: number
  duration: number
  motion: boolean
  objects: string[]
  segment_size: number
}

export interface RecordingSummary {
  day: string    // "YYYY-MM-DD"
  camera: string
  duration: number
  motion: number
  objects: number
}

export interface DetectionStats {
  detection_enabled: boolean
  total_frames: number
  total_detection_time: number
  detection_fps: number
  avg_inference_speed: number
}

export interface CameraStats {
  camera_fps: number
  detect_fps: number
  process_fps: number
  skipped_fps: number
}

export interface ServiceStats {
  uptime: number
  version: string
  go_version: string
  num_cpu: number
  num_goroutines: number
}

export interface Stats {
  detection: DetectionStats
  cameras: Record<string, CameraStats>
  service: ServiceStats
  timestamp: number
}

export interface EventsSummaryItem {
  camera: string
  label: string
  count: number
}

// ─── Filter params ─────────────────────────────────────────────────────────────

export interface EventsParams {
  camera?: string
  label?: string
  sub_label?: string
  after?: number
  before?: number
  has_clip?: boolean
  has_snapshot?: boolean
  false_positive?: boolean
  zone?: string
  limit?: number
  skip?: number
}

export interface RecordingsParams {
  camera?: string
  after?: number
  before?: number
  limit?: number
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

async function get<T>(path: string, params?: Record<string, string | number | boolean | undefined>): Promise<T> {
  const url = new URL(BASE + path, window.location.href)
  if (params) {
    for (const [k, v] of Object.entries(params)) {
      if (v !== undefined && v !== null && v !== '') {
        url.searchParams.set(k, String(v))
      }
    }
  }
  const res = await fetch(url.toString())
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`API ${res.status}: ${text}`)
  }
  return res.json() as Promise<T>
}

async function post<T>(path: string, body?: unknown): Promise<T> {
  const res = await fetch(BASE + path, {
    method: 'POST',
    headers: body ? { 'Content-Type': 'application/json' } : {},
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`API ${res.status}: ${text}`)
  }
  return res.json() as Promise<T>
}

async function del<T>(path: string): Promise<T> {
  const res = await fetch(BASE + path, { method: 'DELETE' })
  if (!res.ok) {
    const text = await res.text().catch(() => res.statusText)
    throw new Error(`API ${res.status}: ${text}`)
  }
  return res.json() as Promise<T>
}

// ─── API functions ────────────────────────────────────────────────────────────

export async function getCameras(): Promise<Camera[]> {
  return get<Camera[]>('/api/cameras')
}

export async function getCamera(name: string): Promise<Camera> {
  return get<Camera>(`/api/cameras/${encodeURIComponent(name)}`)
}

export async function getEvents(params?: EventsParams): Promise<SentinelEvent[]> {
  return get<SentinelEvent[]>('/api/events', params as Record<string, string | number | boolean | undefined>)
}

export async function getEvent(id: string): Promise<SentinelEvent> {
  return get<SentinelEvent>(`/api/events/${encodeURIComponent(id)}`)
}

export async function deleteEvent(id: string): Promise<{ id: string }> {
  return del<{ id: string }>(`/api/events/${encodeURIComponent(id)}`)
}

export async function markFalsePositive(id: string): Promise<SentinelEvent> {
  return post<SentinelEvent>(`/api/events/${encodeURIComponent(id)}/false_positive`)
}

export async function retainEvent(id: string): Promise<SentinelEvent> {
  return post<SentinelEvent>(`/api/events/${encodeURIComponent(id)}/retain`)
}

export async function getStats(): Promise<Stats> {
  return get<Stats>('/api/stats')
}

export async function getEventsSummary(): Promise<EventsSummaryItem[]> {
  return get<EventsSummaryItem[]>('/api/events/summary')
}

export async function getRecordings(params?: RecordingsParams): Promise<Recording[]> {
  return get<Recording[]>('/api/recordings', params as Record<string, string | number | boolean | undefined>)
}

export async function getRecordingsSummary(): Promise<RecordingSummary[]> {
  return get<RecordingSummary[]>('/api/recordings/summary')
}

export async function getCameraRecordings(name: string, params?: RecordingsParams): Promise<Recording[]> {
  return get<Recording[]>(`/api/cameras/${encodeURIComponent(name)}/recordings`,
    params as Record<string, string | number | boolean | undefined>)
}

// ─── URL helpers ──────────────────────────────────────────────────────────────

export function snapshotUrl(eventId: string): string {
  return `/api/events/${encodeURIComponent(eventId)}/snapshot.jpg`
}

export function clipUrl(eventId: string): string {
  return `/api/events/${encodeURIComponent(eventId)}/clip.mp4`
}

export function latestFrameUrl(cameraName: string): string {
  return `/api/cameras/${encodeURIComponent(cameraName)}/latest-frame`
}

export function vodPlaylistUrl(date: string, hour: number, camera: string): string {
  const [year, month, day] = date.split('-')
  const h = String(hour).padStart(2, '0')
  return `/vod/${year}-${month}-${day}/${h}/${encodeURIComponent(camera)}/index.m3u8`
}

// recordingVideoUrl converts a recording path from the DB into a serveable VOD URL.
// Path format: /recordings/{camera}/{YYYY-MM-DD}/{HH}/{MMSS}.mp4
export function recordingVideoUrl(path: string): string {
  const parts = path.split('/')
  const idx = parts.indexOf('recordings')
  if (idx < 0 || parts.length < idx + 5) return ''
  const [, camera, date, hour, file] = parts.slice(idx)
  const [year, month, day] = date.split('-')
  return `/vod/${year}-${month}-${day}/${hour}/${encodeURIComponent(camera)}/${file}`
}

// ─── Faces ──────────────────────────────────────────────────────────────────

export interface FaceIdentity {
  id: number
  name: string
  created_at: number
}

export async function getFaces(): Promise<FaceIdentity[]> {
  return get<FaceIdentity[]>('/api/faces')
}

export async function enrollFace(name: string, eventId: string): Promise<{ id: number; name: string; box: number[] }> {
  return post('/api/faces/enroll', { name, event_id: eventId })
}

export async function deleteFace(id: number): Promise<{ deleted: number }> {
  return del(`/api/faces/${id}`)
}
