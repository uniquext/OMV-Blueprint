import type { JobPhase } from './types'

export type SemanticTone = 'success' | 'danger' | 'info' | 'warning' | 'restored' | 'muted' | 'backup'

export function semanticBadgeClass(tone: SemanticTone): string {
  return `semantic-badge semantic-badge--${tone}`
}

export function jobStatusTone(status: string): SemanticTone {
  switch (status) {
    case 'compatible':
    case 'processed':
      return 'success'
    case 'failed':
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
    case 'pending':
      return 'muted'
    case 'queued':
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
