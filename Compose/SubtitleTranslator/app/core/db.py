import sqlite3
import uuid
import json
import datetime
import logging
import time
import os
from typing import Optional, Dict, List

from constants import TERMINAL_STATES

logger = logging.getLogger(__name__)

DB_PATH = os.environ.get("DB_PATH", "/app/data/queue.db")

_event_bus = None
_loop = None
_last_anomaly_check_time = 0.0
_cached_has_anomaly = False
_cached_anomaly_reason = None

def set_event_bus(bus, loop):
    global _event_bus, _loop
    _event_bus = bus
    _loop = loop

def _publish_event(event_type: str, payload: dict):
    if _event_bus is not None:
        try:
            _event_bus.publish(event_type, payload)
        except Exception as e:
            logger.error(f"Failed to publish event {event_type}: {e}")

def init_db():


    with sqlite3.connect(DB_PATH) as conn:
        # 启用 WAL 模式，支撑 WebUI 并发读写
        conn.execute("PRAGMA journal_mode=WAL")
        conn.execute("""
            CREATE TABLE IF NOT EXISTS translate_task (
                id TEXT PRIMARY KEY,
                file_path TEXT NOT NULL,
                status TEXT NOT NULL,
                progress TEXT,
                current_batch INTEGER DEFAULT 0,
                total_batches INTEGER DEFAULT 0,
                created_at TEXT NOT NULL,
                updated_at TEXT NOT NULL,
                error TEXT,
                completed_at TEXT,
                started_at TEXT
            )
        """)
        conn.execute("""
            CREATE TABLE IF NOT EXISTS subtitle_job (
                id TEXT PRIMARY KEY,
                media_path TEXT NOT NULL,
                status TEXT NOT NULL,
                funnel_level INTEGER,
                original_srt_path TEXT,
                output_srt_path TEXT,
                cleanup_files TEXT,
                translate_task_id TEXT,
                source TEXT DEFAULT 'scheduler',
                created_at TEXT NOT NULL,
                updated_at TEXT NOT NULL,
                error TEXT,
                completed_at TEXT
            )
        """)
        conn.execute("""
            CREATE TABLE IF NOT EXISTS translation_errors (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                task_id TEXT NOT NULL,
                batch_idx INTEGER,
                error_reason TEXT NOT NULL,
                system_prompt TEXT,
                glossary TEXT,
                user_prompt TEXT,
                llm_response TEXT,
                llm_settings TEXT,
                created_at TEXT NOT NULL,
                FOREIGN KEY(task_id) REFERENCES translate_task(id)
            )
        """)
        conn.commit()
    logger.info(f"Database initialized at {DB_PATH}")


def fetch_next_task() -> Optional[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM translate_task WHERE status = 'queued' ORDER BY created_at ASC LIMIT 1"
        )
        row = cursor.fetchone()
        if not row:
            return None

        task = dict(row)
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        # 取出任务时自动设置 started_at
        conn.execute(
            "UPDATE translate_task SET status = 'processing', started_at = ?, updated_at = ? WHERE id = ?",
            (now, now, task['id'])
        )
        conn.commit()
        task['status'] = 'processing'
        task['started_at'] = now
        return task


def get_task(task_id: str) -> Optional[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM translate_task WHERE id = ?",
            (task_id,)
        )
        row = cursor.fetchone()
        if not row:
            return None
        return dict(row)


def get_processing_tasks() -> List[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute("SELECT * FROM translate_task WHERE status = 'processing' ORDER BY created_at ASC")
        return [dict(r) for r in cursor.fetchall()]


def update_progress(task_id: str, current_batch: int, total_batches: int, progress_text: str = ""):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE translate_task SET current_batch = ?, total_batches = ?, progress = ?, updated_at = ? WHERE id = ?",
            (current_batch, total_batches, progress_text, now, task_id)
        )
        conn.commit()

    _publish_event("task_progress", {
        "task_id": task_id,
        "current_batch": current_batch,
        "total_batches": total_batches
    })


