import os
import threading
import logging
from typing import Optional
from fastapi import APIRouter, HTTPException, status
from pydantic import BaseModel, Field

from config_loader import load_config
from subtitle.lang_utils import normalize_language
from scanner.media_scanner import scan_directory

debounce_map = None
worker_pool = None

logger = logging.getLogger(__name__)
router = APIRouter()

_scan_lock = threading.Lock()


class NotifyRequest(BaseModel):
    media_path: str = Field(..., description="媒体文件完整路径")
    language: Optional[str] = Field(None, description="字幕语言码")
    subtitle_path: Optional[str] = Field(None, description="下载到的字幕路径(辅助信息)")


class ScanRequest(BaseModel):
    dir_path: str = Field(..., description="扫描根目录")


@router.post("/notify", status_code=status.HTTP_202_ACCEPTED)
async def notify_endpoint(request: NotifyRequest):
    if not debounce_map or not worker_pool:
        raise HTTPException(status_code=503, detail="Pipeline not initialized")

    media_path = request.media_path
    language = request.language

    logger.info(f"Received notify: media={media_path}, lang={language}")

    if os.path.isdir(media_path):
        raise HTTPException(status_code=400, detail="media_path must be a file, not a directory")

    config = load_config()
    extensions = [ext.lower() for ext in config["media"]["extensions"]]
    file_ext = os.path.splitext(media_path)[1].lower()
    if file_ext not in extensions:
        raise HTTPException(status_code=400, detail=f"media_path extension '{file_ext}' is not a supported media file")

    lang_map_override = config["media"]["lang_map_override"]

    normalized_lang = normalize_language(language, lang_map_override) if language else ""

    if normalized_lang == "zh":
        worker_pool.submit_job(media_path)
        logger.info(f"Language is zh, immediate processing: {media_path}")
        return {
            "status": "accepted",
            "route": "immediate",
            "message": "简中字幕已到达，跳过等待层直接处理"
        }
    else:
        debounce_map.upsert(media_path, source="notify", language=language or "")
        debounce_seconds = config["pipeline"]["debounce_seconds"]
        return {
            "status": "accepted",
            "route": "debounce",
            "message": f"已进入等待层，将在 {debounce_seconds} 秒后处理"
        }


@router.post("/scan", status_code=status.HTTP_200_OK)
async def scan_endpoint(request: ScanRequest):
    if not worker_pool:
        raise HTTPException(status_code=503, detail="Pipeline not initialized")

    if not _scan_lock.acquire(blocking=False):
        raise HTTPException(
            status_code=status.HTTP_409_CONFLICT,
            detail={"error": "Scan already in progress", "message": "上次扫描尚未完成，请稍后重试"}
        )

    try:
        config = load_config()
        scan_dir = request.dir_path
        extensions = config["media"]["extensions"]

        if not os.path.isdir(scan_dir):
            raise HTTPException(status_code=400, detail=f"Directory not found: {scan_dir}")

        logger.info(f"Manual scan triggered for {scan_dir}")
        files = scan_directory(scan_dir, extensions)

        enqueued_count = 0
        for file_path in files:
            if worker_pool.submit_job(file_path):
                enqueued_count += 1

        return {
            "count": enqueued_count,
            "message": f"已扫描到 {enqueued_count} 个待处理媒体文件，已加入执行层队列"
        }
    finally:
        _scan_lock.release()
