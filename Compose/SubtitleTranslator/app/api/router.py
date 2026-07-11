import os
import sys
import copy
import json
import threading
import logging
import asyncio
from typing import Optional, List, Union
from fastapi import APIRouter, HTTPException, BackgroundTasks, status, Query, Request
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field, ValidationError

from core.config_loader import load_config, atomic_write_json, _reset_config
import core.config_loader
from core.config_models import ConfigPayload, mask_api_key
from subtitle.lang_utils import normalize_language
from scanner.media_scanner import scan_directory
from api.response import api_success, api_error

debounce_map = None
worker_pool = None
_event_bus = None

logger = logging.getLogger(__name__)
router = APIRouter()

_scanning_event = threading.Event()
_config_lock = threading.Lock()


@router.get("/api/health", status_code=status.HTTP_200_OK)
async def health_endpoint():
    """健康检查端点"""
    return api_success(data={"status": "ok"})


class NotifyRequest(BaseModel):
    media_path: str = Field(..., description="媒体文件完整路径")
    language: Optional[str] = Field(None, description="字幕语言码")
    subtitle_path: Optional[str] = Field(None, description="下载到的字幕路径(辅助信息)")


@router.post("/api/notify", status_code=status.HTTP_202_ACCEPTED)
async def notify_endpoint(request: NotifyRequest):
    if not debounce_map or not worker_pool:
        raise HTTPException(status_code=503, detail={"error": "PIPELINE_NOT_READY", "message": "Pipeline not initialized"})

    media_path = request.media_path
    language = request.language

    logger.info(f"Received notify: media={media_path}, lang={language}")

    if os.path.isdir(media_path):
        raise HTTPException(status_code=400, detail={"error": "INVALID_MEDIA_PATH", "message": "media_path must be a file, not a directory"})

    config = load_config()
    extensions = [ext.lower() for ext in config["media"]["extensions"]]
    file_ext = os.path.splitext(media_path)[1].lower()
    if file_ext not in extensions:
        raise HTTPException(status_code=400, detail={"error": "UNSUPPORTED_EXTENSION", "message": f"media_path extension '{file_ext}' is not a supported media file"})

    lang_map_override = config["media"]["lang_map_override"]

    normalized_lang = normalize_language(language, lang_map_override) if language else ""

    if normalized_lang == "zh":
        worker_pool.submit_job(media_path, source="notify")
        logger.info(f"Language is zh, immediate processing: {media_path}")
        return api_success(
            data={"route": "immediate", "message": "Simplified Chinese subtitle arrived, skipping debounce layer for immediate processing"},
            message="accepted"
        )
    else:
        debounce_map.upsert(media_path, source="notify", language=language or "")
        debounce_seconds = config["pipeline"]["debounce_seconds"]
        return api_success(
            data={"route": "debounce", "message": f"Queued in debounce layer, will process after {debounce_seconds}s"},
            message="accepted"
        )


@router.post("/api/scan", status_code=status.HTTP_200_OK)
async def scan_endpoint():
    """无参全盘扫描：使用配置的 pipeline.scan_dir 路径"""
    if not worker_pool:
        raise HTTPException(status_code=503, detail={"error": "PIPELINE_NOT_READY", "message": "Pipeline not initialized"})

    if _scanning_event.is_set():
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail={"error": "SCAN_IN_PROGRESS", "message": "A scan is already in progress, please retry later"}
        )

    _scanning_event.set()
    try:
        config = load_config()
        scan_dir = config["pipeline"]["scan_dir"]
        extensions = config["media"]["extensions"]
        ignore_list_str = config["pipeline"].get("ignore_list", "")
        ignore_list = [i.strip() for i in ignore_list_str.split(",") if i.strip()]

        if not os.path.isdir(scan_dir):
            raise HTTPException(status_code=400, detail={"error": "DIRECTORY_NOT_FOUND", "message": f"Directory not found: {scan_dir}"})

        logger.info(f"Manual scan triggered for {scan_dir}")
        files = scan_directory(scan_dir, extensions, ignore_list)

        enqueued_count = 0
        for file_path in files:
            if worker_pool.submit_job(file_path, source="scan"):
                enqueued_count += 1

        if _event_bus:
            _event_bus.publish("scan_completed", {"count": enqueued_count})

        return api_success(
            data={"count": enqueued_count},
            message=f"Scanned {enqueued_count} media files, enqueued to worker pool"
        )
    finally:
        _scanning_event.clear()