def complete_task(task_id: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        # done 是终态，同步设置 completed_at
        conn.execute(
            "UPDATE translate_task SET status = 'done', completed_at = ?, updated_at = ? WHERE id = ?",
            (now, now, task_id)
        )
        conn.commit()


def fail_task(task_id: str, error: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        # failed 是终态，同步设置 completed_at
        conn.execute(
            "UPDATE translate_task SET status = 'failed', error = ?, completed_at = ?, updated_at = ? WHERE id = ?",
            (error, now, now, task_id)
        )
        conn.commit()


def reset_task_to_queued(task_id: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE translate_task SET status = 'queued', updated_at = ? WHERE id = ?",
            (now, task_id)
        )
        conn.commit()
    logger.info(f"Task {task_id} reset to queued for recovery")


def create_job(media_path: str, source: str = "scheduler") -> Optional[str]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        # 去重查询：终态（done/failed/skipped）的 Job 允许重新创建
        cursor = conn.execute(
            "SELECT id FROM subtitle_job WHERE media_path = ? AND status NOT IN ('done', 'failed', 'skipped')",
            (media_path,)
        )
        if cursor.fetchone():
            logger.info(f"Active job already exists for {media_path}, skipping")
            return None

        job_id = str(uuid.uuid4())
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        conn.execute(
            """
            INSERT INTO subtitle_job (id, media_path, status, source, created_at, updated_at)
            VALUES (?, ?, 'funneling', ?, ?, ?)
            """,
            (job_id, media_path, source, now, now)
        )
        conn.commit()
    logger.info(f"Created subtitle job {job_id} for {media_path}")

    _publish_event("job_created", {
        "job_id": job_id,
        "media_path": media_path,
        "source": source
    })

    return job_id


def backfill_job(media_path: str, funnel_level: int, output_srt_path: str = None) -> Optional[str]:
    """回填已存在的字幕文件记录到 subtitle_job"""
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        # 全量去重查询：任何状态下，只要有该 media_path 存在，就跳过
        cursor = conn.execute(
            "SELECT id FROM subtitle_job WHERE media_path = ? LIMIT 1",
            (media_path,)
        )
        if cursor.fetchone():
            return None

        job_id = str(uuid.uuid4())
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        
        conn.execute(
            """
            INSERT INTO subtitle_job (
                id, media_path, status, funnel_level, output_srt_path,
                source, created_at, updated_at, completed_at
            )
            VALUES (?, ?, 'skipped', ?, ?, 'scheduler', ?, ?, ?)
            """,
            (job_id, media_path, funnel_level, output_srt_path, now, now, now)
        )
        conn.commit()
        
    logger.info(f"Backfilled job {job_id} for {media_path} with level {funnel_level}")
    return job_id


def update_job_status(job_id: str, status: str, error: str = None):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    # 终态自动设置 completed_at
    completed_at = now if status in TERMINAL_STATES else None
    with sqlite3.connect(DB_PATH) as conn:
        if error:
            conn.execute(
                "UPDATE subtitle_job SET status = ?, error = ?, completed_at = COALESCE(?, completed_at), updated_at = ? WHERE id = ?",
                (status, error, completed_at, now, job_id)
            )
        else:
            conn.execute(
                "UPDATE subtitle_job SET status = ?, completed_at = COALESCE(?, completed_at), updated_at = ? WHERE id = ?",
                (status, completed_at, now, job_id)
            )
        conn.commit()

        # We need media_path for the event payload, let's fetch it if not available
        conn.row_factory = sqlite3.Row
        cursor = conn.execute("SELECT media_path FROM subtitle_job WHERE id = ?", (job_id,))
        row = cursor.fetchone()
        media_path = dict(row)["media_path"] if row else ""

    logger.info(f"Job {job_id} status updated to {status}")

    payload = {
        "job_id": job_id,
        "status": status,
        "media_path": media_path
    }
    if error:
        payload["error"] = error

    _publish_event("job_status_changed", payload)


def update_job_funnel_info(job_id: str, funnel_level: int, original_srt_path: str = None,
                           output_srt_path: str = None, cleanup_files: List[str] = None):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            """UPDATE subtitle_job
               SET funnel_level = ?, original_srt_path = ?, output_srt_path = ?,
                   cleanup_files = ?, updated_at = ?
               WHERE id = ?""",
            (funnel_level, original_srt_path, output_srt_path,
             json.dumps(cleanup_files) if cleanup_files else None, now, job_id)
        )
        conn.commit()
    logger.info(f"Job {job_id} funnel info updated: level={funnel_level}")


def get_jobs_by_status(status: str) -> List[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM subtitle_job WHERE status = ?",
            (status,)
        )
        return [dict(row) for row in cursor.fetchall()]


def get_jobs(statuses: list = None) -> list:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        if statuses:
            placeholders = ",".join("?" for _ in statuses)
            cursor = conn.execute(
                f"SELECT * FROM subtitle_job WHERE status IN ({placeholders})",
                statuses
            )
        else:
            cursor = conn.execute("SELECT * FROM subtitle_job")
        return [dict(row) for row in cursor.fetchall()]


def get_job_by_media_path(media_path: str) -> Optional[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM subtitle_job WHERE media_path = ? ORDER BY created_at DESC LIMIT 1",
            (media_path,)
        )
        row = cursor.fetchone()
        if not row:
            return None
        return dict(row)


def get_job_by_translate_task(translate_task_id: str) -> Optional[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM subtitle_job WHERE translate_task_id = ?",
            (translate_task_id,)
        )
        row = cursor.fetchone()
        if not row:
            return None
        return dict(row)


def create_translate_task_for_job(job_id: str, file_path: str) -> Optional[str]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT id FROM translate_task WHERE file_path = ? AND status IN ('queued', 'processing')",
            (file_path,)
        )
        if cursor.fetchone():
            logger.info(f"Active task already exists for {file_path}, skipping")
            return None

        task_id = str(uuid.uuid4())
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()

        conn.execute(
            """
            INSERT INTO translate_task (id, file_path, status, progress, current_batch, total_batches, created_at, updated_at)
            VALUES (?, ?, 'queued', '0/0', 0, 0, ?, ?)
            """,
            (task_id, file_path, now, now)
        )
        conn.execute(
            "UPDATE subtitle_job SET status = 'translating', translate_task_id = ?, updated_at = ? WHERE id = ?",
            (task_id, now, job_id)
        )
        conn.commit()

        # Fetch media_path for the event payload
        conn.row_factory = sqlite3.Row
        cursor = conn.execute("SELECT media_path FROM subtitle_job WHERE id = ?", (job_id,))
        row = cursor.fetchone()
        media_path = dict(row)["media_path"] if row else ""

    logger.info(f"Created translate task {task_id} for job {job_id} (file: {file_path})")

    _publish_event("job_status_changed", {
        "job_id": job_id,
        "status": "translating",
        "media_path": media_path
    })

    return task_id


