# SubtitleTranslator Phase 3 — 专属字幕命名与防冲突规范

> 版本: 1.0
> 日期: 2026-05-03
> 状态: 已锁定，完全对齐

---

## 一、核心定位与目标

为解决 `SubtitleTranslator` 自身处理/生成的字幕文件与外部后续自动或手动下载的字幕发生命名冲突，制定本规范。
通过为 AI 翻译及繁转简生成的字幕文件引入特定的后缀标识符，并使用通配符匹配机制，彻底防止冲突并实现免重复扫描的去重逻辑。

## 二、命名格式规范

所有由本工具生成、转换或重构的字幕文件，必须统一采用以下格式：

```text
[视频文件名].zh.[标识符].srt
```

### 2.1 具体场景与标识符约定

| 场景 | 后缀格式 | 示例 | 触发模块 |
|---|---|---|---|
| **繁转简字幕** (Level 1) | `[视频名].zh.opencc.srt` | `MyVideo.zh.opencc.srt` | OpenCC 转换 |
| **AI 翻译字幕** (Level 2/3) | `[视频名].zh.ai.srt` | `MyVideo.zh.ai.srt` | 翻译 + SRT 回写 |
| **混合场景** (翻译后转简) | `[视频名].zh.ai.opencc.srt` | `MyVideo.zh.ai.opencc.srt` | 复合管道（兼容项） |

### 2.2 命名防冲突的原理与优势
- **100% 隔离外部冲突**：Bazarr、TinyMediaManager 等第三方下载刮削工具绝对不会生成含有 `zh.ai` 或 `zh.opencc` 的后缀。因此即使下载了 `.zh.srt` 或 `.zh-CN.srt`，也绝不会覆盖本工具自身的产出。
- **媒体播放器无缝识别**：Emby / Jellyfin / Plex 等播放器扫描到 `.zh.` 就会正确提取为 `中文` (Chinese) 语言轨道，而后方的标识符（如 `ai`）则通常会被播放器显示为附加标签（如 `Chinese (ai)`）或安全忽略，不影响观影体验。

---

## 三、扫描与去重逻辑（判断是不是自己的产出）

本工具在扫描媒体库和漏斗筛选时，**直接使用 `zh.*.srt` 通配符**，实现对专属产出及现有中文字幕的一体化匹配。

### 3.1 磁盘扫描去重 (`media_scanner.py`)
在遍历视频文件时，程序需检测当前目录下是否已经存在该视频的中文字幕，如果存在则直接跳过扫描。
- **匹配规则**：
  若当前目录下存在满足 `[视频文件名].zh.srt` **或** 满足 `[视频文件名].zh.*.srt` 的文件，则直接跳过该媒体文件，避免无限循环。

### 3.2 漏斗分流匹配 (`funnel.py`)
在执行漏斗筛选 (`evaluate_media`) 时：
- **匹配规则**：
  在识别外置字幕 (`scan_external_subtitles`) 时，同样检测 `[视频文件名].zh.srt` 及所有 `[视频文件名].zh.*.srt` 字幕。若匹配到，则将其归类为语言 `zh`。
- **分流决策**：
  漏斗识别到 `zh` 存在后，判定为 **Level 0 Skip**，Worker 立即释放，直接结束任务。

---

## 四、具体模块代码调整路线

### 4.1 `app/pipeline/funnel.py`
- 更改 `scan_external_subtitles` 中对中文字幕的匹配。将任何以 `[视频文件名].zh.` 开头、并以 `.srt` 结尾的文件全部归类为 `zh`。
- 在 `execute_funnel_action` 中：
  - Level 1 繁转简输出路径设为 `f"{media_stem}.zh.opencc.srt"`。
  - Level 2/3 翻译后的输出路径设为 `f"{media_stem}.zh.ai.srt"`。

### 4.2 `app/scanner/media_scanner.py`
- 在 `scan_directory` 遍历文件的逻辑中，修改跳过判断条件。通过 `os.listdir(root)` 查找是否存在以 `f"{media_stem}.zh."` 开头并以 `".srt"` 结尾的文件，或是否存在完全相等的 `f"{media_stem}.zh.srt"`。

### 4.3 `app/pipeline/consumer.py`
- 优化 `rebuild_srt` 兜底（Fallback）输出路径：
  ```python
  output_srt_path = job.get("output_srt_path") or os.path.join(media_dir, f"{media_stem}.zh.ai.srt")
  ```

---
[返回总索引](../../README.md)
