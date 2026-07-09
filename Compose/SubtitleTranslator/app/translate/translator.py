import os
import re
import httpx
import time
import json
from typing import List, Dict, Optional, Tuple
from dataclasses import dataclass
from .prompt_loader import load_system_prompt, load_mt_system_prompt, load_glossary
from core import db
from .rate_limiter import rate_limiter
import logging
from .eta_tracker import eta_tracker
from core.config_loader import load_config

logger = logging.getLogger(__name__)

_config = None

def _get_config():
    global _config
    if _config is None:
        _config = load_config()
    return _config


def _save_error_snapshot(
    task_id: str,
    batch_idx: int,
    error_reason: str,
    system_prompt: str,
    glossary: dict,
    user_prompt: str,
    llm_response: str = None
):
    """将错误诊断快照写入数据库，写入失败仅记录日志不阻断主流程"""
    try:
        import json as json_module
        glossary_str = json_module.dumps(glossary, ensure_ascii=False) if glossary is not None else None
        llm_settings = {
            "model": _get_config()["llm"]["model"],
            "temperature": _get_config()["llm"]["temperature"]
        }
        db.insert_translation_error(
            task_id=task_id,
            batch_idx=batch_idx,
            error_reason=error_reason,
            system_prompt=system_prompt,
            glossary=glossary_str,
            user_prompt=user_prompt,
            llm_response=llm_response,
            llm_settings=llm_settings
        )
    except Exception as e:
        logger.warning(f"Failed to save error snapshot for task {task_id}: {e}")

@dataclass
class Batch:
    """A translation batch, containing the current lines and pre-context."""
    batch_idx: int
    lines: List[str]
    context_before: List[str]
    start_id: int
    end_id: int

@dataclass
class TranslatedLine:
    """Parsed single-line translation result."""
    id: int
    text: str

def preprocess(file_path: str) -> Tuple[List[str], List[Optional[int]]]:
    """
    Reads each line of the file, builds a blank line mapping table, and returns the formatted list with IDs and line_map.

    Formatted list: `ID: n | text` (No tag keywords, unified for all models)
    line_map: index = original line number, value = assigned ID (non-blank line) or None (blank line)
    """
    with open(file_path, "r", encoding="utf-8") as f:
        lines = f.readlines()

    formatted_lines = []
    # line_map[original_line_number] = ID or None (blank line)
    line_map = []
    current_id = 1
    for line in lines:
        text = line.strip()
        if text:
            formatted_lines.append(f"ID: {current_id} | {text}")
            line_map.append(current_id)
            current_id += 1
        else:
            line_map.append(None)

    return formatted_lines, line_map

def create_batches(lines: List[str], batch_size: int = None, context_size: int = None) -> List[Batch]:
    """Batches numbered lines and appends a pre-context window for each batch (pre-context only, no post-context)."""
    if batch_size is None:
        batch_size = _get_config()["llm"]["batch_size"]
    if context_size is None:
        context_size = _get_config()["llm"]["context_size"]
    batches = []
    total_lines = len(lines)
    batch_idx = 1
    for i in range(0, total_lines, batch_size):
        batch_lines = lines[i:i + batch_size]
        start_id = i + 1
        end_id = min(i + batch_size, total_lines)

        # Pre-context only
        context_before = lines[max(0, i - context_size):i]

        batches.append(Batch(
            batch_idx=batch_idx,
            lines=batch_lines,
            context_before=context_before,
            start_id=start_id,
            end_id=end_id
        ))
        batch_idx += 1
    return batches

def call_llm(system_prompt: str, user_prompt: str, glossary: Dict, model_type: str = "chat", temperature: float = None) -> tuple[str, int]:
    """
    Uses httpx to call the LLM API and returns the raw response text.

    User_prompt construction depends on model_type:
    - chat: Injects glossary into user_prompt
    - mt: Sends plain text directly, no glossary injection
    """
    headers = {
        "Authorization": f"Bearer {_get_config()['llm']['api_key']}",
        "Content-Type": "application/json"
    }

    # MT model does not inject glossary, Chat model injects normally
    if model_type == "mt":
        final_user_content = user_prompt
    else:
        final_user_content = f"术语表：{json.dumps(glossary, ensure_ascii=False)}\n\n{user_prompt}"

    if temperature is None:
        temperature = _get_config()["llm"]["temperature"]

    payload = {
        "model": _get_config()["llm"]["model"],
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": final_user_content}
        ],
        "temperature": temperature
    }

    # 移除强制的 response_format 以兼容推理模型 (Reasoning Models)
    with httpx.Client(timeout=float(_get_config()["llm"]["timeout"])) as client:
        rate_limiter.pre_request_check()
        response = client.post(_get_config()["llm"]["api_url"], headers=headers, json=payload)
        response.raise_for_status()
        data = response.json()

        usage = data.get("usage", {})
        total_tokens = usage.get("total_tokens", 0)
        logger.info(f"LLM token_usage", extra={"token_usage": usage})
        rate_limiter.post_request_update(total_tokens)

        return data["choices"][0]["message"]["content"], total_tokens


