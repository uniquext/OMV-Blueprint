import sqlite3
import uuid
import datetime
from typing import Optional, Dict

DB_PATH = "/app/data/queue.db"

def init_db():
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute("""
            CREATE TABLE IF NOT EXISTS task (
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
        conn.commit()

class TaskAlreadyExistsError(Exception):
    pass

def add_task(file_path: str) -> str:
    with sqlite3.connect(DB_PATH) as conn:
        # Check if exists and is not done/failed
        cursor = conn.execute(
            "SELECT id FROM task WHERE file_path = ? AND status IN ('queued', 'processing')",
            (file_path,)
        )
        if cursor.fetchone():
            raise TaskAlreadyExistsError(f"Task for {file_path} is already queued or processing.")

        task_id = str(uuid.uuid4())
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        conn.execute(
            """
            INSERT INTO task (id, file_path, status, progress, current_batch, total_batches, created_at, updated_at)
            VALUES (?, ?, 'queued', '0/0', 0, 0, ?, ?)
            """,
            (task_id, file_path, now, now)
        )
        conn.commit()
    return task_id

def fetch_next_task() -> Optional[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT * FROM task WHERE status = 'queued' ORDER BY created_at ASC LIMIT 1"
        )
        row = cursor.fetchone()
        if not row:
            return None
        
        task = dict(row)
        now = datetime.datetime.now(datetime.timezone.utc).isoformat()
        conn.execute(
            "UPDATE task SET status = 'processing', updated_at = ? WHERE id = ?",
            (now, task['id'])
        )
        conn.commit()
        task['status'] = 'processing'
        return task

def update_progress(task_id: str, current_batch: int, total_batches: int, progress_text: str = ""):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE task SET current_batch = ?, total_batches = ?, progress = ?, updated_at = ? WHERE id = ?",
            (current_batch, total_batches, progress_text, now, task_id)
        )
        conn.commit()

def complete_task(task_id: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE task SET status = 'done', updated_at = ? WHERE id = ?",
            (now, task_id)
        )
        conn.commit()

def fail_task(task_id: str, error: str):
    now = datetime.datetime.now(datetime.timezone.utc).isoformat()
    with sqlite3.connect(DB_PATH) as conn:
        conn.execute(
            "UPDATE task SET status = 'failed', error = ?, updated_at = ? WHERE id = ?",
            (error, now, task_id)
        )
        conn.commit()

def get_task_by_file(file_path: str) -> Optional[Dict]:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        # Get the latest task for this file
        cursor = conn.execute(
            "SELECT * FROM task WHERE file_path = ? ORDER BY created_at DESC LIMIT 1",
            (file_path,)
        )
        row = cursor.fetchone()
        if not row:
            return None
        return dict(row)

def get_queue_stats() -> Dict:
    with sqlite3.connect(DB_PATH) as conn:
        conn.row_factory = sqlite3.Row
        cursor = conn.execute(
            "SELECT status, COUNT(*) as count FROM task GROUP BY status"
        )
        stats = {'pending': 0, 'processing': 0, 'done': 0, 'failed': 0}
        for row in cursor.fetchall():
            if row['status'] == 'queued':
                stats['pending'] = row['count']
            elif row['status'] == 'processing':
                stats['processing'] = row['count']
            elif row['status'] == 'done':
                stats['done'] = row['count']
            elif row['status'] == 'failed':
                stats['failed'] = row['count']

        cursor = conn.execute(
            "SELECT * FROM task ORDER BY created_at DESC LIMIT 50"
        )
        tasks = [dict(r) for r in cursor.fetchall()]
        
        return {
            "pending": stats["pending"],
            "processing": stats["processing"],
            "done": stats["done"],
            "failed": stats["failed"],
            "tasks": tasks
        }
