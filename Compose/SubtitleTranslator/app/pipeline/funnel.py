import os
import logging
from typing import Dict
from pathlib import Path

from core import db
from core.config_loader import load_config
from subtitle.lang_utils import normalize_language
from subtitle.srt_handler import extract_text_from_srt
from subtitle.opencc_handler import convert_traditional_to_simplified
from subtitle.ffmpeg_handler import (
    get_subtitle_streams,
    is_graphical_codec,
    extract_subtitle_track,
)

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

    config = load_config()
    lang_map_override = config["media"]["lang_map_override"]
    strategy = config.get("strategy", {"location_priority": "external", "language_priority": "chinese"})
    loc_prio = strategy.get("location_priority", "external")
    lang_prio = strategy.get("language_priority", "chinese")

    try:
        streams = get_subtitle_streams(media_path)
    except Exception as e:
        logger.error(f"ffprobe failed for {media_path}: {e}")
        return {"funnel_type": -1, "reason": "ffprobe_error"}

    candidates = {}

    if "zh" in external:
        candidates[20] = {"funnel_type": 20, "srt_path": external["zh"], "language": "zh"}
    if "zt" in external:
        candidates[21] = {"funnel_type": 21, "srt_path": external["zt"], "language": "zt"}
    
    for ext_lang in ["en", "ja"]:
        if ext_lang in external:
            candidates[22] = {"funnel_type": 22, "srt_path": external[ext_lang], "language": ext_lang}
            break

    has_graphical = False
    if streams:
        for stream in streams:
            codec = stream.get("codec_name", "")
            if is_graphical_codec(codec):
                has_graphical = True
                continue

            lang = stream.get("tags", {}).get("language", "")
            normalized = normalize_language(lang, lang_map_override)
            if normalized == "zh" and 10 not in candidates:
                candidates[10] = {"funnel_type": 10, "stream_index": stream["index"], "language": "zh"}
            elif normalized == "zt" and 11 not in candidates:
                candidates[11] = {"funnel_type": 11, "stream_index": stream["index"], "language": "zt"}
            elif normalized in ["en", "ja"] and 12 not in candidates:
                candidates[12] = {"funnel_type": 12, "stream_index": stream["index"], "language": normalized}

    if loc_prio == "internal" and lang_prio == "absolute":
        order = [10, 11, 12, 20, 21, 22]
    elif loc_prio == "internal" and lang_prio == "chinese":
        order = [10, 20, 11, 21, 12, 22]
    elif loc_prio == "external" and lang_prio == "absolute":
        order = [20, 21, 22, 10, 11, 12]
    else: # external + chinese
        order = [20, 10, 21, 11, 22, 12]

    for t in order:
        if t in candidates:
            logger.info(f"Evaluated {media_path}: Selected Type {t}")
            return candidates[t]

    if has_graphical:
        logger.info(f"Graphical subtitles only: {media_path}")
        return {"funnel_type": -1, "reason": "graphical_only"}

    logger.warning(f"No usable subtitles: {media_path}")
    return {"funnel_type": -1, "reason": "no_subtitle"}


def execute_funnel_action(job_id: str, media_path: str, funnel_result: Dict) -> None:
    funnel_type = funnel_result.get("funnel_type", -1)
    media_stem = Path(media_path).stem
    media_dir = os.path.dirname(media_path)
    
    if funnel_type in (11, 21):
        output_zh_srt = os.path.join(media_dir, f"{media_stem}.zh.opencc.srt")
    else:
        output_zh_srt = os.path.join(media_dir, f"{media_stem}.zh.ai.srt")

    if funnel_type in (10, 20):
        logger.info(f"Job {job_id}: Type {funnel_type} skip, marking skipped")
        original_srt = funnel_result.get("srt_path")
        if not original_srt and funnel_type == 10:
            original_srt = f"内置轨道: Stream #{funnel_result.get('stream_index')} ({funnel_result.get('language')})"
        db.update_job_funnel_info(job_id, funnel_type=funnel_type, original_srt_path=original_srt, output_srt_path=original_srt)
        db.update_job_status(job_id, "skipped")
        return

    if funnel_type == -1:
        reason = funnel_result.get("reason", "unknown")
        if reason == "graphical_only":
            logger.info(f"Job {job_id}: Graphical subtitles only, cannot extract text, skipping")
        else:
            logger.warning(f"Job {job_id}: No usable subtitle ({reason}), skipping")
        db.update_job_funnel_info(job_id, funnel_type=-1)
        db.update_job_status(job_id, "skipped")
        return

    db.update_job_status(job_id, "extracting")

    if funnel_type == 21:
        srt_path = funnel_result["srt_path"]
        db.update_job_funnel_info(job_id, funnel_type=21,
                                  original_srt_path=srt_path,
                                  output_srt_path=output_zh_srt)
        convert_traditional_to_simplified(srt_path, output_zh_srt)
        db.update_job_status(job_id, "done")
        logger.info(f"Job {job_id}: Type 21 conversion done -> {output_zh_srt}")
        return

    if funnel_type == 11:
        stream_index = funnel_result["stream_index"]
        emb_srt = os.path.join(media_dir, f"{media_stem}.zt.emb.srt")
        db.update_job_funnel_info(job_id, funnel_type=11,
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
        logger.info(f"Job {job_id}: Type 11 conversion done -> {output_zh_srt}")
        return

    if funnel_type == 22:
        srt_path = funnel_result["srt_path"]
        lang = funnel_result["language"]
        txt_path = os.path.join(media_dir, f"{media_stem}.{lang}.txt")
        zh_txt_path = os.path.join(media_dir, f"{media_stem}.zh.txt")
        cleanup = [txt_path, zh_txt_path]

        db.update_job_funnel_info(job_id, funnel_type=22,
                                  original_srt_path=srt_path,
                                  output_srt_path=output_zh_srt,
                                  cleanup_files=cleanup)

        extract_text_from_srt(srt_path, txt_path)
        task_id = db.create_translate_task_for_job(job_id, txt_path)

        if task_id:
            logger.info(f"Job {job_id}: Type 22 extract done, task {task_id} created")
        else:
            logger.warning(f"Job {job_id}: Type 22 extract done, but task deduped")
        return

    if funnel_type == 12:
        stream_index = funnel_result["stream_index"]
        lang = funnel_result["language"]
        emb_srt = os.path.join(media_dir, f"{media_stem}.{lang}.emb.srt")
        emb_txt = os.path.join(media_dir, f"{media_stem}.{lang}.emb.txt")
        emb_zh_txt = os.path.join(media_dir, f"{media_stem}.{lang}.emb.zh.txt")
        cleanup = [emb_srt, emb_txt, emb_zh_txt]

        db.update_job_funnel_info(job_id, funnel_type=12,
                                  original_srt_path=emb_srt,
                                  output_srt_path=output_zh_srt,
                                  cleanup_files=cleanup)

        extract_subtitle_track(media_path, stream_index, emb_srt)
        extract_text_from_srt(emb_srt, emb_txt)
        task_id = db.create_translate_task_for_job(job_id, emb_txt)

        if task_id:
            logger.info(f"Job {job_id}: Type 12 extract done, task {task_id} created")
        else:
            logger.warning(f"Job {job_id}: Type 12 extract done, but task deduped")
        return
