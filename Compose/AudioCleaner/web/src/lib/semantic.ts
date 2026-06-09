import type { JobPhase } from './types'

export type SemanticTone = 'success' | 'danger' | 'info' | 'warning' | 'restored' | 'muted' | 'backup'

export function semanticBadgeClass(tone: SemanticTone): string {
  return `semantic-badge semantic-badge--${tone}`
}

export function jobStatusTone(status: string): SemanticTone {
  switch (status) {
    case 'qualified':
      return 'success'
    case 'unqualified':
      return 'danger'
    case 'processing':
      return 'info'
    case 'restored':
      return 'restored'
    case 'ignored':
      return 'muted'
    default:
      return 'muted'
  }
}

export function backupStateTone(state: string): SemanticTone {
  switch (state) {
    case 'available':
      return 'success'
    case 'restored':
      return 'restored'
    case 'missing':
      return 'danger'
    case 'expired':
      return 'warning'
    default:
      return 'muted'
  }
}

export function jobPhaseTone(phase: JobPhase | string): SemanticTone {
  switch (phase) {
    case 'queued':
    case 'deferred':
    case 'retry_wait':
      return 'warning'
    case 'checking':
    case 'transcoding':
    case 'verifying':
    case 'replacing':
      return 'info'
    case 'backing_up':
      return 'backup'
    default:
      return 'muted'
  }
}

export function eventTypeTone(eventType: string): SemanticTone {
  switch (eventType) {
    case 'job.qualified':
      return 'success'
    case 'job.unqualified':
    case 'service.restart_failed':
      return 'danger'
    case 'job.restored':
      return 'restored'
    case 'job.queued':
      return 'warning'
    case 'job.backing_up':
      return 'backup'
    case 'job.checking':
    case 'job.transcoding':
    case 'job.verifying':
    case 'job.replacing':
    case 'job.ffmpeg_data_stream_fallback':
    case 'service.restarting':
      return 'info'
    default:
      return 'muted'
  }
}