def get_stats() -> dict:
    """获取各个状态 of Job 计数、成功率和平均耗时"""
    global _last_anomaly_check_time, _cached_has_anomaly, _cached_anomaly_reason
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row

        stats = {
            "funneling": 0,
            "extracting": 0,
            "translating": 0,
            "rebuilding": 0,
            "done": 0,
            "skipped": 0,
            "failed": 0
        }

        # Count statuses
        cursor = conn.execute("SELECT status, COUNT(*) as count FROM subtitle_job GROUP BY status")
        for row in cursor.fetchall():
            if row["status"] in stats:
                stats[row["status"]] = row["count"]

        # Calculate success_rate
        total_finished = stats["done"] + stats["failed"]
        success_rate = 0.0
        if total_finished > 0:
            success_rate = round(stats["done"] / total_finished, 3)

        # Calculate avg_duration_seconds (已剔除负数耗时的时钟异常数据)
        cursor = conn.execute(
            "SELECT AVG(strftime('%s', completed_at) - strftime('%s', created_at)) as avg_dur "
            "FROM subtitle_job "
            "WHERE status = 'done' AND completed_at IS NOT NULL "
            "AND (strftime('%s', completed_at) - strftime('%s', created_at)) >= 0"
        )
        row = cursor.fetchone()
        avg_duration = int(row["avg_dur"]) if row and row["avg_dur"] is not None else 0

        # Calculate avg_translate_seconds (已剔除负数耗时的时钟异常数据)
        cursor = conn.execute(
            "SELECT AVG(strftime('%s', completed_at) - strftime('%s', started_at)) as avg_trans "
            "FROM translate_task "
            "WHERE status = 'done' AND completed_at IS NOT NULL AND started_at IS NOT NULL "
            "AND (strftime('%s', completed_at) - strftime('%s', started_at)) >= 0"
        )
        row = cursor.fetchone()
        avg_trans = int(row["avg_trans"]) if row and row["avg_trans"] is not None else 0

        # Calculate funnel_translate_stats (按漏斗级别分组的平均纯翻译时间，且排除负数)
        funnel_stats = {}
        cursor = conn.execute(
            "SELECT j.funnel_level, AVG(strftime('%s', t.completed_at) - strftime('%s', t.started_at)) as avg_trans "
            "FROM subtitle_job j "
            "INNER JOIN translate_task t ON j.translate_task_id = t.id "
            "WHERE t.status = 'done' AND t.completed_at IS NOT NULL AND t.started_at IS NOT NULL "
            "AND (strftime('%s', t.completed_at) - strftime('%s', t.started_at)) >= 0 "
            "GROUP BY j.funnel_level"
        )
        for row in cursor.fetchall():
            if row["funnel_level"] is not None and row["avg_trans"] is not None:
                funnel_stats[str(row["funnel_level"])] = int(row["avg_trans"])

        # 5秒节流时钟回拨与负数耗时扫描
        now_sec = time.time()
        if now_sec - _last_anomaly_check_time > 5.0:
            # 扫描 Job 表
            cursor = conn.execute(
                "SELECT id, media_path, (strftime('%s', completed_at) - strftime('%s', created_at)) as dur "
                "FROM subtitle_job "
                "WHERE status = 'done' AND completed_at IS NOT NULL "
                "AND (strftime('%s', completed_at) - strftime('%s', created_at)) < 0 LIMIT 1"
            )
            row_job = cursor.fetchone()

            # 扫描 Task 表
            cursor = conn.execute(
                "SELECT id, file_path, (strftime('%s', completed_at) - strftime('%s', started_at)) as dur "
                "FROM translate_task "
                "WHERE status = 'done' AND completed_at IS NOT NULL AND started_at IS NOT NULL "
                "AND (strftime('%s', completed_at) - strftime('%s', started_at)) < 0 LIMIT 1"
            )
            row_task = cursor.fetchone()

            if row_job:
                _cached_has_anomaly = True
                _cached_anomaly_reason = f"Detected negative duration in job {row_job['id']} ({row_job['media_path']}): {row_job['dur']}s"
            elif row_task:
                _cached_has_anomaly = True
                _cached_anomaly_reason = f"Detected negative duration in task {row_task['id']} ({row_task['file_path']}): {row_task['dur']}s"
            else:
                _cached_has_anomaly = False
                _cached_anomaly_reason = None

            _last_anomaly_check_time = now_sec

        return {
            **stats,
            "success_rate": success_rate,
            "avg_duration_seconds": avg_duration,
            "avg_translate_seconds": avg_trans,
            "funnel_translate_stats": funnel_stats,
            "has_timing_anomaly": _cached_has_anomaly,
            "anomaly_reason": _cached_anomaly_reason
        }


