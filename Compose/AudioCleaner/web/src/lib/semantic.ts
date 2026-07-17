export type SemanticTone = 'success' | 'danger' | 'info' | 'warning' | 'muted' | 'backup'

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
    default:
      return 'muted'
  }
}
