import os
import re
import httpx
import time
import json
from typing import List, Dict, Optional, Tuple
from dataclasses import dataclass
from prompt_loader import load_system_prompt, load_glossary
import queue_db

LLM_API_URL = os.environ.get("LLM_API_URL")
LLM_API_KEY = os.environ.get("LLM_API_KEY")
LLM_MODEL = os.environ.get("LLM_MODEL")
LLM_TIMEOUT = int(os.environ.get("LLM_TIMEOUT", "120"))
BATCH_SIZE = int(os.environ.get("BATCH_SIZE", "50"))
CONTEXT_SIZE = int(os.environ.get("CONTEXT_SIZE", "5"))
# 模型类型: chat (通用对话模型) / mt (专用翻译模型)
LLM_MODEL_TYPE = os.environ.get("LLM_MODEL_TYPE", "chat")

@dataclass
class Batch:
    """一个翻译批次，包含当前批次的行和前文上下文"""
    batch_idx: int
    lines: List[str]
    context_before: List[str]
    start_id: int
    end_id: int

@dataclass
class TranslatedLine:
    """解析后的单行翻译结果"""
    id: int
    text: str

def preprocess(file_path: str) -> Tuple[List[str], List[Optional[int]]]:
    """
    读取文件每行，构建空行映射表，返回编号后的格式化列表和 line_map。

    格式化列表: `ID: n | text`（无标签关键词，统一所有模型）
    line_map: 索引=原始行号，值=分配的 ID（非空行）或 None（空行）
    """
    with open(file_path, "r", encoding="utf-8") as f:
        lines = f.readlines()

    formatted_lines = []
    # line_map[原始行号] = ID 或 None（空行）
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
    """将编号行分批并为每批附加前文上下文窗口（仅前文，无后文上下文）"""
    batches = []
    total_lines = len(lines)
    batch_idx = 1
    for i in range(0, total_lines, batch_size):
        batch_lines = lines[i:i + batch_size]
        start_id = i + 1
        end_id = min(i + batch_size, total_lines)

        # 仅前文上下文
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
    使用 httpx 调用 LLM API，返回原始响应文本。

    根据 model_type 控制 user_prompt 的构造：
    - chat: 注入术语表到 user_prompt
    - mt: 直接发送纯文本，不注入术语表
    """
    headers = {
        "Authorization": f"Bearer {LLM_API_KEY}",
        "Content-Type": "application/json"
    }

    # MT 模型不注入术语表，Chat 模型正常注入
    if model_type == "mt":
        final_user_content = user_prompt
    else:
        final_user_content = f"术语表：{json.dumps(glossary, ensure_ascii=False)}\n\n{user_prompt}"

    payload = {
        "model": LLM_MODEL,
        "messages": [
            {"role": "system", "content": system_prompt},
            {"role": "user", "content": final_user_content}
        ],
        "temperature": 0.3
    }

    with httpx.Client(timeout=float(LLM_TIMEOUT)) as client:
        response = client.post(LLM_API_URL, headers=headers, json=payload)
        response.raise_for_status()
        data = response.json()

        usage = data.get("usage", {})
        print(f"[LLM] token_usage: {usage}")

        return data["choices"][0]["message"]["content"]

def parse_response(response_text: str, expected_ids: List[int]) -> List[TranslatedLine]:
    """
    宽容解析器：兼容多种 LLM 输出格式。

    使用放宽正则 `^ID:\\s*(\\d+)\\s*\\|\\s*(.+)$`，管道符后直接捕获全部内容，
    不再要求任何标签关键词（原文/译文/Translation 等）。

    解析策略：先按 ID 范围过滤（丢弃上下文行被翻译的噪声或 MT 模型
    翻译了前上下文的结果），再做行数和连续性校验。
    """
    results = []
    # 宽容正则：管道符后直接捕获全部内容
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
            # 仅保留当前批次 ID 范围内的行，丢弃上下文噪声
            if line_id in expected_id_set:
                results.append(TranslatedLine(id=line_id, text=translated_text))

    if len(results) != len(expected_ids):
        raise ValueError(f"行数校验失败。期望 {len(expected_ids)} 行，实际 {len(results)} 行。")

    for i, tl in enumerate(results):
        if tl.id != expected_ids[i]:
            raise ValueError(f"ID 连续性校验失败。期望 ID {expected_ids[i]}，实际 {tl.id}。")

    return results

def _build_output_with_line_map(tmp_path: str, out_path: str, line_map: List[Optional[int]]):
    """
    根据 line_map 将 .tmp 文件中的翻译结果回填空行，生成最终的 .zh.txt 文件。

    .tmp 格式: `ID: n | translated_text`
    .zh.txt 格式: 纯译文，空行按原始位置回填
    """
    # 读取 .tmp 并构建 ID -> 译文 的映射
    id_to_text = {}
    pattern = re.compile(r"^ID:\s*(\d+)\s*\|\s*(.+)$")
    with open(tmp_path, "r", encoding="utf-8") as f:
        for line in f:
            match = pattern.match(line.strip())
            if match:
                id_to_text[int(match.group(1))] = match.group(2).strip()

    # 按 line_map 回填空行
    with open(out_path, "w", encoding="utf-8") as f:
        for mapped_id in line_map:
            if mapped_id is None:
                f.write("\n")
            else:
                f.write(id_to_text.get(mapped_id, "") + "\n")

def translate_file(task_id: str, file_path: str):
    """
    翻译主流程：preprocess → create_batches → 逐批 call_llm → parse_response →
    原子化追加写入 .tmp → 更新数据库进度 → 全部完成后按 line_map 回填空行 → .zh.txt
    """
    try:
        lines, line_map = preprocess(file_path)
        if not lines:
            # 空文件（或全是空行）：根据 line_map 生成仅含空行的 .zh.txt
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

        # Phase 5 基础实现：从头开始，断点续传在 Phase 8 实现
        start_batch_idx = 1

        with open(tmp_path, "w" if start_batch_idx == 1 else "a", encoding="utf-8") as tmp_file:
            for batch in batches[start_batch_idx - 1:]:
                # 根据模型类型构建 user_prompt
                if LLM_MODEL_TYPE == "mt":
                    # MT 模型：前上下文与翻译内容纯文本平铺，无指令词
                    all_lines = batch.context_before + batch.lines
                    user_prompt = "\n".join(all_lines)
                else:
                    # Chat 模型：保留语义化指令标注
                    user_prompt = ""
                    if batch.context_before:
                        user_prompt += "前文上下文（仅供参考，不要翻译）：\n" + "\n".join(batch.context_before) + "\n\n"
                    user_prompt += "需要翻译的内容：\n" + "\n".join(batch.lines)

                expected_ids = list(range(batch.start_id, batch.end_id + 1))
                translation_results = []

                max_retries = 3
                for attempt in range(max_retries):
                    try:
                        start_time = time.time()
                        response_text = call_llm(system_prompt, user_prompt, glossary, model_type=LLM_MODEL_TYPE)
                        elapsed = time.time() - start_time
                        print(f"[LLM] roundtrip time: {elapsed:.2f}s")
                        print(f"[LLM] response text:\n{response_text}")

                        translation_results = parse_response(response_text, expected_ids)
                        break
                    except Exception as e:
                        if attempt == max_retries - 1:
                            raise e
                        sleep_time = 2 ** attempt
                        print(f"[LLM] Error: {e}, retrying in {sleep_time}s...")
                        time.sleep(sleep_time)

                # 原子化追加写入：以批次为单位写入 .tmp（格式 ID: n | text）
                for result in translation_results:
                    tmp_file.write(f"ID: {result.id} | {result.text}\n")
                tmp_file.flush()
                os.fsync(tmp_file.fileno())

                progress_pct = int(batch.batch_idx / total_batches * 100)
                queue_db.update_progress(task_id, batch.batch_idx, total_batches, f"{progress_pct}%")

                print(f"[Translator] Batch {batch.batch_idx}/{total_batches} done ({progress_pct}%)")

                if batch.batch_idx < total_batches:
                    time.sleep(1)  # 批次间简单节流

        # 翻译完成：根据 line_map 回填空行，生成最终 .zh.txt
        out_path = file_path.replace(".txt", ".zh.txt")
        if file_path.endswith(".en.txt"):
            out_path = file_path.replace(".en.txt", ".zh.txt")

        _build_output_with_line_map(tmp_path, out_path, line_map)

        # 清理 .tmp 中间文件
        os.remove(tmp_path)

        queue_db.complete_task(task_id)
        print(f"[Translator] Task {task_id} completed: {out_path}")

    except Exception as e:
        queue_db.fail_task(task_id, str(e))
        raise e
