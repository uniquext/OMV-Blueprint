import os
import json
import logging
from typing import Dict, Any

logger = logging.getLogger(__name__)

CONFIG_PATH = "/app/config/config.json"

DEFAULTS = {
    "app_port": 9800,
    "llm": {
        "api_url": "",
        "api_key": "",
        "model": "",
        "model_type": "chat",
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

ENV_OVERRIDES = {
    "APP_PORT": ("app_port", int),
    "LLM_API_URL": ("llm.api_url", str),
    "LLM_API_KEY": ("llm.api_key", str),
    "LLM_MODEL": ("llm.model", str),
    "LLM_MODEL_TYPE": ("llm.model_type", str),
    "LLM_TIMEOUT": ("llm.timeout", int),
    "BATCH_SIZE": ("llm.batch_size", int),
    "CONTEXT_SIZE": ("llm.context_size", int),
    "RPM_LIMIT": ("llm.rpm_limit", int),
    "TPM_LIMIT": ("llm.tpm_limit", int),
    "MAX_RETRIES": ("llm.max_retries", int),
    "DEBOUNCE_SECONDS": ("pipeline.debounce_seconds", int),
    "DEBOUNCE_POLL_INTERVAL": ("pipeline.debounce_poll_interval", int),
    "FUNNEL_WORKERS": ("pipeline.funnel_workers", int),
    "SCAN_INTERVAL": ("pipeline.scan_interval", int),
    "SCAN_DIR": ("pipeline.scan_dir", str),
    "MEDIA_EXTENSIONS": ("media.extensions", lambda x: [ext.strip() for ext in x.split(",")]),
    "LANG_MAP_OVERRIDE": ("media.lang_map_override", json.loads),
    "WATCHDOG_ENABLED": ("watchdog.enabled", lambda x: x.lower() == "true"),
    "WATCHDOG_PATH": ("watchdog.path", str),
}

def _set_nested_value(config: Dict, path: str, value: Any):
    """根据点号分隔的路径设置嵌套字典的值"""
    keys = path.split(".")
    for key in keys[:-1]:
        config = config.setdefault(key, {})
    config[keys[-1]] = value

def _apply_env_overrides(config: Dict) -> bool:
    """从环境变量覆盖配置，如果发生了覆盖则返回 True"""
    changed = False
    for env_key, (config_path, converter) in ENV_OVERRIDES.items():
        val = os.environ.get(env_key)
        if val is not None and val != "":
            try:
                converted_val = converter(val)
                _set_nested_value(config, config_path, converted_val)
                changed = True
            except Exception as e:
                logger.error(f"Failed to convert env {env_key}={val}: {e}")
    return changed

import copy

def _merge_config(default: Dict, user: Dict) -> Dict:
    """递归合并配置字典"""
    result = copy.deepcopy(default)
    for key, value in user.items():
        if key in result and isinstance(result[key], dict) and isinstance(value, dict):
            result[key] = _merge_config(result[key], value)
        else:
            result[key] = value
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
    2. 如果 config.json 不存在，按 DEFAULTS 生成。
    3. 读取 config.json 并应用覆盖。
    """
    global _config
    if _config is not None:
        return _config

    config_dir = os.path.dirname(CONFIG_PATH)
    if not os.path.exists(config_dir):
        os.makedirs(config_dir, exist_ok=True)

    if not os.path.exists(CONFIG_PATH):
        logger.info(f"Config file not found, creating default at {CONFIG_PATH}")
        config = copy.deepcopy(DEFAULTS)
    else:
        try:
            with open(CONFIG_PATH, "r", encoding="utf-8") as f:
                user_config = json.load(f)
            config = _merge_config(DEFAULTS, user_config)
        except Exception as e:
            logger.error(f"Failed to read config file: {e}")
            config = copy.deepcopy(DEFAULTS)

    # 应用环境变量覆盖
    changed = _apply_env_overrides(config)
    
    # 如果发生了覆盖或文件之前不存在，则写回文件
    if changed or not os.path.exists(CONFIG_PATH):
        try:
            with open(CONFIG_PATH, "w", encoding="utf-8") as f:
                json.dump(config, f, indent=4, ensure_ascii=False)
            logger.info(f"Config updated and saved to {CONFIG_PATH}")
        except Exception as e:
            logger.error(f"Failed to save config file: {e}")

    # 必填项校验
    if not config["llm"].get("api_url") or not config["llm"].get("api_key"):
        logger.error("LLM_API_URL and LLM_API_KEY must be set in config.json or environment variables.")
        import sys
        sys.exit(1)

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
    if not os.path.exists(PROMPTS_DIR):
        os.makedirs(PROMPTS_DIR, exist_ok=True)
    
    sys_prompt_path = os.path.join(PROMPTS_DIR, "system_prompt.txt")
    if not os.path.exists(sys_prompt_path):
        logger.info(f"Generating default system prompt at {sys_prompt_path}")
        with open(sys_prompt_path, "w", encoding="utf-8") as f:
            f.write(DEFAULT_SYSTEM_PROMPT)
            
    glossary_path = os.path.join(PROMPTS_DIR, "glossary.json")
    if not os.path.exists(glossary_path):
        logger.info(f"Generating default glossary at {glossary_path}")
        with open(glossary_path, "w", encoding="utf-8") as f:
            json.dump({}, f, indent=4, ensure_ascii=False)
