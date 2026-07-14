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
    default:
      return 'muted'
  }
}

export function backupAvailabilityTone(availability: string): SemanticTone {
  switch (availability) {
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
