import { onBeforeUnmount, onMounted, reactive } from 'vue'
import type { EventStreamStatus } from '../lib/types'

export type RefreshMode = 'full' | 'partial'

export interface EventStreamState {
  status: EventStreamStatus
  lastEventAt: string | null
  lastSnapshotID: number | null
  refreshMode: RefreshMode
}

export const eventStreamState = reactive<EventStreamState>({
  status: 'disconnected',
  lastEventAt: null,
  lastSnapshotID: null,
  refreshMode: 'full'
})

let currentEventSource: EventSource | null = null

export function extractSnapshotID(event: unknown): number | null {
  if (!event || typeof event !== 'object') {
    return null
  }

  const payload = event as { snapshot_id?: unknown; data?: unknown }
  if (typeof payload.snapshot_id === 'number') {
    return payload.snapshot_id
  }

  if (payload.data && typeof payload.data === 'object') {
    const data = payload.data as { snapshot_id?: unknown }
    if (typeof data.snapshot_id === 'number') {
      return data.snapshot_id
    }
  }

  return null
}

export function nextRefreshMode(previous: number | null, next: number): RefreshMode {
  if (previous === null) {
    return 'full'
  }
  return next === previous + 1 ? 'partial' : 'full'
}

function parseEventData(data: string): unknown {
  try {
    return JSON.parse(data)
  } catch {
    return { data: null }
  }
}

function closeCurrentEventSource(): void {
  currentEventSource?.close()
  currentEventSource = null
  eventStreamState.status = 'disconnected'
}

export function useEventStream(url = '/api/events'): EventStreamState {
  onMounted(() => {
    closeCurrentEventSource()

    const eventSource = new EventSource(url)
    currentEventSource = eventSource
    eventStreamState.status = 'connecting'

    eventSource.onopen = () => {
      eventStreamState.status = 'connected'
    }

    eventSource.onmessage = (message) => {
      eventStreamState.lastEventAt = new Date().toISOString()

      const snapshotID = extractSnapshotID(parseEventData(message.data))
      if (snapshotID !== null) {
        eventStreamState.refreshMode = nextRefreshMode(eventStreamState.lastSnapshotID, snapshotID)
        eventStreamState.lastSnapshotID = snapshotID
      }
    }

    eventSource.onerror = () => {
      eventStreamState.status = 'error'
    }
  })

  onBeforeUnmount(() => {
    closeCurrentEventSource()
  })

  return eventStreamState
}
