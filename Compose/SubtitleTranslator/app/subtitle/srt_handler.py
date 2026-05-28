"""
SRT 字幕文件处理模块

提供 SRT 字幕的纯文本提取和翻译回写功能。
使用 pysrt 库解析 SRT 格式，确保时间轴信息的完整保留。
"""
import os
import logging
import pysrt
import re

logger = logging.getLogger(__name__)


def _deduplicate_layers(texts):
    if not texts:
        return texts
    n = len(texts)
    # 1. 检查 Interleaved 模式 (如 A A B B C C)
    for n_repeats in range(n, 1, -1):
        if n % n_repeats == 0:
            is_interleaved = True
            for i in range(0, n, n_repeats):
                if len(set(texts[i:i+n_repeats])) != 1:
                    is_interleaved = False
                    break
            if is_interleaved:
                return [texts[i] for i in range(0, n, n_repeats)]
    
    # 2. 检查 Block 模式 (如 A B C A B C)
    for chunk_size in range(1, n // 2 + 1):
        if n % chunk_size == 0:
            chunk = texts[:chunk_size]
            if texts == chunk * (n // chunk_size):
                return chunk
                
    return texts


def _process_group(group, merged):
    if not group:
        return
    if len(group) == 1:
        merged.append(group[0])
        return
        
    texts = [sub.text for sub in group]
    deduped_texts = _deduplicate_layers(texts)
    
    # 将去重后的文本用换行符连接成一个单独的 subtitle 条目
    new_sub = group[0]
    new_sub.text = "\n".join(deduped_texts)
    merged.append(new_sub)


def optimize_srt(srt_path: str) -> None:
    """
    优化 SRT 文件，将具有完全相同起止时间的字幕条目合并为一条。
    通过智能模式识别（交错模式与块模式），解决 ASS 格式特效图层导致的冗余条目问题，
    同时确保不会误删同时间轴内合理的重复字符（如 "sweet" 中的连续 'e'）。
    """
    subs = pysrt.open(srt_path, encoding="utf-8")
    original_count = len(subs)
    if original_count == 0:
        return
        
    merged = pysrt.SubRipFile()
    current_group = []
    
    for sub in subs:
        if not current_group:
            current_group.append(sub)
        else:
            if sub.start == current_group[0].start and sub.end == current_group[0].end:
                current_group.append(sub)
            else:
                _process_group(current_group, merged)
                current_group = [sub]
                
    if current_group:
        _process_group(current_group, merged)
        
    if len(merged) < original_count:
        logger.info(f"Optimized SRT {srt_path}: Merged {original_count} entries into {len(merged)} entries.")
        merged.save(srt_path, encoding="utf-8")


def extract_text_from_srt(srt_path: str, txt_path: str) -> None:
    """
    从 SRT 字幕文件中提取纯文本。

    将每条字幕的文本提取为一行，多行字幕合并为单行（换行替换为空格）。
    同时通过正则表达式过滤掉可能存在的 HTML 标签（如 <i>, <b>, <font> 等）。
    输出文件每行对应一条字幕条目。

    Args:
        srt_path: 输入 SRT 文件路径
        txt_path: 输出纯文本文件路径

    Raises:
        FileNotFoundError: 输入文件不存在
    """
    if not os.path.exists(srt_path):
        raise FileNotFoundError(f"SRT file not found: {srt_path}")

    # 在提取前进行优化，消除冗余时间轴
    optimize_srt(srt_path)

    subs = pysrt.open(srt_path, encoding="utf-8")
    logger.info(f"Extracting text from {srt_path}: {len(subs)} entries")

    with open(txt_path, "w", encoding="utf-8") as f:
        for sub in subs:
            # 过滤 HTML 标签 (如 <i>) 和 ASS 样式标签 (如 {\an8})
            clean_text = re.sub(r'<[^>]+>|\{[^}]+\}', '', sub.text)
            # 多行文本合并为单行: 将 \n 替换为空格
            line = clean_text.replace("\n", " ").strip()
            f.write(line + "\n")

    logger.info(f"Text extracted to {txt_path}")


def rebuild_srt_with_translation(
    template_srt_path: str,
    zh_txt_path: str,
    output_srt_path: str
) -> None:
    """
    将翻译文本回写到 SRT 字幕文件中。

    以模板 SRT 的时间轴为基准，将翻译文本逐行替换字幕内容。
    当翻译行数与字幕条目数不匹配时，取 min(len) 进行替换并记录警告。

    Args:
        template_srt_path: 模板 SRT 文件路径 (提供时间轴)
        zh_txt_path: 翻译文本文件路径 (每行一条翻译)
        output_srt_path: 输出 SRT 文件路径

    Raises:
        FileNotFoundError: 模板文件或翻译文件不存在
    """
    if not os.path.exists(template_srt_path):
        raise FileNotFoundError(f"Template SRT not found: {template_srt_path}")
    if not os.path.exists(zh_txt_path):
        raise FileNotFoundError(f"Translation text not found: {zh_txt_path}")

    subs = pysrt.open(template_srt_path, encoding="utf-8")

    with open(zh_txt_path, "r", encoding="utf-8") as f:
        translated_lines = [line.strip() for line in f.readlines() if line.strip()]

    sub_count = len(subs)
    trans_count = len(translated_lines)

    if sub_count != trans_count:
        logger.warning(
            f"Line count mismatch: {sub_count} subtitle entries vs "
            f"{trans_count} translated lines. Using min({min(sub_count, trans_count)})."
        )

    # 对位替换: 取 min(len) 条
    replace_count = min(sub_count, trans_count)
    for i in range(replace_count):
        subs[i].text = translated_lines[i]

    subs.save(output_srt_path, encoding="utf-8")
    logger.info(f"SRT rebuilt: {output_srt_path} ({replace_count}/{sub_count} entries replaced)")
