const serviceTimestampPattern = /^(\d{4}-\d{2}-\d{2})T(\d{2}:\d{2})(?::(\d{2}))?/

export function serviceDatePart(value: string | undefined): string {
  if (!value) {
    return ''
  }
  return value.match(serviceTimestampPattern)?.[1] ?? ''
}

export function formatServiceTimestamp(value: string, includeSeconds = false): string {
  if (!value) {
    return ''
  }
  const match = value.match(serviceTimestampPattern)
  if (!match) {
    return value
  }
  const [, date, hourAndMinute, seconds = '00'] = match
  return includeSeconds ? `${date} ${hourAndMinute}:${seconds}` : `${date} ${hourAndMinute}`
}

export function serviceDurationMilliseconds(startedAt: string, finishedAt: string): number | null {
  const started = parseServiceTimestamp(startedAt)
  const finished = parseServiceTimestamp(finishedAt)
  if (started === null || finished === null || finished < started) {
    return null
  }
  return finished - started
}

function parseServiceTimestamp(value: string): number | null {
  if (!value) {
    return null
  }
  const millisecondPrecision = value.replace(/(\.\d{3})\d+(?=(?:Z|[+-]\d{2}:\d{2})$)/, '$1')
  const parsed = Date.parse(millisecondPrecision)
  return Number.isNaN(parsed) ? null : parsed
}