@router.get("/api/browse", status_code=status.HTTP_200_OK)
async def browse_endpoint(path: Optional[str] = Query(None, description="要浏览的目录路径，默认为 scan_dir")):
    """浏览目录结构"""
    config = load_config()
    scan_dir = os.path.realpath(config["pipeline"]["scan_dir"])

    target_path = os.path.realpath(path) if path else scan_dir

    # 路径安全校验
    if not target_path.startswith(scan_dir):
        # 兼容 os.path.commonpath 校验方式
        try:
            if os.path.commonpath([target_path, scan_dir]) != scan_dir:
                raise HTTPException(status_code=400, detail={"error": "PATH_OUT_OF_BOUNDS", "message": "Target path is outside the scan directory"})
        except ValueError:
            raise HTTPException(status_code=400, detail={"error": "PATH_OUT_OF_BOUNDS", "message": "Target path is outside the scan directory"})
    
    # Python 3.9 commonpath behavior requires both paths to be on same drive (handled by ValueError above)
    # 额外精确判定：必须是 scan_dir 的子路径或本身
    if target_path != scan_dir and not target_path.startswith(scan_dir + os.sep):
        raise HTTPException(status_code=400, detail={"error": "PATH_OUT_OF_BOUNDS", "message": f"Target path '{target_path}' is outside the scan directory '{scan_dir}'"})

    if not os.path.exists(target_path):
        raise HTTPException(status_code=400, detail={"error": "PATH_NOT_FOUND", "message": "Target path does not exist"})

    if not os.path.isdir(target_path):
        raise HTTPException(status_code=400, detail={"error": "NOT_A_DIRECTORY", "message": "Target path is not a directory"})

    children = []
    try:
        entries = os.listdir(target_path)
        for entry in entries:
            # 过滤隐藏目录
            if entry.startswith("."):
                continue
            entry_path = os.path.join(target_path, entry)
            if os.path.isdir(entry_path):
                children.append({
                    "name": entry,
                    "path": entry_path
                })
        # 不区分大小写升序排列
        children.sort(key=lambda x: x["name"].lower())
    except Exception as e:
        logger.error(f"Error browsing directory {target_path}: {e}")
        raise HTTPException(status_code=500, detail={"error": "BROWSE_ERROR", "message": str(e)})

    return api_success(data={
        "path": target_path,
        "children": children
    })


# ============================================================================
# 配置管理 API
# ============================================================================

@router.get("/api/config", status_code=status.HTTP_200_OK)
async def get_config():
    """返回完整配置（api_key 脱敏）"""
    config = load_config()
    result = copy.deepcopy(config)
    # api_key 脱敏
    if "llm" in result and "api_key" in result["llm"]:
        result["llm"]["api_key"] = mask_api_key(result["llm"]["api_key"])
    return api_success(data=result)


