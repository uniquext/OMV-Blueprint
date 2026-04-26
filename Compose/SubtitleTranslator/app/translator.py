import os
import re
import httpx
import time
import json
from typing import List, Dict, Optional, Tuple
from dataclasses import dataclass
from prompt_loader import load_system_prompt, load_glossary
import queue_db
from rate_limiter import rate_limiter
import logging
from eta_tracker import eta_tracker

logger = logging.getLogger(__name__)


LLM_API_URL = os.environ.get("LLM_API_URL")
LLM_API_KEY = os.environ.get("LLM_API_KEY")
LLM_MODEL = os.environ.get("LLM_MODEL")
LLM_TIMEOUT = int(os.environ.get("LLM_TIMEOUT", "120"))
BATCH_SIZE = int(os.environ.get("BATCH_SIZE", "20"))
CONTEXT_SIZE = int(os.environ.get("CONTEXT_SIZE", "5"))
# Model type: chat (General dialogue model) / mt (Specialized translation model)
LLM_MODEL_TYPE = os.environ.get("LLM_MODEL_TYPE", "chat")

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

def create_batches(lines: List[str], batch_size: int = BATCH_SIZE, context_size: int = CONTEXT_SIZE) -> List[Batch]:
    """Batches numbered lines and appends a pre-context window for each batch (pre-context only, no post-context)."""
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

def call_llm(system_prompt: str, user_prompt: str, glossary: Dict, model_type: str = "chat") -> str:
    """
    Uses httpx to call the LLM API and returns the raw response text.

    User_prompt construction depends on model_type:
    - chat: Injects glossary into user_prompt
    - mt: Sends plain text directly, no glossary injection
    """
    headers = {
        "Authorization": f"Bearer {LLM_API_KEY}",
        "Content-Type": "application/json"
    }

    # MT model does not inject glossary, Chat model injects normally
    if model_type == "mt":
        final_user_content = user_prompt
    else:
        final_user_content = f"Glossary: {json.dumps(glossary, ensure_ascii=False)}\n\n{user_prompt}"

    payload = {
        "model": LLM_MODEL,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": final_user_content}
        ],
        "temperature": 0.3
    }

    with httpx.Client(timeout=float(LLM_TIMEOUT)) as client:
        rate_limiter.pre_request_check()
        response = client.post(LLM_API_URL, headers=headers, json=payload)
        response.raise_for_status()
        data = response.json()

        usage = data.get("usage", {})
        total_tokens = usage.get("total_tokens", 0)
        logger.info(f"LLM token_usage", extra={"token_usage": usage})
        rate_limiter.post_request_update(total_tokens)

        return data["choices"][0]["message"]["content"]


def parse_response(response_text: str, expected_ids: List[int]) -> List[TranslatedLine]:
    """
    Lenient parser: compatible with various LLM output formats.

    Uses lenient regex `^ID:\\s*(\\d+)\\s*\\|\\s*(.+)$` to capture all content after the pipe symbol,
    no longer requiring tag keywords (original/translated/Translation, etc.).

    Parsing strategy: filter by ID range first (discard noise from translated context lines or MT model
    results that include pre-context translation), then validate line count and continuity.
    """
    results = []
    # Lenient regex: captures all content after the pipe symbol
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
            # Only keep lines within the current batch ID range, discard context noise
            if line_id in expected_id_set:
                results.append(TranslatedLine(id=line_id, text=translated_text))

    if len(results) != len(expected_ids):
        raise ValueError(f"Line count validation failed. Expected {len(expected_ids)} lines, got {len(results)} lines.")

    for i, tl in enumerate(results):
        if tl.id != expected_ids[i]:
            raise ValueError(f"ID continuity validation failed. Expected ID {expected_ids[i]}, got {tl.id}.")

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

def translate_file(task_id: str, file_path: str, start_batch_idx: int = 1):
    """
    Main translation workflow: preprocess -> create_batches -> batch call_llm -> parse_response ->
    atomic append to .tmp -> update DB progress -> backfill blank lines after completion -> .zh.txt
    """
    try:
        lines, line_map = preprocess(file_path)
        if not lines:
            # Empty file (or all blank lines): generate .zh.txt with only blank lines based on line_map
            out_path = file_path.replace(".txt", ".zh.txt")
            if file_path.endswith(".en.txt"):
                out_path = file_path.replace(".en.txt", ".zh.txt")
            with open(out_path, "w", encoding="utf-8") as f:
                for _ in line_map:
                    f.write("\n")
            queue_db.update_progress(task_id, 0, 0, "100%")
            queue_db.complete_task(task_id)
            return

        batches = create_batches(lines)
        total_batches = len(batches)

        system_prompt = load_system_prompt()
        glossary = load_glossary()

        tmp_path = file_path + ".tmp"

        with open(tmp_path, "w" if start_batch_idx == 1 else "a", encoding="utf-8") as tmp_file:
            for batch in batches[start_batch_idx - 1:]:
                # Build user_prompt based on model type
                if LLM_MODEL_TYPE == "mt":
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

                max_retries = 4
                for attempt in range(max_retries):
                    try:
                        start_time = time.time()
                        response_text = call_llm(system_prompt, user_prompt, glossary, model_type=LLM_MODEL_TYPE)
                        elapsed = time.time() - start_time
                        
                        # Add duration to sliding window
                        eta_tracker.add_duration(elapsed)
                        
                        logger.info("API call completed", extra={
                            "task_id": task_id,
                            "batch_id": batch.batch_idx,
                            "elapsed": round(elapsed, 2)
                        })
                        
                        logger.debug(f"LLM response text:\n{response_text}")

                        translation_results = parse_response(response_text, expected_ids)
                        break
                    except Exception as e:
                        if attempt == max_retries - 1:
                            raise e
                        sleep_time = 5 * (2 ** attempt)
                        logger.warning(f"LLM Error: {e}, retrying in {sleep_time}s...")
                        time.sleep(sleep_time)


                # Atomic append: write to .tmp in batch units (format ID: n | text)
                for result in translation_results:
                    tmp_file.write(f"ID: {result.id} | {result.text}\n")
                tmp_file.flush()
                os.fsync(tmp_file.fileno())

                progress_pct = int(batch.batch_idx / total_batches * 100)
                queue_db.update_progress(task_id, batch.batch_idx, total_batches, f"{progress_pct}%")

                logger.info(f"Task {task_id}: Batch {batch.batch_idx}/{total_batches} done ({progress_pct}%)")

                if batch.batch_idx < total_batches:
                    time.sleep(1)  # Simple throttling between batches

        # Translation complete: backfill blank lines based on line_map to generate final .zh.txt
        out_path = file_path.replace(".txt", ".zh.txt")
        if file_path.endswith(".en.txt"):
            out_path = file_path.replace(".en.txt", ".zh.txt")

        _build_output_with_line_map(tmp_path, out_path, line_map)

        # Clean up .tmp intermediate file
        os.remove(tmp_path)

        queue_db.complete_task(task_id)
        logger.info(f"Task {task_id} completed: {out_path}")

    except Exception as e:
        queue_db.fail_task(task_id, str(e))
        logger.error(f"Task {task_id} failed with error: {e}", exc_info=True)
        raise e