def parse_response(response_text: str, expected_ids: List[int], model_type: str = "chat") -> List[TranslatedLine]:
    """
    Parses response from LLM: uses JSON parsing for chat models, and lenient regex for MT models.
    """
    if model_type == "mt":
        results = []
        discarded = 0
        pattern = re.compile(r"^ID:\s*(\d+)\s*\|\s*(.+)$")
        expected_id_set = set(expected_ids)
        for line in response_text.strip().split("\n"):
            line = line.strip()
            if not line:
                continue
            match = pattern.match(line)
            if match:
                line_id = int(match.group(1))
                translated_text = match.group(2).strip()
                if line_id in expected_id_set:
                    results.append(TranslatedLine(id=line_id, text=translated_text))
                else:
                    discarded += 1

        if discarded > 0:
            logger.warning(
                f"LLM returned {len(results) + discarded} lines matching ID pattern, "
                f"but only {len(results)} within expected IDs {expected_ids}. "
                f"Discarded {discarded} out-of-range lines."
            )

        if len(results) != len(expected_ids):
            raise ValueError(f"Line count validation failed. Expected {len(expected_ids)} lines, got {len(results)} lines.")

        for i, tl in enumerate(results):
            if tl.id != expected_ids[i]:
                raise ValueError(f"ID continuity validation failed. Expected ID {expected_ids[i]}, got {tl.id}.")

        return results
    else:
        try:
            text = response_text.strip()
            start_idx = text.find('{')
            end_idx = text.rfind('}')
            if start_idx != -1 and end_idx != -1 and end_idx > start_idx:
                json_str = text[start_idx:end_idx+1]
                parsed_json = json.loads(json_str)
            else:
                raise ValueError("No JSON object found in response.")
        except Exception as e:
            raise ValueError(f"Failed to parse LLM response as JSON: {e}")

        results = []
        missing_ids = []
        
        for line_id in expected_ids:
            str_id = str(line_id)
            if str_id in parsed_json:
                results.append(TranslatedLine(id=line_id, text=str(parsed_json[str_id]).strip()))
            else:
                missing_ids.append(line_id)
                
        if missing_ids:
            raise ValueError(f"Line count validation failed. Missing translations for IDs: {missing_ids}. Expected {len(expected_ids)} lines.")

        return results

def _build_output_with_line_map(tmp_path: str, out_path: str, line_map: List[Optional[int]]):
    """
    Backfills translation results into blank lines based on line_map to generate the final .zh.txt file.

    .tmp format: `ID: n | translated_text`
    .zh.txt format: Plain translated text, blank lines backfilled to original positions
    """
    # Read .tmp and build ID -> text mapping
    id_to_text = {}
    pattern = re.compile(r"^ID:\s*(\d+)\s*\|\s*(.+)$")
    with open(tmp_path, "r", encoding="utf-8") as f:
        for line in f:
            match = pattern.match(line.strip())
            if match:
                id_to_text[int(match.group(1))] = match.group(2).strip()

    # Backfill blank lines according to line_map
    with open(out_path, "w", encoding="utf-8") as f:
        for mapped_id in line_map:
            if mapped_id is None:
                f.write("\n")
            else:
                f.write(id_to_text.get(mapped_id, "") + "\n")

