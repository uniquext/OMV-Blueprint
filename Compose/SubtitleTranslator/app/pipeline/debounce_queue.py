import time
import queue
import logging
import threading
from typing import Dict, List, Optional
from concurrent.futures import ThreadPoolExecutor

import db
from pipeline.funnel import evaluate_media, execute_funnel_action
from subtitle.lang_utils import normalize_language

logger = logging.getLogger(__name__)


class DebounceMap:
    def __init__(self, debounce_seconds: float = 60.0, lang_map_override: Dict = None):
        self._entries: Dict[str, Dict] = {}
        self._lock = threading.Lock()
        self._debounce_seconds = debounce_seconds
        self._lang_map_override = lang_map_override

    def upsert(self, media_path: str, source: str = "", language: str = "") -> None:
        with self._lock:
            now = time.time()
            normalized = normalize_language(language, self._lang_map_override) if language else ""
            if normalized == "zh":
                ready_at = now
            else:
                ready_at = now + self._debounce_seconds

            self._entries[media_path] = {
                "media_path": media_path,
                "source": source,
                "language": language,
                "ready_at": ready_at,
            }
            logger.debug(f"Debounce upsert: {media_path} (ready_at={ready_at:.1f})")

    def pop_ready(self) -> List[Dict]:
        with self._lock:
            now = time.time()
            ready = []
            expired_keys = []

            for key, entry in self._entries.items():
                if entry["ready_at"] <= now:
                    ready.append(entry.copy())
                    expired_keys.append(key)

            for key in expired_keys:
                del self._entries[key]

            return ready

    def contains(self, media_path: str) -> bool:
        with self._lock:
            return media_path in self._entries


class FunnelWorkerPool:
    def __init__(self, max_workers: int = 5, debounce_map: DebounceMap = None):
        self._queue: queue.Queue = queue.Queue()
        self._executor = ThreadPoolExecutor(max_workers=max_workers)
        self._running = True
        self._futures = []
        self._debounce_map = debounce_map

        for _ in range(max_workers):
            future = self._executor.submit(self._worker_loop)
            self._futures.append(future)

        logger.info(f"FunnelWorkerPool started with {max_workers} workers")

    def submit_job(self, media_path: str) -> bool:
        if self._debounce_map and self._debounce_map.contains(media_path):
            logger.debug(f"Skipped enqueue, key in debounce map: {media_path}")
            return False
        self._queue.put(media_path)
        logger.debug(f"Submitted to FIFO: {media_path}")
        return True

    def _worker_loop(self) -> None:
        while self._running:
            try:
                media_path = self._queue.get(timeout=0.5)
            except queue.Empty:
                continue

            try:
                self._process_media(media_path)
            except Exception as e:
                logger.error(f"Worker error processing {media_path}: {e}")
            finally:
                self._queue.task_done()

    def _process_media(self, media_path: str) -> None:
        job_id = db.create_job(media_path)
        if job_id is None:
            logger.info(f"Job deduped for {media_path}, skipping")
            return

        funnel_result = evaluate_media(media_path)
        execute_funnel_action(job_id, media_path, funnel_result)

    def shutdown(self, wait: bool = True, timeout: float = 10) -> None:
        if wait:
            try:
                self._queue.join()
            except Exception:
                pass

        self._running = False
        self._executor.shutdown(wait=wait)
        logger.info("FunnelWorkerPool shutdown")


def start_debounce_scheduler(debounce_map: DebounceMap, worker_pool: FunnelWorkerPool, interval: float = 1.0) -> threading.Thread:
    def scheduler_loop():
        logger.info("Debounce scheduler started")
        while worker_pool._running:
            ready = debounce_map.pop_ready()
            for entry in ready:
                worker_pool.submit_job(entry["media_path"])
            time.sleep(interval)

    thread = threading.Thread(target=scheduler_loop, daemon=True)
    thread.start()
    return thread
