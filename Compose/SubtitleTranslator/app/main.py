import os
import sys
import json
import logging
from logging.handlers import RotatingFileHandler
import threading
from contextlib import asynccontextmanager
from fastapi import FastAPI
import uvicorn

import db
from config_loader import load_config
import api
from pipeline.debounce_queue import DebounceMap, FunnelWorkerPool, start_debounce_scheduler
from pipeline.consumer import consumer_loop, recover_tasks_on_startup
from scanner.watchdog_monitor import start_watchdog
from scanner.scheduler import start_scheduler
from scanner.media_scanner import scan_directory


def setup_logging():
    log_dir = os.environ.get("LOG_DIR", "/app/logs")
    
    logger = logging.getLogger()
    logger.setLevel(logging.INFO)
    # 清理已有的 handlers 防止重复注册
    logger.handlers.clear()
    
    formatter = logging.Formatter('%(asctime)s - %(name)s - %(levelname)s - %(message)s')

    # 1. 始终添加 stdout 控制台日志
    stream_handler = logging.StreamHandler(sys.stdout)
    stream_handler.setFormatter(formatter)
    logger.addHandler(stream_handler)

    # 2. 尝试添加文件日志
    try:
        os.makedirs(log_dir, exist_ok=True)
        log_file = os.path.join(log_dir, "subtitle_translator.log")
        file_handler = RotatingFileHandler(log_file, maxBytes=10*1024*1024, backupCount=5)
        file_handler.setFormatter(formatter)
        logger.addHandler(file_handler)
    except OSError:
        pass

setup_logging()
logger = logging.getLogger(__name__)

debounce_map = None
worker_pool = None
watchdog_observer = None


def _cleanup_residual_intermediate_files():
    config = load_config()
    scan_dir = config["pipeline"]["scan_dir"]
    cleaned = 0

    protected_paths = set()
    translating_jobs = db.get_jobs_by_status("translating")
    for job in translating_jobs:
        translate_task_id = job.get("translate_task_id")
        if translate_task_id:
            task = db.get_task(translate_task_id)
            if task:
                file_path = task["file_path"]
                protected_paths.add(file_path)
                if file_path.endswith(".en.txt"):
                    protected_paths.add(file_path.replace(".en.txt", ".zh.txt"))
                elif file_path.endswith(".ja.txt"):
                    protected_paths.add(file_path.replace(".ja.txt", ".zh.txt"))
                elif file_path.endswith(".emb.txt"):
                    protected_paths.add(file_path.replace(".txt", ".zh.txt"))
                else:
                    protected_paths.add(file_path.replace(".txt", ".zh.txt"))
        original_srt = job.get("original_srt_path")
        if original_srt:
            protected_paths.add(original_srt)

    for root, dirs, files in os.walk(scan_dir):
        dirs[:] = [d for d in dirs if not d.startswith('.')]
        for f in files:
            if f.endswith((".emb.srt", ".emb.txt", ".en.txt", ".ja.txt", ".zh.txt")):
                tmp_file = os.path.join(root, f)
                if tmp_file in protected_paths:
                    logger.debug(f"Skipping protected file for translating job: {tmp_file}")
                    continue
                try:
                    os.remove(tmp_file)
                    logger.info(f"Cleaned up residual intermediate file: {tmp_file}")
                    cleaned += 1
                except OSError as e:
                    logger.warning(f"Failed to clean up {tmp_file}: {e}")
    return cleaned


def recover_startup_jobs():
    logger.info("Running startup recovery...")

    recover_tasks_on_startup()

    failed_count = 0
    statuses_to_fail = ["funneling", "extracting", "rebuilding"]

    for s in statuses_to_fail:
        jobs = db.get_jobs_by_status(s)
        for job in jobs:
            job_id = job["id"]
            media_path = job["media_path"]
            logger.info(f"Failing interrupted job {job_id} for {media_path} (was {s})")
            db.update_job_status(job_id, "failed", "Interrupted by system restart")

            cleanup_json = job.get("cleanup_files")
            if cleanup_json:
                try:
                    cleanup_list = json.loads(cleanup_json) if isinstance(cleanup_json, str) else cleanup_json
                    for path in cleanup_list:
                        if os.path.exists(path):
                            try:
                                os.remove(path)
                                logger.info(f"Cleaned up intermediate file: {path}")
                            except OSError as e:
                                logger.warning(f"Failed to clean up {path}: {e}")
                except (json.JSONDecodeError, TypeError):
                    logger.warning(f"Invalid cleanup_files JSON for job {job_id}")
            failed_count += 1

    cleaned = _cleanup_residual_intermediate_files()
    logger.info(f"Startup recovery complete. Marked {failed_count} jobs as failed, cleaned {cleaned} residual files.")


@asynccontextmanager
async def lifespan(app: FastAPI):
    global debounce_map, worker_pool, watchdog_observer

    config = load_config()
    db.init_db()

    recover_startup_jobs()

    funnel_workers = config["pipeline"]["funnel_workers"]
    debounce_seconds = config["pipeline"]["debounce_seconds"]
    extensions = config["media"]["extensions"]
    lang_map_override = config["media"]["lang_map_override"]

    debounce_map = DebounceMap(debounce_seconds=debounce_seconds, lang_map_override=lang_map_override)
    worker_pool = FunnelWorkerPool(max_workers=funnel_workers, debounce_map=debounce_map)

    poll_interval = config["pipeline"]["debounce_poll_interval"]
    start_debounce_scheduler(debounce_map, worker_pool, interval=poll_interval)

    api.debounce_map = debounce_map
    api.worker_pool = worker_pool

    consumer_thread = threading.Thread(target=consumer_loop, daemon=True)
    consumer_thread.start()
    logger.info("Consumer thread started")

    watchdog_enabled = config["watchdog"]["enabled"]
    watchdog_path = config["watchdog"]["path"]

    if watchdog_enabled:
        watchdog_observer = start_watchdog(watchdog_path, debounce_map, extensions)
    else:
        logger.info("Watchdog disabled by config")

    start_scheduler(worker_pool, extensions)

    scan_dir = config["pipeline"]["scan_dir"]
    try:
        files = scan_directory(scan_dir, extensions)
        if files:
            logger.info(f"Startup scan found {len(files)} files to process")
            for f in files:
                worker_pool.submit_job(f)
    except Exception as e:
        logger.error(f"Startup scan failed: {e}")

    yield

    logger.info("Shutting down...")
    if watchdog_observer:
        watchdog_observer.stop()
        watchdog_observer.join()
    worker_pool.shutdown()

app = FastAPI(title="SubtitleTranslator API", lifespan=lifespan)
app.include_router(api.router)


@app.get("/health")
async def health():
    return {"status": "ok"}


if __name__ == "__main__":
    config = load_config()
    uvicorn.run("main:app", host="0.0.0.0", port=int(config["app_port"]), reload=False)
