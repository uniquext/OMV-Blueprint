import os
import logging
from typing import Dict
from pathlib import Path

import db
from subtitle.lang_utils import normalize_language
from subtitle.srt_handler import extract_text_from_srt
from subtitle.opencc_handler import convert_traditional_to_simplified
from subtitle.ffmpeg_handler import (
    get_subtitle_streams,
    is_graphical_codec,
    extract_subtitle_track,
)
from config_loader import load_config

logger = logging.getLogger(__name__)

PRIORITY_LANGUAGES = ["zh", "zt", "en", "ja"]


def scan_external_subtitles(media_path: str) -> Dict[str, str]:
    media_dir = os.path.dirname(media_path)
    media_stem = Path(media_path).stem

    result: Dict[str, str] = {}

    config = load_config()
    lang_map_override = config["media"]["lang_map_override"]

    search_dirs = [media_dir]
    subs_dir = os.path.join(media_dir, "Subs")
    if os.path.isdir(subs_dir):
        search_dirs.append(subs_dir)

    for search_dir in search_dirs:
        try:
            for filename in os.listdir(search_dir):
                if not filename.endswith(".srt"):
                    continue

                # 支持通配符前缀匹配，包括专属字幕及外部中文字幕
                if (filename.startswith(f"{media_stem}.zh.") or filename.startswith(f"{media_stem}.zh-")) and filename.endswith(".srt"):
                    result["zh"] = os.path.join(search_dir, filename)
                    continue

                if not filename.startswith(media_stem + "."):
                    continue

                # 正常提取其他语言变体（如 .en.srt, .ja.srt 等）
                parts = filename.rsplit(".", 2)
                if len(parts) != 3:
                    continue

                file_stem, lang_suffix, _ = parts
                if file_stem != media_stem:
                    continue

                normalized_lang = normalize_language(lang_suffix, lang_map_override)
                if normalized_lang:
                    full_path = os.path.join(search_dir, filename)
                    result[normalized_lang] = full_path
        except OSError:
            continue

    return result


def evaluate_media(media_path: str) -> Dict:
    external = scan_external_subtitles(media_path)

    if "zh" in external:
        logger.info(f"Level 0 skip (external zh): {media_path}")
        return {
            "level": 0, "source_type": "external",
            "srt_path": external["zh"], "language": "zh"
        }

    if "zt" in external:
        logger.info(f"Level 1 (external zt): {media_path}")
        return {
            "level": 1, "source_type": "external",
            "srt_path": external["zt"], "language": "zt"
        }

    if "en" in external:
        logger.info(f"Level 2 (external en): {media_path}")
        return {
            "level": 2, "source_type": "external",
            "srt_path": external["en"], "language": "en"
        }

    if "ja" in external:
        logger.info(f"Level 2 (external ja): {media_path}")
        return {
            "level": 2, "source_type": "external",
            "srt_path": external["ja"], "language": "ja"
        }

    config = load_config()
    lang_map_override = config["media"]["lang_map_override"]

    try:
        streams = get_subtitle_streams(media_path)
    except Exception as e:
        logger.error(f"ffprobe failed for {media_path}: {e}")
        return {"level": -1, "reason": "ffprobe_error"}

    if not streams:
        logger.warning(f"No subtitles found: {media_path}")
        return {"level": -1, "reason": "no_subtitle"}

    text_streams: Dict[str, Dict] = {}
    has_graphical = False

    for stream in streams:
        codec = stream.get("codec_name", "")
        if is_graphical_codec(codec):
            has_graphical = True
            continue

        lang = stream.get("tags", {}).get("language", "")
        normalized = normalize_language(lang, lang_map_override)
        if normalized and normalized not in text_streams:
            text_streams[normalized] = stream

    if "zh" in text_streams:
        s = text_streams["zh"]
        logger.info(f"Level 0 skip (embedded zh): {media_path}")
        return {
            "level": 0, "source_type": "embedded",
            "stream_index": s["index"], "language": "zh"
        }

    if "zt" in text_streams:
        s = text_streams["zt"]
        logger.info(f"Level 1 (embedded zt): {media_path}")
        return {
            "level": 1, "source_type": "embedded",
            "stream_index": s["index"], "language": "zt"
        }

    if "en" in text_streams:
        s = text_streams["en"]
        logger.info(f"Level 3 (embedded en): {media_path}")
        return {
            "level": 3, "source_type": "embedded",
            "stream_index": s["index"], "language": "en"
        }

    if "ja" in text_streams:
        s = text_streams["ja"]
        logger.info(f"Level 3 (embedded ja): {media_path}")
        return {
            "level": 3, "source_type": "embedded",
            "stream_index": s["index"], "language": "ja"
        }

    if has_graphical:
        logger.info(f"Graphical subtitles only: {media_path}")
        return {"level": -1, "reason": "graphical_only"}

    logger.warning(f"No usable subtitles: {media_path}")
    return {"level": -1, "reason": "no_subtitle"}


