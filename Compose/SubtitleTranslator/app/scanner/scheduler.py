"""
定时扫描调度器模块

负责按配置的定时间隔触发全盘扫描，
并将扫描到的媒体文件批量推入 Layer 2 的执行层 (FunnelWorkerPool)。
"""
import logging
from typing import List
from apscheduler.schedulers.background import BackgroundScheduler

from config_loader import load_config
from scanner.media_scanner import scan_directory

logger = logging.getLogger(__name__)

# 全局 scheduler 实例
scheduler = BackgroundScheduler()

def get_scheduler() -> BackgroundScheduler:
    """获取全局调度器实例"""
    return scheduler

def job_scan_and_enqueue(worker_pool, extensions: List[str]) -> None:
    """
    定时任务：执行全盘扫描，并推入执行队列。
    跳过等待层 (DebounceMap)，直接进入 Layer 2 执行层 (FIFO Queue)。

    Args:
        worker_pool: Layer 2 的 FunnelWorkerPool 实例
        extensions: 媒体扩展名列表
    """
    config = load_config()
    scan_dir = config["pipeline"]["scan_dir"]
    logger.info(f"Running scheduled scan in {scan_dir}...")
    try:
        files = scan_directory(scan_dir, extensions)
        enqueued_count = 0
        for file_path in files:
            # 直接推入 WorkerPool 队列
            success = worker_pool.submit_job(file_path)
            if success:
                enqueued_count += 1
                
        if enqueued_count > 0:
            logger.info(f"Scheduled scan completed. Enqueued {enqueued_count} new tasks.")
        else:
            logger.info("Scheduled scan completed. No new tasks found.")
            
    except Exception as e:
        logger.error(f"Error during scheduled scan: {e}", exc_info=True)


def start_scheduler(worker_pool=None, extensions: List[str] = None) -> None:
    """
    启动定时调度器。

    Args:
        worker_pool: Layer 2 的 FunnelWorkerPool 实例
        extensions: 媒体扩展名列表
    """
    config = load_config()
    scan_interval = config["pipeline"]["scan_interval"]
    
    if scan_interval > 0:
        logger.info(f"Starting scheduler with interval {scan_interval} seconds...")

        if worker_pool is not None and extensions is not None:
            scheduler.add_job(
                job_scan_and_enqueue,
                'interval',
                seconds=scan_interval,
                args=[worker_pool, extensions]
            )
            scheduler.start()
        else:
            logger.info("No worker_pool provided, scheduler job not added.")
    else:
        logger.info("SCAN_INTERVAL is 0 or less. Scheduler disabled.")