def validate_tmp_file(tmp_path: str, batch_size: int) -> int:
    """
    校验 .tmp 文件中的完整行数，对齐到批次边界

    逐行解析 .tmp 文件，统计连续递增 ID 的有效行数，
    然后将结果向下对齐到 batch_size 的整数倍（即只信任完整批次），
    防止半写批次被续传依赖。若文件不存在返回 0。
    """
    if not os.path.exists(tmp_path):
        return 0

    pattern = re.compile(r"^ID:\s*(\d+)\s*\|\s*(.+)$")
    consecutive_count = 0
    current_id = 1

    with open(tmp_path, "r", encoding="utf-8") as f:
        for line in f:
            stripped = line.strip()
            if not stripped:
                continue
            match = pattern.match(stripped)
            if match:
                line_id = int(match.group(1))
                if line_id == current_id:
                    consecutive_count = current_id
                    current_id += 1

    return (consecutive_count // batch_size) * batch_size


def compute_resume_batch_idx(current_batch: int, total_batches: int, tmp_path: str, batch_size: int) -> int:
    """
    基于 DB 记录和 .tmp 文件实际内容计算断点续传的起始批次索引

    策略：取 DB 和 .tmp 中较小的进度值作为续传起点，以 .tmp 为准（因为文件是实际持久化）。
    - 若 current_batch == 0（新任务），返回 1
    - 若 .tmp 文件不存在，返回 1（从头开始）
    - 否则以 .tmp 中的完整行数推算起始批次
    """
    if current_batch == 0 and total_batches == 0:
        return 1

    if not os.path.exists(tmp_path):
        return 1

    tmp_valid_lines = validate_tmp_file(tmp_path, batch_size)
    if tmp_valid_lines == 0:
        return 1

    tmp_batch_idx = (tmp_valid_lines - 1) // batch_size + 1
    resume_batch = min(current_batch, tmp_batch_idx)

    if resume_batch < 1:
        resume_batch = 1

    return resume_batch + 1


def truncate_tmp_for_resume(tmp_path: str, batch_size: int) -> int:
    """
    截断 .tmp 文件到最近的有效批次边界

    返回值：
    - -1：.tmp 文件不存在，调用方应将 start_batch_idx 重置为 1
    - 0：.tmp 存在但无有效行，调用方应将 start_batch_idx 重置为 1
    - >0：成功截断，返回保留的有效行数
    """
    if not os.path.exists(tmp_path):
        return -1

    valid_lines = validate_tmp_file(tmp_path, batch_size)
    if valid_lines == 0:
        return 0

    with open(tmp_path, "r", encoding="utf-8") as rf:
        all_lines = rf.readlines()
    pattern = re.compile(r"^ID:\s*(\d+)\s*\|\s*(.+)$")
    keep_lines = []
    kept_ids = 0
    for raw_line in all_lines:
        match = pattern.match(raw_line.strip())
        if match and int(match.group(1)) == kept_ids + 1:
            keep_lines.append(raw_line)
            kept_ids += 1
            if kept_ids >= valid_lines:
                break
    with open(tmp_path, "w", encoding="utf-8") as wf:
        wf.writelines(keep_lines)
    logger.info(f"Truncated .tmp to {valid_lines} valid lines for resume")
    return valid_lines


def translate_file(task_id: str, file_path: str, start_batch_idx: int = 1):
    """
    Main translation workflow: preprocess -> create_batches -> batch call_llm -> parse_response ->
    atomic append to .tmp -> update DB progress -> backfill blank lines after completion -> .zh.txt
    """
    task_tokens = 0
    try:
        lines, line_map = preprocess(file_path)
        if not lines:
            # Empty file (or all blank lines): generate .zh.txt with only blank lines based on line_map
            out_path = file_path.replace(".txt", ".zh.txt")
            if file_path.endswith(".en.txt"):
                out_path = file_path.replace(".en.txt", ".zh.txt")
            elif file_path.endswith(".ja.txt"):
                out_path = file_path.replace(".ja.txt", ".zh.txt")
            with open(out_path, "w", encoding="utf-8") as f:
                for _ in line_map:
                    f.write("\n")
            db.update_progress(task_id, 0, 0, "100%")
            db.complete_task(task_id, task_tokens)
            return

        batches = create_batches(lines)
        total_batches = len(batches)

        model_type = _get_config()["llm"]["model_type"]
        if model_type == "mt":
            system_prompt = load_mt_system_prompt()
        else:
            system_prompt = load_system_prompt()
        glossary = load_glossary()

        tmp_path = file_path + ".tmp"

        if start_batch_idx > 1:
            result = truncate_tmp_for_resume(tmp_path, _get_config()["llm"]["batch_size"])
            if result < 1:
                logger.warning(f"Task {task_id}: Cannot resume (truncate result={result}), resetting to batch 1")
                start_batch_idx = 1

        failed_batches = []
        prev_batch_failed = False

        with open(tmp_path, "w" if start_batch_idx == 1 else "a", encoding="utf-8") as tmp_file:
            for batch in batches[start_batch_idx - 1:]:
                if prev_batch_failed:
                    batch = Batch(
                        batch_idx=batch.batch_idx,
                        lines=batch.lines,
                        context_before=[],
                        start_id=batch.start_id,
                        end_id=batch.end_id
                    )

                # Build user_prompt based on model type
                if model_type == "mt":
                    # MT model: pre-context and translation content tiled in plain text, no instruction words
                    all_lines = batch.context_before + batch.lines
                    user_prompt = "\n".join(all_lines)
                else:
                    # Chat model: keep semantic instruction markers
                    user_prompt = ""
                    if batch.context_before:
                        user_prompt += "前文上下文（仅供参考，不要翻译）：\n" + "\n".join(batch.context_before) + "\n\n"
                    user_prompt += "需要翻译的内容：\n" + "\n".join(batch.lines)

                expected_ids = list(range(batch.start_id, batch.end_id + 1))
                translation_results = []

                max_retries = _get_config()["llm"]["max_retries"]
                base_temperature = _get_config()["llm"]["temperature"]
                validation_failures = 0
                response_text = None
                for attempt in range(max_retries + 1):
                    current_temperature = max(0.1, base_temperature - 0.1 * validation_failures)
                    try:
                        start_time = time.time()
                        response_text, tokens = call_llm(system_prompt, user_prompt, glossary, model_type=model_type, temperature=current_temperature)
                        task_tokens += tokens
                        elapsed = time.time() - start_time
                        
                        # Add duration to sliding window
                        eta_tracker.add_duration(elapsed)
                        
                        logger.info("API call completed", extra={
                            "task_id": task_id,
                            "batch_id": batch.batch_idx,
                            "elapsed": round(elapsed, 2)
                        })
                        
                        logger.debug(f"LLM response text:\n{response_text}")

                        try:
                            translation_results = parse_response(response_text, expected_ids, model_type=model_type)
                        except ValueError as ve:
                            logger.error(f"Validation failed during parsing.\n=== User Prompt ===\n{user_prompt}\n=== LLM Response ===\n{response_text}\n===================")
                            validation_failures += 1
                            raise ve
                        break
                    except Exception as e:
                        if attempt >= max_retries:
                            # 重试耗尽，写入错误诊断快照后再跳过
                            _save_error_snapshot(
                                task_id=task_id,
                                batch_idx=batch.batch_idx,
                                error_reason=str(e),
                                system_prompt=system_prompt,
                                glossary=glossary,
                                user_prompt=user_prompt,
                                llm_response=response_text
                            )
                            logger.warning(
                                f"Task {task_id}: Batch {batch.batch_idx} failed after {max_retries} retries, "
                                f"filling original text and skipping"
                            )
                            failed_batches.append(batch.batch_idx)
                            translation_results = []
                            break
                        sleep_time = 5 * (2 ** attempt)
                        logger.warning(f"LLM Error: {e}, retrying in {sleep_time}s...")
                        time.sleep(sleep_time)


                if not translation_results and batch.batch_idx in failed_batches:
                    line_id = batch.start_id
                    for line in batch.lines:
                        tmp_file.write(f"ID: {line_id} | {line}\n")
                        line_id += 1
                    prev_batch_failed = True
                else:
                    for result in translation_results:
                        tmp_file.write(f"ID: {result.id} | {result.text}\n")
                    prev_batch_failed = False
                
                tmp_file.flush()
                os.fsync(tmp_file.fileno())

                progress_pct = int(batch.batch_idx / total_batches * 100)
                db.update_progress(task_id, batch.batch_idx, total_batches, f"{progress_pct}%")

                logger.info(f"Task {task_id}: Batch {batch.batch_idx}/{total_batches} done ({progress_pct}%)")

                if batch.batch_idx < total_batches:
                    time.sleep(1)  # Simple throttling between batches

        # Translation complete: backfill blank lines based on line_map to generate final .zh.txt
        out_path = file_path.replace(".txt", ".zh.txt")
        if file_path.endswith(".en.txt"):
            out_path = file_path.replace(".en.txt", ".zh.txt")
        elif file_path.endswith(".ja.txt"):
            out_path = file_path.replace(".ja.txt", ".zh.txt")

        _build_output_with_line_map(tmp_path, out_path, line_map)

        # Clean up .tmp intermediate file
        os.remove(tmp_path)

        db.complete_task(task_id, task_tokens, skipped_batches=failed_batches if failed_batches else None)
        if failed_batches:
            logger.warning(
                f"Task {task_id} completed with {len(failed_batches)} skipped batches: {failed_batches}"
            )
        else:
            logger.info(f"Task {task_id} completed: {out_path} (Tokens used: {task_tokens})")

    except Exception as e:
        db.fail_task(task_id, str(e))
        logger.error(f"Task {task_id} failed with error: {e}")


def retry_skipped_batches(task_id: str, file_path: str, skipped_batches: list):
    """
    精准重试失败的批次。
    读取原始 .txt 文件，只对 skipped_batches 中的批次调 LLM。
    成功则更新 .zh.txt 中对应行，失败则保留原文。
    """
    task_tokens = 0
    lines, line_map = preprocess(file_path)
    if not lines:
        return
        
    batches = create_batches(lines)
    
    out_path = file_path.replace(".txt", ".zh.txt")
    if file_path.endswith(".en.txt"):
        out_path = file_path.replace(".en.txt", ".zh.txt")
    elif file_path.endswith(".ja.txt"):
        out_path = file_path.replace(".ja.txt", ".zh.txt")
        
    if not os.path.exists(out_path):
        logger.error(f"Cannot retry task {task_id}: {out_path} not found")
        return

    with open(out_path, "r", encoding="utf-8") as f:
        zh_lines = f.read().splitlines()
        
    model_type = _get_config()["llm"]["model_type"]
    if model_type == "mt":
        system_prompt = load_mt_system_prompt()
    else:
        system_prompt = load_system_prompt()
    glossary = load_glossary()
    
    still_failed = []
    
    for batch_idx in skipped_batches:
        if batch_idx > len(batches):
            continue
            
        batch = batches[batch_idx - 1]
        
        prev_idx = batch_idx - 1
        if prev_idx > 0:
            if prev_idx in skipped_batches:
                batch = Batch(
                    batch_idx=batch.batch_idx,
                    lines=batch.lines,
                    context_before=[],
                    start_id=batch.start_id,
                    end_id=batch.end_id
                )
            else:
                prev_batch = batches[prev_idx - 1]
                try:
                    orig_start = line_map.index(prev_batch.start_id)
                    orig_end = line_map.index(prev_batch.end_id)
                    translated_context = [l for l in zh_lines[orig_start:orig_end+1] if l.strip()]
                    batch = Batch(
                        batch_idx=batch.batch_idx,
                        lines=batch.lines,
                        context_before=translated_context,
                        start_id=batch.start_id,
                        end_id=batch.end_id
                    )
                except ValueError:
                    pass
        if model_type == "mt":
            all_lines = batch.context_before + batch.lines
            user_prompt = "\n".join(all_lines)
        else:
            user_prompt = ""
            if batch.context_before:
                user_prompt += "前文上下文（仅供参考，不要翻译）：\n" + "\n".join(batch.context_before) + "\n\n"
            user_prompt += "需要翻译的内容：\n" + "\n".join(batch.lines)
            
        expected_ids = list(range(batch.start_id, batch.end_id + 1))
        translation_results = []
        max_retries = _get_config()["llm"]["max_retries"]
        base_temperature = _get_config()["llm"]["temperature"]
        validation_failures = 0
        response_text = None
        
        for attempt in range(max_retries + 1):
            current_temperature = max(0.1, base_temperature - 0.1 * validation_failures)
            try:
                response_text, tokens = call_llm(system_prompt, user_prompt, glossary, model_type=model_type, temperature=current_temperature)
                task_tokens += tokens
                translation_results = parse_response(response_text, expected_ids, model_type=model_type)
                break
            except ValueError as e:
                if attempt >= max_retries:
                    _save_error_snapshot(
                        task_id=task_id,
                        batch_idx=batch.batch_idx,
                        error_reason=str(e),
                        system_prompt=system_prompt,
                        glossary=glossary,
                        user_prompt=user_prompt,
                        llm_response=response_text
                    )
                    still_failed.append(batch.batch_idx)
                    break
                validation_failures += 1
                time.sleep(5 * (2 ** attempt))
            except Exception as e:
                if attempt >= max_retries:
                    _save_error_snapshot(
                        task_id=task_id,
                        batch_idx=batch.batch_idx,
                        error_reason=str(e),
                        system_prompt=system_prompt,
                        glossary=glossary,
                        user_prompt=user_prompt,
                        llm_response=response_text
                    )
                    still_failed.append(batch.batch_idx)
                    break
                time.sleep(5 * (2 ** attempt))
                
        if translation_results:
            for result in translation_results:
                try:
                    orig_idx = line_map.index(result.id)
                    if orig_idx < len(zh_lines):
                        zh_lines[orig_idx] = result.text
                except ValueError:
                    pass
                    
    with open(out_path, "w", encoding="utf-8") as f:
        for line in zh_lines:
            f.write(line + "\n")
            
    db.complete_task(task_id, task_tokens, skipped_batches=still_failed if still_failed else None)
    if still_failed:
        logger.warning(f"Task {task_id} retry completed with {len(still_failed)} still skipped batches: {still_failed}")
    else:
        logger.info(f"Task {task_id} retry completed successfully: {out_path}")