@router.put("/api/config", status_code=status.HTTP_200_OK)
async def put_config(payload: ConfigPayload, background_tasks: BackgroundTasks):
    """
    全量更新配置

    接收完整配置 JSON → Pydantic 校验 → 原子写入 → 延迟 execv 重启
    """
    # 并发写入保护
    if not _config_lock.acquire(blocking=False):
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail={"error": "CONFIG_UPDATE_IN_PROGRESS", "message": "Another config update is in progress"}
        )

    try:
        # 如果收到的是脱敏后的 api_key，则保留并还原现有的真实 api_key
        if "••••" in payload.llm.api_key:
            current_config = load_config()
            old_key = current_config.get("llm", {}).get("api_key", "")
            if old_key:
                payload.llm.api_key = old_key
            else:
                raise HTTPException(
                    status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
                    detail={"error": "MASKED_API_KEY_RESTORE_FAILED", "message": "Cannot restore masked api_key: current api_key is empty. Please provide the actual api_key."}
                )

        # 序列化为字典（extra='ignore' 已自动过滤 app_port 等未定义字段）
        config_dict = payload.model_dump()

        # 原子写入
        atomic_write_json(core.config_loader.CONFIG_PATH, config_dict)
        logger.info("Config updated via API, scheduling restart")

        # 重置配置缓存
        _reset_config()

        # 延迟执行 execv 重启（确保 HTTP 响应先到达客户端）
        background_tasks.add_task(_do_execv_restart)

        # 注意：锁在 _do_execv_restart 中释放（execv 后进程重建，或全部重试失败后显式释放）
        return api_success(
            data={"status": "restarting"},
            message="Config written, process is restarting"
        )
    except HTTPException:
        _config_lock.release()
        raise
    except Exception as e:
        _config_lock.release()
        logger.error(f"Failed to update config: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail={"error": "CONFIG_WRITE_FAILED", "message": f"Config update failed: {str(e)}"}
        )


# ============================================================================
# Prompt 管理 API
# ============================================================================

class PromptsPayload(BaseModel):
    """PUT /api/prompts 请求体"""
    system_prompt: Optional[str] = None
    glossary: Optional[dict] = None


@router.get("/api/prompts", status_code=status.HTTP_200_OK)
async def get_prompts():
    """返回 system_prompt 和 glossary 的当前内容"""
    from core.config_loader import PROMPTS_DIR

    result = {"system_prompt": "", "glossary": {}}

    sys_prompt_path = os.path.join(PROMPTS_DIR, "system_prompt.txt")
    if os.path.exists(sys_prompt_path):
        try:
            with open(sys_prompt_path, "r", encoding="utf-8") as f:
                result["system_prompt"] = f.read()
        except Exception as e:
            logger.error(f"Failed to read system_prompt: {e}")

    glossary_path = os.path.join(PROMPTS_DIR, "glossary.json")
    if os.path.exists(glossary_path):
        try:
            with open(glossary_path, "r", encoding="utf-8") as f:
                result["glossary"] = json.load(f)
        except Exception as e:
            logger.error(f"Failed to read glossary: {e}")

    return api_success(data=result)


@router.put("/api/prompts", status_code=status.HTTP_200_OK)
async def put_prompts(payload: PromptsPayload):
    """
    更新 Prompt 文件

    支持部分更新（只传 system_prompt 或只传 glossary），即时生效无需重启。
    """
    import json as json_module
    from core.config_loader import PROMPTS_DIR, atomic_write_text, atomic_write_json

    if payload.system_prompt is None and payload.glossary is None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail={"error": "EMPTY_PROMPTS_PAYLOAD", "message": "At least one of 'system_prompt' or 'glossary' must be provided"}
        )

    if payload.glossary is not None and not isinstance(payload.glossary, dict):
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail={"error": "INVALID_GLOSSARY_TYPE", "message": "glossary must be a JSON object"}
        )

    if not os.path.exists(PROMPTS_DIR):
        os.makedirs(PROMPTS_DIR, exist_ok=True)

    updated = []

    if payload.system_prompt is not None:
        sys_prompt_path = os.path.join(PROMPTS_DIR, "system_prompt.txt")
        atomic_write_text(sys_prompt_path, payload.system_prompt)
        updated.append("system_prompt")
        logger.info("system_prompt updated via API")

    if payload.glossary is not None:
        glossary_path = os.path.join(PROMPTS_DIR, "glossary.json")
        atomic_write_json(glossary_path, payload.glossary)
        updated.append("glossary")
        logger.info("glossary updated via API")

    return api_success(
        data={"updated": updated},
        message=f"Updated: {', '.join(updated)}"
    )


