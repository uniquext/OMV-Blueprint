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
