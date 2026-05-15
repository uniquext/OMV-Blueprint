import os
import json
import logging
import tempfile
from typing import Dict, Any

logger = logging.getLogger(__name__)

CONFIG_PATH = "/app/config/config.json"


def atomic_write_json(path: str, data: Any) -> None:
    """
    原子写入 JSON 文件

    使用 tempfile + fsync + os.replace() 模式确保写入原子性。
    如果写入过程中发生异常，原文件内容不受影响。
    """
    dir_name = os.path.dirname(path) or "."
    fd = None
    tmp_path = None
    try:
        fd, tmp_path = tempfile.mkstemp(prefix=".tmp", dir=dir_name)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            fd = None  # os.fdopen 接管了 fd，后续不需要再 close
            json.dump(data, f, indent=4, ensure_ascii=False)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp_path, path)
        tmp_path = None  # replace 成功，不需要清理
    except Exception:
        # 清理临时文件
        if tmp_path and os.path.exists(tmp_path):
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
        if fd is not None:
            try:
                os.close(fd)
            except OSError:
                pass
        raise


def atomic_write_text(path: str, content: str) -> None:
    """
    原子写入文本文件

    使用 tempfile + fsync + os.replace() 模式确保写入原子性。
    如果写入过程中发生异常，原文件内容不受影响。
    """
    dir_name = os.path.dirname(path) or "."
    fd = None
    tmp_path = None
    try:
        fd, tmp_path = tempfile.mkstemp(prefix=".tmp", dir=dir_name)
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            fd = None  # os.fdopen 接管了 fd
            f.write(content)
            f.flush()
            os.fsync(f.fileno())
        os.replace(tmp_path, path)
        tmp_path = None
    except Exception:
        if tmp_path and os.path.exists(tmp_path):
            try:
                os.unlink(tmp_path)
            except OSError:
                pass
        if fd is not None:
            try:
                os.close(fd)
            except OSError:
                pass
        raise

DEFAULTS = {
    "llm": {
        "api_url": "",
        "api_key": "",
        "model": "",
        "model_type": "chat",
        "temperature": 0.3,
        "timeout": 120,
        "batch_size": 20,
        "context_size": 5,
        "rpm_limit": 1000,
        "tpm_limit": 50000,
        "max_retries": 3
    },
    "pipeline": {
        "debounce_seconds": 60,
        "debounce_poll_interval": 10,
        "funnel_workers": 5,
        "scan_interval": 0,
        "scan_dir": "/media"
    },
    "media": {
        "extensions": [".mkv", ".mp4", ".avi", ".wmv", ".flv", ".ts", ".m2ts"],
        "lang_map_override": {}
    },
    "watchdog": {
        "enabled": True,
        "path": "/media"
    }
}

import copy

def _merge_config(default: Dict, user: Dict) -> Dict:
    """递归合并配置字典"""
    result = copy.deepcopy(default)
    for key, value in user.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = _merge_config(result[key], value)
        elif key in result:
            # 仅合入 DEFAULTS 中已定义的顶层键
            result[key] = value
        # 未在 DEFAULTS 中定义的顶层键（如 app_port）被丢弃
    return result

_config = None

def _reset_config():
    """仅供测试使用：重置配置缓存"""
    global _config
    _config = None

def load_config() -> Dict[str, Any]:
    """
    加载配置主流程 (带缓存):
    1. 如果已经加载过，直接返回。
    2. 如果 config.json 不存在，按 DEFAULTS 生成并原子写入。
    3. 读取 config.json 并与 DEFAULTS 合并。
    config.json 为唯一配置来源（app_port 除外，仅通过环境变量 APP_PORT）。
    """
    global _config
    if _config is not None:
        return _config

    config_dir = os.path.dirname(CONFIG_PATH)
    if config_dir and not os.path.exists(config_dir):
        os.makedirs(config_dir, exist_ok=True)

    if not os.path.exists(CONFIG_PATH):
        logger.info(f"Config file not found, creating default at {CONFIG_PATH}")
        config = copy.deepcopy(DEFAULTS)
        try:
            atomic_write_json(CONFIG_PATH, config)
            logger.info(f"Default config saved to {CONFIG_PATH}")
        except Exception as e:
            logger.error(f"Failed to save default config file: {e}")
    else:
        try:
            with open(CONFIG_PATH, "r", encoding="utf-8") as f:
                user_config = json.load(f)
            config = _merge_config(DEFAULTS, user_config)
        except Exception as e:
            logger.error(f"Failed to read config file: {e}")
            config = copy.deepcopy(DEFAULTS)

    # 必填项校验（仅记录 error 日志，不退出进程）
    if not config["llm"].get("api_url") or not config["llm"].get("api_key") or not config["llm"].get("model"):
        logger.error("LLM api_url, api_key and model are not configured. Translation will not work until configured via config.json or API.")

    # 自动生成 prompts
    _ensure_prompts(config)

    _config = config
    return _config

PROMPTS_DIR = "/app/prompts"

DEFAULT_SYSTEM_PROMPT = """你是一个专业的字幕翻译助手。
你的任务是将输入的字幕文本翻译成简体中文。
请保持翻译的自然、流畅，并符合中文母语使用者的习惯。
如果遇到专有名词，请参考提供的术语表。
保持输出格式与输入一致：ID: n | 翻译后的文本
"""

def _ensure_prompts(config: Dict):
    """自动生成默认的 prompt 文件（使用原子写入）"""
    if not os.path.exists(PROMPTS_DIR):
        os.makedirs(PROMPTS_DIR, exist_ok=True)
    
    sys_prompt_path = os.path.join(PROMPTS_DIR, "system_prompt.txt")
    if not os.path.exists(sys_prompt_path):
        logger.info(f"Generating default system prompt at {sys_prompt_path}")
        atomic_write_text(sys_prompt_path, DEFAULT_SYSTEM_PROMPT)
            
    glossary_path = os.path.join(PROMPTS_DIR, "glossary.json")
    if not os.path.exists(glossary_path):
        logger.info(f"Generating default glossary at {glossary_path}")
        atomic_write_json(glossary_path, {})