def _do_execv_restart():
    """
    执行 os.execv() 重启进程

    - 先优雅关闭 WorkerPool（最多等 5 秒）
    - 关闭所有 FileHandler 防止 fd 泄漏
    - 最多重试 3 次
    - 全部失败后记录 CRITICAL 日志并释放锁
    """
    import time

    time.sleep(1)  # 等待 HTTP 响应到达客户端

    # 优雅关闭 WorkerPool，等待当前 SQLite 事务完成
    if worker_pool is not None:
        try:
            logger.info("Shutting down WorkerPool before execv...")
            worker_pool.shutdown(wait=True, timeout=5)
            logger.info("WorkerPool shutdown complete")
        except Exception as e:
            logger.warning(f"WorkerPool shutdown failed (continuing with execv): {e}")

    # 最多重试 3 次
    for attempt in range(3):
        logger.info(f"Executing os.execv() restart (attempt {attempt + 1}/3)")

        # 必须在日志输出后关闭 FileHandler，否则日志写入会导致 handler 重新打开文件
        root_logger = logging.getLogger()
        for handler in root_logger.handlers:
            if isinstance(handler, logging.FileHandler):
                try:
                    handler.close()
                except Exception:
                    pass

        try:
            os.execv(sys.executable, [sys.executable] + sys.argv)
        except Exception as e:
            logger.error(f"execv attempt {attempt + 1} failed: {e}")
            if attempt < 2:
                time.sleep(1)

    # 所有重试失败
    logger.critical("All execv attempts failed. Process continues with old config in memory.")
    _config_lock.release()

# ============================================================================
# Dashboard 实时状态 API
# ============================================================================

@router.get("/api/stats", status_code=status.HTTP_200_OK)
async def get_stats_api():
    if not debounce_map or not worker_pool:
        raise HTTPException(status_code=503, detail={"error": "PIPELINE_NOT_READY", "message": "Pipeline not initialized"})

    from core import db
    stats = db.get_stats()

    # 时钟负数耗时异常时，向 EventBus 组播发布 timing_anomaly_detected SSE 事件
    if stats.get("has_timing_anomaly") and _event_bus:
        _event_bus.publish("timing_anomaly_detected", {"message": "检测到时钟异常或耗时数据为负数，可能存在系统时钟回拨！"})

    # 内存快照
    debounce_snapshot = debounce_map.snapshot()
    pool_snapshot = worker_pool.snapshot()

    # 获取 EventBus 的最新 snapshot_id
    snapshot_id = 0
    if _event_bus:
        snapshot_id = _event_bus.get_snapshot_id()

    return api_success(data={
        **stats,
        "debouncing": debounce_snapshot.get("pending_count", 0),
        "queued": pool_snapshot.get("queue_size", 0),
        "active_workers": pool_snapshot.get("active_workers", 0),
        "scanning": _scanning_event.is_set(),
        "snapshot_id": snapshot_id
    })


@router.get("/api/queue/debouncing", status_code=status.HTTP_200_OK)
async def get_queue_debouncing():
    if not debounce_map:
        raise HTTPException(status_code=503, detail={"error": "PIPELINE_NOT_READY", "message": "Pipeline not initialized"})
    snapshot = debounce_map.snapshot()
    return api_success(data=snapshot.get("entries", []))

@router.get("/api/queue/pending", status_code=status.HTTP_200_OK)
async def get_queue_pending():
    if not worker_pool:
        raise HTTPException(status_code=503, detail={"error": "PIPELINE_NOT_READY", "message": "Pipeline not initialized"})
    snapshot = worker_pool.snapshot()
    return api_success(data={
        "fifo_size": snapshot.get("queue_size", 0),
        "active_workers": snapshot.get("active_workers", 0),
        "items": snapshot.get("items", [])
    })

