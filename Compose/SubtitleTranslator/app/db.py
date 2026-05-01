import sqlite3
import uuid
import json
import datetime
import logging
from typing import Optional, Dict, List

logger = logging.getLogger(__name__)

DB_PATH = "/app/data/queue.db"


def init_db():
    with sqlite3.connect(DB_PATH) as conn:
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
                error TEXT
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
                created_at TEXT NOT NULL,
                updated_at TEXT NOT NULL,
                error TEXT
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
        conn.execute(
            "UPDATE translate_task SET status = 'processing', updated_at = ? WHERE id = ?",
            (now, task['id'])
        )
        conn.commit()
        task['status'] = 'processing'
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


def complete_task(task_id: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE translate_task SET status = 'done', updated_at = ? WHERE id = ?",
            (now, task_id)
        )
        conn.commit()


def fail_task(task_id: str, error: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE translate_task SET status = 'failed', error = ?, updated_at = ? WHERE id = ?",
            (error, now, task_id)
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


def create_job(media_path: str) -> Optional[str]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT id FROM subtitle_job WHERE media_path = ? AND status NOT IN ('done', 'failed')",
            (media_path,)
        )
        if cursor.fetchone():
            logger.info(f"Active job already exists for {media_path}, skipping")
            return None

        job_id = str(uuid.uuid4())
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        conn.execute(
            """
            INSERT INTO subtitle_job (id, media_path, status, created_at, updated_at)
            VALUES (?, ?, 'funneling', ?, ?)
            """,
            (job_id, media_path, now, now)
        )
        conn.commit()
    logger.info(f"Created subtitle job {job_id} for {media_path}")
    return job_id


def update_job_status(job_id: str, status: str, error: str = None):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        if error:
            conn.execute(
                "UPDATE subtitle_job SET status = ?, error = ?, updated_at = ? WHERE id = ?",
                (status, error, now, job_id)
            )
        else:
            conn.execute(
                "UPDATE subtitle_job SET status = ?, updated_at = ? WHERE id = ?",
                (status, now, job_id)
            )
        conn.commit()
    logger.info(f"Job {job_id} status updated to {status}")


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

    logger.info(f"Created translate task {task_id} for job {job_id} (file: {file_path})")
    return task_id
