"""
ffmpeg/ffprobe 字幕流操作模块

提供字幕流探测、图形字幕识别和字幕轨提取功能。
通过 subprocess 调用 ffprobe/ffmpeg 命令行工具。
"""
import json
import logging
import subprocess
from typing import List, Dict

logger = logging.getLogger(__name__)

# 图形字幕 codec 集合 (无法提取文本)
GRAPHICAL_CODECS = {"dvd_subtitle", "pgssub", "hdmv_pgs_subtitle"}


def is_graphical_codec(codec_name: str) -> bool:
    """
    判断字幕 codec 是否为图形字幕。

    图形字幕 (如 DVD/PGS) 为位图格式，无法提取文本内容。

    Args:
        codec_name: codec 名称

    Returns:
        True 如果是图形字幕，否则 False
    """
    return codec_name in GRAPHICAL_CODECS


def get_subtitle_streams(media_path: str) -> List[Dict]:
    """
    探测媒体文件中的字幕流信息。

    调用 ffprobe 获取所有流信息，筛选出 subtitle 类型的流。

    Args:
        media_path: 媒体文件路径

    Returns:
        字幕流信息列表，每个元素包含 index、codec_name、tags 等字段

    Raises:
        subprocess.CalledProcessError: ffprobe 调用失败
    """
    cmd = [
        "ffprobe",
        "-v", "quiet",
        "-print_format", "json",
        "-show_streams",
        media_path
    ]

    logger.info(f"Running ffprobe on {media_path}")
    result = subprocess.run(cmd, capture_output=True, text=True, check=True)

    data = json.loads(result.stdout)
    streams = data.get("streams", [])

    # 筛选字幕流
    subtitle_streams = [
        s for s in streams if s.get("codec_type") == "subtitle"
    ]

    logger.info(f"Found {len(subtitle_streams)} subtitle streams in {media_path}")
    return subtitle_streams


def extract_subtitle_track(
    media_path: str,
    stream_index: int,
    output_srt_path: str
) -> None:
    """
    从媒体文件中提取指定索引的字幕轨为 SRT 文件。

    Args:
        media_path: 媒体文件路径
        stream_index: 字幕流的绝对索引号
        output_srt_path: 输出 SRT 文件路径

    Raises:
        subprocess.CalledProcessError: ffmpeg 调用失败
    """
    cmd = [
        "ffmpeg",
        "-y",
        "-i", media_path,
        "-map", f"0:{stream_index}",
        "-c:s", "srt",
        output_srt_path
    ]

    logger.info(f"Extracting subtitle track {stream_index} from {media_path}")
    subprocess.run(cmd, capture_output=True, text=True, check=True)
    logger.info(f"Subtitle track extracted to {output_srt_path}")
