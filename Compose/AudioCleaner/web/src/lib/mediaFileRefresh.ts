import type { ServerSentEvent } from './types'

const fileRefreshEvents = new Set([
  'compatible',
  'processed',
  'failed',
  'file.backup_created',
  'file.backup_restored',
  'file.backup_deleted'
])

export function shouldRefreshMediaFiles(event: ServerSentEvent | null | undefined): boolean {
  if (!event) return false
  if (fileRefreshEvents.has(event.type)) return true
  return event.type === 'config.applied' && event.data?.module === 'media'
}