@router.get("/api/jobs", status_code=status.HTTP_200_OK)
async def get_jobs_api(
    page: int = Query(1, ge=1, description="页码"),
    page_size: int = Query(20, ge=1, le=100, description="每页容量"),
    status: Optional[List[str]] = Query(None, description="状态多选"),
    funnel_type: Optional[int] = Query(None, description="漏斗类型"),
    date_from: Optional[str] = Query(None, description="起始时间 ISO 格式"),
    date_to: Optional[str] = Query(None, description="结束时间 ISO 格式"),
    sort_dir: str = Query("desc", description="排序方向"),
    is_deleted: Optional[int] = Query(0, description="是否删除")
):
    from core import db
    try:
        items, total = db.query_jobs(
            page=page,
            page_size=page_size,
            status=status,
            funnel_type=funnel_type,
            date_from=date_from,
            date_to=date_to,
            sort_dir=sort_dir,
            is_deleted=is_deleted
        )
        return api_success(data={
            "items": items,
            "total": total,
            "page": page,
            "page_size": page_size
        })
    except Exception as e:
        logger.error(f"Error querying jobs: {e}")
        return api_error(code=500, message=f"Failed to query jobs: {e}")


@router.get("/api/tasks", status_code=status.HTTP_200_OK)
async def get_tasks_api(
    page: int = Query(1, ge=1, description="页码"),
    page_size: int = Query(20, ge=1, le=100, description="每页容量"),
    status: Optional[str] = Query(None, description="任务状态")
):
    from core import db
    try:
        items, total = db.query_tasks(
            page=page,
            page_size=page_size,
            status=status
        )
        return api_success(data={
            "items": items,
            "total": total,
            "page": page,
            "page_size": page_size
        })
    except Exception as e:
        logger.error(f"Error querying tasks: {e}")
        return api_error(code=500, message=f"Failed to query tasks: {e}")


@router.get("/api/jobs/stats", status_code=status.HTTP_200_OK)
async def get_jobs_stats_api():
    from core import db
    try:
        stats = db.get_jobs_stats()
        return api_success(data=stats)
    except Exception as e:
        logger.error(f"Error getting jobs stats: {e}")
        return api_error(code=500, message=f"Failed to get jobs stats: {e}")


@router.get("/api/tasks/{task_id}/errors", status_code=status.HTTP_200_OK)
async def get_task_errors(task_id: str):
    """获取指定翻译任务的错误诊断快照"""
    from core import db
    task = db.get_task(task_id)
    if not task:
        return api_error(code=404, message="Task not found")
    errors = db.get_translation_errors(task_id)
    return api_success(data=errors)


@router.get("/api/logs", status_code=status.HTTP_200_OK)
async def get_logs_api(
    lines: int = Query(200, ge=1, le=2000, description="倒数读取的行数")
):
    from collections import deque
    log_dir = os.environ.get("LOG_DIR", "/app/logs")
    log_file = os.path.join(log_dir, "subtitle_translator.log")

    if not os.path.exists(log_file):
        return api_success(data={
            "content": "No log file found",
            "lines": 0
        })

    try:
        with open(log_file, "r", encoding="utf-8", errors="replace") as f:
            log_lines = deque(f, lines)
            content = "".join(log_lines)
            actual_lines = len(log_lines)

        return api_success(data={
            "content": content,
            "lines": actual_lines
        })
    except Exception as e:
        logger.error(f"Error reading log file: {e}")
        return api_success(data={
            "content": f"Error reading log file: {e}",
            "lines": 0
        })


SSE_HEARTBEAT_TIMEOUT = 30

@router.get("/api/events")
async def sse_events(request: Request):
    if not _event_bus:
        raise HTTPException(status_code=503, detail="EventBus not initialized")

    async def event_generator():
        queue = _event_bus.subscribe()
        try:
            while True:
                if await request.is_disconnected():
                    break

                try:
                    event = await asyncio.wait_for(queue.get(), timeout=SSE_HEARTBEAT_TIMEOUT)
                    event_type = event["type"]
                    payload = event["payload"]

                    yield f"data: {json.dumps({'type': event_type, **payload})}\n\n"

                except asyncio.TimeoutError:
                    # Send heartbeat
                    yield ":ping\n\n"
        finally:
            _event_bus.unsubscribe(queue)

    return StreamingResponse(event_generator(), media_type="text/event-stream")


