"""
OpenCC 繁简转换模块

封装 OpenCC 库，提供 SRT 字幕文件的繁体中文转简体中文功能。
仅转换文本内容，保留时间轴和序号信息。
"""
import os
import logging
import pysrt
from opencc import OpenCC

logger = logging.getLogger(__name__)

# 繁体转简体转换器 (单例)
_converter = OpenCC("t2s")


def convert_traditional_to_simplified(srt_path: str, output_srt_path: str) -> None:
    """
    将繁体中文 SRT 字幕转换为简体中文。

    使用 OpenCC t2s 方案转换字幕文本，保留所有时间轴和序号信息。

    Args:
        srt_path: 输入繁体中文 SRT 文件路径
        output_srt_path: 输出简体中文 SRT 文件路径

    Raises:
        FileNotFoundError: 输入文件不存在
    """
    if not os.path.exists(srt_path):
        raise FileNotFoundError(f"SRT file not found: {srt_path}")

    subs = pysrt.open(srt_path, encoding="utf-8")
    logger.info(f"Converting traditional to simplified: {srt_path} ({len(subs)} entries)")

    for sub in subs:
        sub.text = _converter.convert(sub.text)

    subs.save(output_srt_path, encoding="utf-8")
    logger.info(f"Simplified SRT saved to {output_srt_path}")
