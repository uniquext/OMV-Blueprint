"""
语言标签标准化模块

将来自多种来源 (ffprobe、文件名后缀、Bazarr Hook) 的语言代码
归一化为 zh / zt / en / ja 等标准简写形式。
"""
import logging
from typing import Dict, Optional

logger = logging.getLogger(__name__)

# 默认语言映射表: 将各种来源的语言代码映射为标准简写
LANG_MAP: Dict[str, str] = {
    # 简体中文
    "zh": "zh",
    "zho": "zh",
    "chi": "zh",
    "chinese": "zh",
    "zh-cn": "zh",
    "zh-hans": "zh",
    # 繁体中文
    "zt": "zt",
    "zht": "zt",
    "zh-tw": "zt",
    "zh-hant": "zt",
    # 英文
    "en": "en",
    "eng": "en",
    "english": "en",
    # 日文
    "ja": "ja",
    "jpn": "ja",
    "japanese": "ja",
}


def normalize_language(
    lang_code: str,
    lang_map_override: Optional[Dict[str, str]] = None
) -> str:
    """
    将语言代码标准化为 zh/zt/en/ja 等简写形式。

    处理流程:
    1. 剥离冒号后缀 (如 zh:hi → zh, en:forced → en)
    2. 合并自定义映射覆盖 (LANG_MAP_OVERRIDE)
    3. 查表映射，未命中则透传原始值

    Args:
        lang_code: 原始语言代码 (如 "chi", "eng:forced", "zh:hi")
        lang_map_override: 可选的自定义映射覆盖字典

    Returns:
        标准化后的语言简写 (如 "zh", "en", "ja")
    """
    if not lang_code:
        return lang_code

    # 剥离冒号后缀 (如 :hi, :forced, :sdh)
    base_code = lang_code.split(":")[0].split(".")[0].strip().lower()

    # 合并映射表: 默认 + 覆盖
    effective_map = dict(LANG_MAP)
    if lang_map_override:
        effective_map.update(lang_map_override)

    # 查表映射，未命中则透传
    result = effective_map.get(base_code, base_code)
    return result
