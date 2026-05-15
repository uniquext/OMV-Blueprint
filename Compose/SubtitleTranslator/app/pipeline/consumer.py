import os
import json
import time
import logging
from pathlib import Path
from typing import Dict, Optional, List

import db
from subtitle.srt_handler import rebuild_srt_with_translation
from translate.translator import translate_file, compute_resume_batch_idx
from config_loader import load_config

logger = logging.getLogger(__name__)


def recover_tasks_on_startup():
    processing_tasks = db.get_processing_tasks()
    recovered = 0
    for task in processing_tasks:
        task_id = task["id"]
        file_path = task["file_path"]
        logger.info(f"Recovering processing task {task_id} for {file_path}")
        db.reset_task_to_queued(task_id)
        recovered += 1
    logger.info(f"Startup recovery: reset {recovered} processing tasks to queued")


def process_task(task_id: str):
    """
    处理单个翻译任务，支持断点续传

    从 DB 读取任务进度，结合 .tmp 文件实际内容计算续传起始批次，
    调用 translate_file 从断点处继续翻译。
    """
    task = db.get_task(task_id)
    if not task:
        logger.error(f"Task {task_id} not found")
        return

    file_path = task["file_path"]
    current_batch = task.get("current_batch", 0) or 0
    total_batches = task.get("total_batches", 0) or 0

    config = load_config()
    batch_size = config["llm"]["batch_size"]

    tmp_path = file_path + ".tmp"
    start_batch_idx = compute_resume_batch_idx(
        current_batch=current_batch,
        total_batches=total_batches,
        tmp_path=tmp_path,
        batch_size=batch_size,
    )

    if start_batch_idx > 1:
        logger.info(f"Task {task_id}: Resuming from batch {start_batch_idx} (DB: {current_batch}/{total_batches})")

    try:
        translate_file(task_id, file_path, start_batch_idx=start_batch_idx)
    except Exception as e:
        logger.error(f"translate_file failed for task {task_id}: {e}")

    updated_task = db.get_task(task_id)
    if updated_task is None:
        return

    job = db.get_job_by_translate_task(task_id)
    if job is None:
        logger.debug(f"Task {task_id}: No associated SubtitleJob, skipping rebuild")
        return

    job_id = job["id"]

    if updated_task["status"] == "failed":
        db.update_job_status(job_id, "failed", error=updated_task.get("error", "Translation failed"))
        logger.warning(f"Job {job_id}: Marked failed (translate error)")
    elif updated_task["status"] == "done":
        try:
            rebuild_srt(job)
            cleanup_intermediate_files(job)
            db.update_job_status(job_id, "done")
            logger.info(f"Job {job_id}: Pipeline complete")
        except Exception as e:
            logger.error(f"Rebuild failed for job {job_id}: {e}")
            db.update_job_status(job_id, "failed", error=str(e))
    else:
        logger.error(f"Unexpected task status after translate_file: {updated_task['status']}")


def consumer_loop():
    logger.info("Consumer loop started")
    while True:
        task = db.fetch_next_task()
        if task is None:
            time.sleep(1)
            continue

        task_id = task["id"]
        process_task(task_id)


def rebuild_srt(job: Dict):
    job_id = job["id"]
    media_path = job["media_path"]
    media_stem = Path(media_path).stem
    media_dir = os.path.dirname(media_path)

    original_srt_path = job.get("original_srt_path")
    output_srt_path = job.get("output_srt_path") or os.path.join(media_dir, f"{media_stem}.zh.ai.srt")

    translate_task_id = job.get("translate_task_id")
    if not translate_task_id:
        raise ValueError(f"Job {job_id}: No translate_task_id, cannot rebuild")

    translate_task = db.get_task(translate_task_id)
    if not translate_task:
        raise ValueError(f"Job {job_id}: TranslateTask {translate_task_id} not found")

    file_path = translate_task["file_path"]

    zh_txt_path = _compute_zh_txt_path(file_path, media_dir, media_stem)

    if not original_srt_path:
        original_srt_path = _infer_template_srt(file_path, media_dir, media_stem)

    if not os.path.exists(original_srt_path):
        raise FileNotFoundError(f"Template SRT not found: {original_srt_path}")
    if not os.path.exists(zh_txt_path):
        raise FileNotFoundError(f"Translation text not found: {zh_txt_path}")

    db.update_job_status(job_id, "rebuilding")
    rebuild_srt_with_translation(original_srt_path, zh_txt_path, output_srt_path)
    logger.info(f"Job {job_id}: SRT rebuilt -> {output_srt_path}")


def _compute_zh_txt_path(file_path: str, media_dir: str, media_stem: str) -> str:
    if file_path.endswith(".en.txt"):
        return file_path.replace(".en.txt", ".zh.txt")
    elif file_path.endswith(".ja.txt"):
        return file_path.replace(".ja.txt", ".zh.txt")
    else:
        return file_path.replace(".txt", ".zh.txt")


def _infer_template_srt(txt_path: str, media_dir: str, media_stem: str) -> str:
    if ".emb.txt" in txt_path:
        srt_path = txt_path.replace(".emb.txt", ".emb.srt")
        if os.path.exists(srt_path):
            return srt_path

    for lang in ["en", "ja"]:
        candidate = os.path.join(media_dir, f"{media_stem}.{lang}.srt")
        if os.path.exists(candidate):
            return candidate

    return txt_path.replace(".txt", ".srt")


def cleanup_intermediate_files(job: Dict):
    cleanup_json = job.get("cleanup_files")
    if not cleanup_json:
        return

    try:
        cleanup_list = json.loads(cleanup_json) if isinstance(cleanup_json, str) else cleanup_json
    except (json.JSONDecodeError, TypeError):
        logger.warning(f"Invalid cleanup_files JSON for job {job['id']}")
        return

    for path in cleanup_list:
        if os.path.exists(path):
            try:
                os.remove(path)
                logger.debug(f"Cleaned up intermediate file: {path}")
            except OSError as e:
                logger.warning(f"Failed to clean up {path}: {e}")
