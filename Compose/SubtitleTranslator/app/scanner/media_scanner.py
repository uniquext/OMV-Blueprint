"""
媒体文件扫描器模块

负责递归遍历目录，寻找需要处理的媒体文件。
"""
import os
import logging
from typing import List

logger = logging.getLogger(__name__)


def is_media_file(filename: str, extensions: List[str]) -> bool:
    """
    判断文件是否为合法的媒体文件。

    条件:
    1. 不以点开头 (非隐藏文件)
    2. 扩展名在配置的列表中 (不区分大小写)

    Args:
        filename: 文件名
        extensions: 允许的扩展名列表, 例如 [".mkv", ".mp4"]

    Returns:
        是否为媒体文件
    """
    if filename.startswith("."):
        return False

    ext = os.path.splitext(filename)[1].lower()
    return ext in extensions


def classify_zh_subtitle(filename: str) -> int:
    """
    根据中文字幕文件名分类到 funnel_type。
    
    规则：
    - *.zh.ai.srt -> Type 02
    - *.zh.opencc.srt -> Type 01
    """
    filename = filename.lower()
    if filename.endswith(".zh.ai.srt"):
        return 2
    elif filename.endswith(".zh.opencc.srt"):
        return 1
    return None


def scan_directory(dir_path: str, extensions: List[str], ignore_list: List[str] = None) -> List[str]:
    """
    递归扫描目录，返回所有需要处理的媒体文件完整路径。

    会跳过隐藏目录，以及匹配 ignore_list 的目录或文件，
    以及已经存在对应的 .zh.ai.srt 或 .zh.opencc.srt 的媒体文件。

    Args:
        dir_path: 目标扫描目录
        extensions: 允许的扩展名列表
        ignore_list: 需要忽略的目录名称或绝对路径子串列表

    Returns:
        需要处理的媒体文件绝对路径列表

    Raises:
        FileNotFoundError: 目录不存在
    """
    if not os.path.exists(dir_path):
        raise FileNotFoundError(f"Directory not found: {dir_path}")

    if not os.path.isdir(dir_path):
        raise NotADirectoryError(f"Path is not a directory: {dir_path}")

    media_files = []
    if ignore_list is None:
        ignore_list = []
    # 过滤掉空字符串，防止匹配一切
    ignore_list = [ign for ign in ignore_list if ign.strip()]

    # 统一转换扩展名为小写
    exts_lower = [ext.lower() for ext in extensions]

    for root, dirs, files in os.walk(dir_path):
        # 排除隐藏目录和匹配 ignore_list 的目录
        dirs[:] = [
            d for d in dirs 
            if not d.startswith('.') 
            and d not in ignore_list 
            and not any(ign in os.path.join(root, d) for ign in ignore_list)
        ]

        for f in files:
            file_path = os.path.join(root, f)
            
            # 检查文件绝对路径子串匹配 ignore_list
            if any(ign in file_path for ign in ignore_list):
                continue

            if is_media_file(f, exts_lower):
                
                # 检查是否已经存在 AI 或 OpenCC 的字幕，原生 .zh.srt 将交由漏斗评估 (Type 20)
                media_stem = os.path.splitext(f)[0]
                has_zh = False
                zh_subtitle_filename = None
                for filename in files:
                    if filename.startswith(f"{media_stem}.") and (filename.endswith(".zh.ai.srt") or filename.endswith(".zh.opencc.srt")):
                        has_zh = True
                        zh_subtitle_filename = filename
                        break
                
                if has_zh:
                    logger.debug(f"Skipping {f}, already has .ai.srt or .opencc.srt subtitle")
                    from core import db
                    funnel_type = classify_zh_subtitle(zh_subtitle_filename)
                    if funnel_type is not None:
                        output_srt_path = os.path.join(root, zh_subtitle_filename)
                        db.backfill_job(file_path, funnel_type, output_srt_path)
                    continue
                
                media_files.append(file_path)

    return media_files
