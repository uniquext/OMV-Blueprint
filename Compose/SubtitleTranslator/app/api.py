import os
import sys
import copy
import json
import threading
import logging
from typing import Optional
from fastapi import APIRouter, HTTPException, BackgroundTasks, status
from pydantic import BaseModel, Field, ValidationError

from config_loader import load_config, atomic_write_json, _reset_config
import config_loader
from config_models import ConfigPayload, mask_api_key
from subtitle.lang_utils import normalize_language
from scanner.media_scanner import scan_directory

debounce_map = None
worker_pool = None

logger = logging.getLogger(__name__)
router = APIRouter()

_scan_lock = threading.Lock()
_config_lock = threading.Lock()


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
    return result


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
            detail="Another config update is in progress"
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
                    detail="Cannot restore masked api_key: current api_key is empty. Please provide the actual api_key."
                )

        # 序列化为字典（extra='ignore' 已自动过滤 app_port 等未定义字段）
        config_dict = payload.model_dump()

        # 原子写入
        atomic_write_json(config_loader.CONFIG_PATH, config_dict)
        logger.info("Config updated via API, scheduling restart")

        # 重置配置缓存
        _reset_config()

        # 延迟执行 execv 重启（确保 HTTP 响应先到达客户端）
        background_tasks.add_task(_do_execv_restart)

        return {
            "status": "restarting",
            "message": "配置已写入，进程即将重启"
        }
    except Exception as e:
        _config_lock.release()
        logger.error(f"Failed to update config: {e}")
        raise HTTPException(
            status_code=status.HTTP_500_INTERNAL_SERVER_ERROR,
            detail=f"Config update failed: {str(e)}"
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
    from config_loader import PROMPTS_DIR

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

    return result


@router.put("/api/prompts", status_code=status.HTTP_200_OK)
async def put_prompts(payload: PromptsPayload):
    """
    更新 Prompt 文件

    支持部分更新（只传 system_prompt 或只传 glossary），即时生效无需重启。
    """
    import json as json_module
    from config_loader import PROMPTS_DIR, atomic_write_text, atomic_write_json

    if payload.system_prompt is None and payload.glossary is None:
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail="At least one of 'system_prompt' or 'glossary' must be provided"
        )

    # glossary 类型校验（必须是 dict）
    if payload.glossary is not None and not isinstance(payload.glossary, dict):
        raise HTTPException(
            status_code=status.HTTP_422_UNPROCESSABLE_ENTITY,
            detail="glossary must be a JSON object"
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

    return {
        "status": "updated",
        "updated": updated,
        "message": f"已更新: {', '.join(updated)}"
    }


def _do_execv_restart():
    """
    执行 os.execv() 重启进程

    - 先关闭所有 FileHandler 防止 fd 泄漏
    - 最多重试 3 次
    - 全部失败后记录 CRITICAL 日志并释放锁
    """
    import time

    time.sleep(1)  # 等待 HTTP 响应到达客户端

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

