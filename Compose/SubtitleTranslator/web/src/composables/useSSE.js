import { ref, onUnmounted } from 'vue';

export function useSSE(url, options = {}) {
    const { onEvent, onSnapshotGap } = options;
    const connected = ref(false);

    let eventSource = null;
    let nextExpectedSnapshotId = null;
    let reconnectDelay = 1000;
    const MAX_RECONNECT_DELAY = 30000;
    let reconnectTimeout = null;
    let closed = false;

    const connect = () => {
        if (closed) return;
        if (eventSource) {
            eventSource.close();
        }

        eventSource = new EventSource(url);

        eventSource.onopen = () => {
            connected.value = true;
            reconnectDelay = 1000;
        };

        eventSource.onerror = () => {
            connected.value = false;
            eventSource.close();
            eventSource = null;

            if (closed) return;
            clearTimeout(reconnectTimeout);
            reconnectTimeout = setTimeout(connect, reconnectDelay);
            reconnectDelay = Math.min(reconnectDelay * 2, MAX_RECONNECT_DELAY);
        };

        eventSource.onmessage = (event) => {
            if (!event.data) return;

            try {
                const data = JSON.parse(event.data);
                const eventType = data.type || 'message';

                if (data.snapshot_id !== undefined) {
                    if (nextExpectedSnapshotId !== null && data.snapshot_id > nextExpectedSnapshotId) {
                        if (onSnapshotGap) onSnapshotGap(data.snapshot_id, nextExpectedSnapshotId);
                    }
                    nextExpectedSnapshotId = data.snapshot_id + 1;
                }

                if (onEvent) onEvent(eventType, data);
            } catch (err) {
                console.error("Failed to parse SSE message", err);
            }
        };
    };

    const close = () => {
        closed = true;
        clearTimeout(reconnectTimeout);
        if (eventSource) {
            eventSource.close();
            eventSource = null;
        }
        connected.value = false;
    };

    onUnmounted(() => {
        close();
    });

    connect();

    return { connected, connect, close };
}