@router.post("/api/tasks/{task_id}/retry", status_code=status.HTTP_200_OK)
async def retry_task_endpoint(task_id: str):
    """手动重试带有跳过批次的失败任务"""
    from core import db
    
    task = db.get_task(task_id)
    if not task:
        raise HTTPException(status_code=404, detail={"error": "TASK_NOT_FOUND", "message": f"Task {task_id} not found"})
        
    skipped = db.get_task_skipped_batches(task_id)
    if not skipped:
        raise HTTPException(status_code=400, detail={"error": "NO_SKIPPED_BATCHES", "message": f"Task {task_id} has no skipped batches to retry"})
        
    try:
        db.reset_task_for_retry(task_id)
        
        # Update associated job status
        job = db.get_job_by_translate_task(task_id)
        if job:
            db.update_job_status(job["id"], "translating")
            if _event_bus:
                _event_bus.publish({
                    "type": "job_updated",
                    "payload": {
                        "job_id": job["id"],
                        "status": "translating"
                    }
                })
            
        logger.info(f"Task {task_id} manually queued for precise retry")
        return api_success(message=f"Task {task_id} queued for retry")
    except Exception as e:
        logger.error(f"Error queuing task for retry: {e}")
        return api_error(code=500, message=str(e))


@router.post("/api/tasks/{task_id}/ignore-complete", status_code=status.HTTP_200_OK)
async def ignore_complete_task(task_id: str):
    """忽略未完成批次，直接强制设为完成"""
    from core import db
    task = db.get_task(task_id)
    if not task:
        raise HTTPException(status_code=404, detail={"error": "TASK_NOT_FOUND", "message": f"Task {task_id} not found"})
    try:
        db.force_complete_task(task_id)
        return api_success(message=f"Task {task_id} marked as done")
    except Exception as e:
        logger.error(f"Error ignoring task: {e}")
        return api_error(code=500, message=str(e))


@router.post("/api/jobs/{job_id}/delete", status_code=status.HTTP_200_OK)
async def delete_job(job_id: str):
    """软删除任务对应的 job"""
    from core import db
    # check if job exists (can use get_jobs with id? or just call soft_delete_job and catch)
    try:
        db.soft_delete_job(job_id)
        return api_success(message=f"Job {job_id} marked as deleted")
    except Exception as e:
        logger.error(f"Error deleting job: {e}")
        return api_error(code=500, message=str(e))


@router.get("/api/tasks/{task_id}/preview", status_code=status.HTTP_200_OK)
async def preview_task(task_id: str):
    """获取任务当前的翻译原文与译文快照对照"""
    from core import db
    import os
    from translate.translator import preprocess
    
    task = db.get_task(task_id)
    if not task:
        raise HTTPException(status_code=404, detail={"error": "TASK_NOT_FOUND", "message": f"Task {task_id} not found"})
        
    source_path = task["file_path"]
    if not os.path.exists(source_path):
        return api_error(code=404, message="Source file not found")
        
    try:
        formatted_lines, _ = preprocess(source_path)
    except Exception as e:
        logger.error(f"Error preprocessing source file {source_path}: {e}")
        return api_error(code=500, message="Failed to parse source file")
        
    original_lines = {}
    for line in formatted_lines:
        parts = line.split("|", 1)
        if len(parts) == 2:
            id_part = parts[0].replace("ID:", "").strip()
            if id_part.isdigit():
                original_lines[int(id_part)] = parts[1].strip()
                
    tmp_path = f"{source_path}.tmp"
    translated_map = {}
    if os.path.exists(tmp_path):
        try:
            with open(tmp_path, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    parts = line.split("|", 1)
                    if len(parts) == 2:
                        id_part = parts[0].replace("ID:", "").strip()
                        if id_part.isdigit():
                            translated_map[int(id_part)] = parts[1].strip()
        except Exception as e:
            logger.warning(f"Error reading tmp file {tmp_path}: {e}")
            
    lines = []
    for line_id, text in original_lines.items():
        lines.append({
            "id": line_id,
            "original": text,
            "translated": translated_map.get(line_id, "")
        })
        
    return api_success(data={"lines": lines})