def query_jobs(page: int = 1, page_size: int = 20, status: list = None, funnel_level: int = None, date_from: str = None, date_to: str = None) -> tuple:
    """分页与多条件模糊过滤查询 Job 列表"""
    conditions = []
    params = []

    if status:
        # 移除空值或过滤非字符串 status 防止注入
        valid_statuses = [s for s in status if isinstance(s, str) and s]
        if valid_statuses:
            placeholders = ",".join("?" for _ in valid_statuses)
            conditions.append(f"status IN ({placeholders})")
            params.extend(valid_statuses)

    if funnel_level is not None:
        conditions.append("funnel_level = ?")
        params.append(funnel_level)

    if date_from:
        conditions.append("created_at >= ?")
        params.append(date_from)

    if date_to:
        conditions.append("created_at <= ?")
        params.append(date_to)

    where_clause = " WHERE " + " AND ".join(conditions) if conditions else ""

    count_sql = f"SELECT COUNT(*) FROM subtitle_job{where_clause}"

    # 按照创建时间倒序排列，并子查询计算 translate_duration（排除歧义风险）
    offset = (page - 1) * page_size
    fetch_sql = (
        f"SELECT *, "
        f"(SELECT strftime('%s', t.completed_at) - strftime('%s', t.started_at) "
        f" FROM translate_task t WHERE t.id = subtitle_job.translate_task_id AND t.completed_at IS NOT NULL AND t.started_at IS NOT NULL) as translate_duration "
        f"FROM subtitle_job{where_clause} ORDER BY created_at DESC LIMIT ? OFFSET ?"
    )

    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row

        # 获取总数
        cursor = conn.execute(count_sql, params)
        total = cursor.fetchone()[0]

        # 分页获取数据
        fetch_params = params + [page_size, offset]
        cursor = conn.execute(fetch_sql, fetch_params)
        items = [dict(row) for row in cursor.fetchall()]

    return items, total


