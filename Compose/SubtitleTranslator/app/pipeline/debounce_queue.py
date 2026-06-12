import time
import math
import datetime
import queue
import logging
import threading
from typing import Dict, List, Optional
from concurrent.futures import ThreadPoolExecutor

from core import db
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

    def snapshot(self) -> Dict:
        """返回当前待处理条目的快照（线程安全副本）"""
        with self._lock:
            now = time.time()
            entries = []
            for e in self._entries.values():
                ready_at = e["ready_at"]
                remaining = max(0, math.ceil(ready_at - now))
                ready_at_iso = datetime.datetime.fromtimestamp(ready_at, tz=datetime.timezone.utc).isoformat()
                entries.append({
                    "media_path": e["media_path"],
                    "ready_at": ready_at_iso,
                    "source": e.get("source", ""),
                    "language": e.get("language", ""),
                    "remaining_seconds": remaining
                })
            return {"pending_count": len(entries), "entries": entries}


class FunnelWorkerPool:
    def __init__(self, max_workers: int = 5, debounce_map: DebounceMap = None):
        self._queue: queue.Queue = queue.Queue()
        self._executor = ThreadPoolExecutor(max_workers=max_workers)
        self._running = True
        self._futures = []
        self._debounce_map = debounce_map
        # 活跃任务计数器（线程安全）
        self._active_count = 0
        self._active_lock = threading.Lock()

        for _ in range(max_workers):
            future = self._executor.submit(self._worker_loop)
            self._futures.append(future)

        logger.info(f"FunnelWorkerPool started with {max_workers} workers")

    def submit_job(self, media_path: str, source: str = "scheduler") -> bool:
        if self._debounce_map and self._debounce_map.contains(media_path):
            logger.debug(f"Skipped enqueue, key in debounce map: {media_path}")
            return False
        self._queue.put({"media_path": media_path, "source": source})
        logger.debug(f"Submitted to FIFO: {media_path}")
        return True

    def _worker_loop(self) -> None:
        while self._running:
            try:
                item = self._queue.get(timeout=0.5)
            except queue.Empty:
                continue

            try:
                # 计数器递增
                with self._active_lock:
                    self._active_count += 1
                try:
                    # 兼容旧格式（纯字符串）和新格式（字典）
                    if isinstance(item, dict):
                        media_path = item["media_path"]
                    else:
                        media_path = item
                    self._process_media(media_path)
                except Exception as e:
                    logger.error(f"Worker error processing {media_path}: {e}")
                finally:
                    # 计数器递减（try/finally 保护，异常时也正确递减）
                    with self._active_lock:
                        self._active_count -= 1
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
        self._running = False
        
        if wait:
            start_time = time.time()
            while time.time() - start_time < timeout:
                with self._active_lock:
                    if self._active_count == 0:
                        break
                time.sleep(0.1)

        self._executor.shutdown(wait=False, cancel_futures=True)
        logger.info("FunnelWorkerPool shutdown")

    def snapshot(self) -> Dict:
        """返回当前运行时状态快照（含队列条目列表）"""
        with self._active_lock:
            active = self._active_count
        # 获取队列中的待处理条目（不消费，仅窥视）
        items = []
        with self._queue.mutex:
            for entry in self._queue.queue:
                if isinstance(entry, dict):
                    items.append({"media_path": entry["media_path"], "source": entry.get("source", "")})
                else:
                    items.append({"media_path": entry, "source": ""})
        return {
            "queue_size": self._queue.qsize(),
            "active_workers": active,
            "items": items,
        }


def start_debounce_scheduler(debounce_map: DebounceMap, worker_pool: FunnelWorkerPool, interval: float = 1.0) -> threading.Thread:
    def scheduler_loop():
        logger.info("Debounce scheduler started")
        while worker_pool._running:
            ready = debounce_map.pop_ready()
            for entry in ready:
                worker_pool.submit_job(entry["media_path"], source=entry.get("source", ""))
            time.sleep(interval)

    thread = threading.Thread(target=scheduler_loop, daemon=True)
    thread.start()
    return thread
