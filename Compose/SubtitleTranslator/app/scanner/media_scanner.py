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


def scan_directory(dir_path: str, extensions: List[str]) -> List[str]:
    """
    递归扫描目录，返回所有需要处理的媒体文件完整路径。

    会跳过隐藏目录，以及已经存在对应的 .zh.srt 的媒体文件。

    Args:
        dir_path: 目标扫描目录
        extensions: 允许的扩展名列表

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
    
    # 统一转换扩展名为小写
    exts_lower = [ext.lower() for ext in extensions]

    for root, dirs, files in os.walk(dir_path):
        # 排除隐藏目录
        dirs[:] = [d for d in dirs if not d.startswith('.')]

        for f in files:
            if is_media_file(f, exts_lower):
                file_path = os.path.join(root, f)
                
                # 检查是否已经存在任何中文字幕 (.zh.srt 或 .zh.*.srt)
                media_stem = os.path.splitext(f)[0]
                has_zh = False
                for filename in files:
                    if (filename.startswith(f"{media_stem}.zh.") or filename.startswith(f"{media_stem}.zh-")) and filename.endswith(".srt"):
                        has_zh = True
                        break
                
                if has_zh:
                    logger.debug(f"Skipping {f}, already has .zh.*.srt subtitle")
                    continue
                
                media_files.append(file_path)

    return media_files
