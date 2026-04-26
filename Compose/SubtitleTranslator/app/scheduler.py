import os
from apscheduler.schedulers.background import BackgroundScheduler
import logging
from scanner import scan_directory
import queue_db

logger = logging.getLogger(__name__)

SCAN_INTERVAL = int(os.environ.get("SCAN_INTERVAL", "0"))
SCAN_DIR = os.environ.get("SCAN_DIR", "/media")

scheduler = BackgroundScheduler()

def job_scan_and_enqueue():
    logger.info(f"Running scheduled scan in {SCAN_DIR}...")
    try:
        files = scan_directory(SCAN_DIR)
        enqueued_count = 0
        for file_path in files:
            try:
                queue_db.add_task(file_path)
                enqueued_count += 1
            except queue_db.TaskAlreadyExistsError:
                pass
        if enqueued_count > 0:
            logger.info(f"Found and enqueued {enqueued_count} new tasks.")
    except Exception as e:
        logger.error(f"Error during scheduled scan: {e}", exc_info=True)

def start_scheduler():
    if SCAN_INTERVAL > 0:
        logger.info(f"Starting scheduler with interval {SCAN_INTERVAL} seconds...")
        scheduler.add_job(job_scan_and_enqueue, 'interval', seconds=SCAN_INTERVAL)
        scheduler.start()
    else:
        logger.info("SCAN_INTERVAL is 0 or less. Scheduler disabled.")
