import time
import os
import traceback
import logging
import queue_db
from translator import translate_file, preprocess, BATCH_SIZE

logger = logging.getLogger(__name__)

def recover_tasks_on_startup():
    logger.info("Checking for interrupted tasks...")
    processing_tasks = queue_db.get_processing_tasks()
    for task in processing_tasks:
        task_id = task["id"]
        file_path = task["file_path"]
        current_batch = task.get("current_batch", 0)

        try:
            tmp_path = file_path + ".tmp"
            logger.info(f"Attempting to recover task {task_id} for {file_path}")
            
            start_batch_idx = 1
            if os.path.exists(tmp_path):
                if current_batch == 0:
                    logger.info("Recovery: .tmp exists but no batch completed. Starting from batch 1.")
                else:
                    with open(tmp_path, "r", encoding="utf-8") as f:
                        lines = f.readlines()
                    tmp_lines_count = len(lines)
                    
                    source_lines, _ = preprocess(file_path)
                    total_source_lines = len(source_lines)
                    expected_lines = min(current_batch * BATCH_SIZE, total_source_lines)
                    
                    if tmp_lines_count == expected_lines:
                        start_batch_idx = current_batch + 1
                        logger.info(f"Recovery: matches expected lines ({expected_lines}). Continuing from batch {start_batch_idx}.")
                    else:
                        safe_lines = lines[:max(0, (current_batch - 1) * BATCH_SIZE)]
                        with open(tmp_path, "w", encoding="utf-8") as f:
                            f.writelines(safe_lines)
                        start_batch_idx = current_batch
                        logger.warning(f"Recovery: mismatch! Expected {expected_lines}, got {tmp_lines_count}. Truncating to {len(safe_lines)} lines. Restarting from batch {start_batch_idx}.")
            else:
                logger.info(f"Recovery: No .tmp file found. Starting from batch 1.")
                start_batch_idx = 1
                
            translate_file(task_id, file_path, start_batch_idx=start_batch_idx)
        except Exception as e:
            logger.error(f"Recovery task {task_id} failed: {e}", exc_info=True)
            try:
                queue_db.fail_task(task_id, str(e))
            except Exception:
                pass

def consumer_loop():
    recover_tasks_on_startup()
    logger.info("Starting task consumer loop...")
    while True:
        try:
            task = queue_db.fetch_next_task()
            if not task:
                time.sleep(5)
                continue
            
            task_id = task["id"]
            file_path = task["file_path"]
            logger.info(f"Fetched task {task_id}: {file_path}")
            
            try:
                translate_file(task_id, file_path)
            except Exception as e:
                logger.error(f"Task {task_id} failed: {e}", exc_info=True)
                # translate_file internally calls queue_db.fail_task(task_id, str(e))
        except Exception as e:
            logger.error(f"Unexpected error in loop: {e}", exc_info=True)
            time.sleep(5)