def query_tasks(page: int = 1, page_size: int = 20, status: str = None) -> tuple:
    """分页与状态过滤查询翻译任务列表"""
    conditions = []
    params = []

    if status:
        conditions.append("status = ?")
        params.append(status)

    where_clause = " WHERE " + " AND ".join(conditions) if conditions else ""

    count_sql = f"SELECT COUNT(*) FROM translate_task{where_clause}"

    offset = (page - 1) * page_size
    fetch_sql = f"SELECT * FROM translate_task{where_clause} ORDER BY created_at DESC LIMIT ? OFFSET ?"

    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row

        # 获取总数
        cursor = conn.execute(count_sql, params)
        total = cursor.fetchone()[0]

        # 分页获取数据
        fetch_params = params + [page_size, offset]
        cursor = conn.execute(fetch_sql, fetch_params)
        items = [dict(row) for row in cursor.fetchall()]

    return items, total


def get_jobs_stats() -> dict:
    """获取终态 Job 的计数与成功率（skipped 任务不计入分母，防除零保护）"""
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row

        cursor = conn.execute(
            """
            SELECT
                COUNT(CASE WHEN status = 'done' THEN 1 END) as done,
                COUNT(CASE WHEN status = 'skipped' THEN 1 END) as skipped,
                COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed
            FROM subtitle_job
            """
        )
        row = cursor.fetchone()

        done = row["done"] or 0
        skipped = row["skipped"] or 0
        failed = row["failed"] or 0

        total_finished = done + failed
        success_rate = round(done / total_finished, 3) if total_finished > 0 else 0.0

        return {
            "done": done,
            "skipped": skipped,
            "failed": failed,
            "success_rate": success_rate
        }


def insert_translation_error(
    task_id: str,
    batch_idx: int,
    error_reason: str,
    system_prompt: str = None,
    glossary: str = None,
    user_prompt: str = None,
    llm_response: str = None,
    llm_settings: dict = None
) -> int:
    """插入一条翻译错误诊断快照，返回新记录的 id"""
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    settings_json = json.dumps(llm_settings, ensure_ascii=False) if llm_settings is not None else None
    with sqlite3.connect(DB_PATH) as conn:
        cursor = conn.execute(
            """INSERT INTO translation_errors
               (task_id, batch_idx, error_reason, system_prompt, glossary, user_prompt, llm_response, llm_settings, created_at)
               VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)""",
            (task_id, batch_idx, error_reason, system_prompt, glossary, user_prompt, llm_response, settings_json, now)
        )
        conn.commit()
        return cursor.lastrowid


def get_translation_errors(task_id: str) -> List[Dict]:
    """查询指定任务的所有错误诊断记录，按 id 升序返回"""
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM translation_errors WHERE task_id = ? ORDER BY id ASC",
            (task_id,)
        )
        results = []
        for row in cursor.fetchall():
            record = dict(row)
            # llm_settings JSON 安全反序列化
            if record.get("llm_settings") is not None:
                try:
                    record["llm_settings"] = json.loads(record["llm_settings"])
                except (json.JSONDecodeError, TypeError):
                    pass  # JSON 损坏时保留原始字符串
            results.append(record)
        return results