def execute_funnel_action(job_id: str, media_path: str, funnel_result: Dict) -> None:
    level = funnel_result.get("level", -1)
    media_stem = Path(media_path).stem
    media_dir = os.path.dirname(media_path)
    if level == 1:
        output_zh_srt = os.path.join(media_dir, f"{media_stem}.zh.opencc.srt")
    else:
        output_zh_srt = os.path.join(media_dir, f"{media_stem}.zh.ai.srt")

    if level == 0:
        logger.info(f"Job {job_id}: Level 0 skip, marking done")
        db.update_job_funnel_info(job_id, funnel_level=0)
        db.update_job_status(job_id, "done")
        return

    if level == -1:
        reason = funnel_result.get("reason", "unknown")
        if reason == "graphical_only":
            logger.info(f"Job {job_id}: Graphical subtitles only, cannot extract text, skipping")
        else:
            logger.warning(f"Job {job_id}: No usable subtitle ({reason}), skipping")
        db.update_job_status(job_id, "done")
        return

    db.update_job_status(job_id, "extracting")

    if level == 1:
        source_type = funnel_result["source_type"]
        if source_type == "external":
            srt_path = funnel_result["srt_path"]
            db.update_job_funnel_info(job_id, funnel_level=1,
                                      original_srt_path=srt_path,
                                      output_srt_path=output_zh_srt)
            convert_traditional_to_simplified(srt_path, output_zh_srt)
        else:
            stream_index = funnel_result["stream_index"]
            emb_srt = os.path.join(media_dir, f"{media_stem}.zt.emb.srt")
            db.update_job_funnel_info(job_id, funnel_level=1,
                                      original_srt_path=emb_srt,
                                      output_srt_path=output_zh_srt,
                                      cleanup_files=[emb_srt])
            extract_subtitle_track(media_path, stream_index, emb_srt)
            convert_traditional_to_simplified(emb_srt, output_zh_srt)
            try:
                os.remove(emb_srt)
            except OSError:
                pass

        db.update_job_status(job_id, "done")
        logger.info(f"Job {job_id}: Level 1 conversion done -> {output_zh_srt}")
        return

    if level == 2:
        srt_path = funnel_result["srt_path"]
        lang = funnel_result["language"]
        txt_path = os.path.join(media_dir, f"{media_stem}.{lang}.txt")
        zh_txt_path = os.path.join(media_dir, f"{media_stem}.zh.txt")
        cleanup = [txt_path, zh_txt_path]

        db.update_job_funnel_info(job_id, funnel_level=2,
                                  original_srt_path=srt_path,
                                  output_srt_path=output_zh_srt,
                                  cleanup_files=cleanup)

        extract_text_from_srt(srt_path, txt_path)
        task_id = db.create_translate_task_for_job(job_id, txt_path)

        if task_id:
            logger.info(f"Job {job_id}: Level 2 extract done, task {task_id} created")
        else:
            logger.warning(f"Job {job_id}: Level 2 extract done, but task deduped")
        return

    if level == 3:
        stream_index = funnel_result["stream_index"]
        lang = funnel_result["language"]
        emb_srt = os.path.join(media_dir, f"{media_stem}.{lang}.emb.srt")
        emb_txt = os.path.join(media_dir, f"{media_stem}.{lang}.emb.txt")
        emb_zh_txt = os.path.join(media_dir, f"{media_stem}.{lang}.emb.zh.txt")
        cleanup = [emb_srt, emb_txt, emb_zh_txt]

        db.update_job_funnel_info(job_id, funnel_level=3,
                                  original_srt_path=emb_srt,
                                  output_srt_path=output_zh_srt,
                                  cleanup_files=cleanup)

        extract_subtitle_track(media_path, stream_index, emb_srt)
        extract_text_from_srt(emb_srt, emb_txt)
        task_id = db.create_translate_task_for_job(job_id, emb_txt)

        if task_id:
            logger.info(f"Job {job_id}: Level 3 extract done, task {task_id} created")
        else:
            logger.warning(f"Job {job_id}: Level 3 extract done, but task deduped")
        return
