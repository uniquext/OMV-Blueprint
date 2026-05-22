import asyncio
import threading
import logging
from typing import Dict, Any, List

logger = logging.getLogger(__name__)

class EventBus:
    def __init__(self, loop: asyncio.AbstractEventLoop):
        self._loop = loop
        self._snapshot_id = 0
        self._subscribers: List[asyncio.Queue] = []
        self._lock = threading.Lock()

    def subscribe(self) -> asyncio.Queue:
        queue = asyncio.Queue(maxsize=256)
        with self._lock:
            self._subscribers.append(queue)
        return queue

    def unsubscribe(self, queue: asyncio.Queue) -> None:
        with self._lock:
            if queue in self._subscribers:
                self._subscribers.remove(queue)

    def publish(self, event_type: str, payload: Dict[str, Any]) -> None:
        with self._lock:
            self._snapshot_id += 1
            current_id = self._snapshot_id

        # Update payload with snapshot_id
        # To avoid modifying the caller's dictionary, create a shallow copy
        event_payload = payload.copy()
        event_payload["snapshot_id"] = current_id

        event = {
            "type": event_type,
            "payload": event_payload
        }

        if self._loop and self._loop.is_running():
            asyncio.run_coroutine_threadsafe(self._dispatch(event), self._loop)

    async def _dispatch(self, event: Dict[str, Any]) -> None:
        # Create a shallow copy of subscribers to iterate over
        with self._lock:
            subscribers = self._subscribers.copy()

        for queue in subscribers:
            try:
                queue.put_nowait(event)
            except asyncio.QueueFull:
                logger.warning("Subscriber queue is full, dropping event")

    def get_snapshot_id(self) -> int:
        with self._lock:
            return self._snapshot_id

    def clear(self) -> None:
        with self._lock:
            self._subscribers.clear()
