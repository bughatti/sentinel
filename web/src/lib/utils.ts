// utils.ts — formatting helpers for Sentinel NVR

/**
 * Returns a human-readable relative time string.
 * e.g. "just now", "5m ago", "2h ago", "3 days ago"
 */
export function timeAgo(unixSeconds: number): string {
  const now = Date.now() / 1000
  const diff = now - unixSeconds

  if (diff < 5) return 'just now'
  if (diff < 60) return `${Math.floor(diff)}s ago`
  if (diff < 3600) return `${Math.floor(diff / 60)}m ago`
  if (diff < 86400) return `${Math.floor(diff / 3600)}h ago`
  if (diff < 86400 * 7) return `${Math.floor(diff / 86400)}d ago`

  return new Date(unixSeconds * 1000).toLocaleDateString(undefined, {
    month: 'short',
    day: 'numeric',
    year: diff > 86400 * 365 ? 'numeric' : undefined,
  })
}

/**
 * Returns an absolute datetime string for display on hover.
 */
export function absoluteTime(unixSeconds: number): string {
  return new Date(unixSeconds * 1000).toLocaleString(undefined, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  })
}

/**
 * Format duration in seconds to a human-readable string.
 * e.g. 65 → "1m 5s", 3661 → "1h 1m 1s"
 */
export function formatDuration(seconds: number): string {
  if (seconds < 1) return `${Math.round(seconds * 1000)}ms`
  const h = Math.floor(seconds / 3600)
  const m = Math.floor((seconds % 3600) / 60)
  const s = Math.floor(seconds % 60)

  if (h > 0) return `${h}h ${m}m ${s}s`
  if (m > 0) return `${m}m ${s}s`
  return `${s}s`
}

/**
 * Format score (0–1 float) as a percentage string.
 */
export function formatScore(score: number): string {
  return `${Math.round(score * 100)}%`
}

/**
 * Format bytes to human-readable string.
 */
export function formatBytes(bytes: number): string {
  if (bytes === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(1024))
  return `${(bytes / Math.pow(1024, i)).toFixed(1)} ${units[i]}`
}

/**
 * Capitalize first letter of a string.
 */
export function capitalize(s: string): string {
  if (!s) return s
  return s.charAt(0).toUpperCase() + s.slice(1)
}

/**
 * Get Tailwind color classes for a detection label.
 */
export function labelColors(label: string): { bg: string; text: string; dot: string } {
  const map: Record<string, { bg: string; text: string; dot: string }> = {
    person:     { bg: 'bg-blue-500/20',   text: 'text-blue-400',   dot: 'bg-blue-500' },
    car:        { bg: 'bg-yellow-500/20', text: 'text-yellow-400', dot: 'bg-yellow-500' },
    truck:      { bg: 'bg-amber-500/20',  text: 'text-amber-400',  dot: 'bg-amber-500' },
    dog:        { bg: 'bg-green-500/20',  text: 'text-green-400',  dot: 'bg-green-500' },
    cat:        { bg: 'bg-purple-500/20', text: 'text-purple-400', dot: 'bg-purple-500' },
    bike:       { bg: 'bg-orange-500/20', text: 'text-orange-400', dot: 'bg-orange-500' },
    motorcycle: { bg: 'bg-red-500/20',    text: 'text-red-400',    dot: 'bg-red-500' },
    bird:       { bg: 'bg-teal-500/20',   text: 'text-teal-400',   dot: 'bg-teal-500' },
    package:    { bg: 'bg-indigo-500/20', text: 'text-indigo-400', dot: 'bg-indigo-500' },
  }
  return map[label.toLowerCase()] ?? { bg: 'bg-gray-500/20', text: 'text-gray-400', dot: 'bg-gray-500' }
}

/**
 * Format a date as YYYY-MM-DD string (local time).
 */
export function toDateString(date: Date): string {
  const y = date.getFullYear()
  const m = String(date.getMonth() + 1).padStart(2, '0')
  const d = String(date.getDate()).padStart(2, '0')
  return `${y}-${m}-${d}`
}

/**
 * Parse a YYYY-MM-DD string into a Date (local midnight).
 */
export function fromDateString(s: string): Date {
  const [y, m, d] = s.split('-').map(Number)
  return new Date(y, m - 1, d)
}

/**
 * Returns start/end Unix timestamps for a given date string and hour (0–23).
 */
export function hourRange(dateStr: string, hour: number): [number, number] {
  const [y, m, d] = dateStr.split('-').map(Number)
  const start = new Date(y, m - 1, d, hour, 0, 0, 0)
  const end = new Date(y, m - 1, d, hour + 1, 0, 0, 0)
  return [start.getTime() / 1000, end.getTime() / 1000]
}

/**
 * Clamp a number between min and max.
 */
export function clamp(n: number, min: number, max: number): number {
  return Math.max(min, Math.min(max, n))
}
