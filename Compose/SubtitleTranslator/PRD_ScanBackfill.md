# SubtitleTranslator — 扫描回填 (Scan Backfill) 产品需求文档

> 日期: 2026-05-25
> 状态: 草稿 (待确认)

---

## 一、 背景与目标

当前系统的定时扫描器 (`media_scanner.scan_directory`) 在发现媒体文件已有 `.zh.*.srt` 字幕时，会直接跳过该文件，不在数据库中创建任何记录。这意味着：

1. **数据库清空后重新扫描时**，所有以前成功处理过的媒体文件（已有 `.zh.ai.srt`、`.zh.opencc.srt` 等产物）在历史记录中完全不可见。
2. **Dashboard 统计失真**：已处理过的文件数量归零，无法反映真实的库存覆盖情况。
3. **`funnel_level` 丢失**：无法通过 WebUI 了解每个媒体文件的字幕来源等级。

**核心目标：**
在扫描器的跳过分支中增加"回填"逻辑，根据已有字幕文件的命名规则反推 `funnel_level`，在数据库中创建对应的 `subtitle_job` 记录，确保即使数据库被清空重建后，历史数据仍可完整恢复。

---

## 二、 核心概念：语义分层

本次迭代确立了 `level` 与 `status` 的语义分离：

| 维度 | 字段 | 含义 |
|:--|:--|:--|
| **数据维度** | `funnel_level` | 该媒体文件的字幕处于什么等级（0/1/2/3） |
| **行为维度** | `status` | 本次处理系统执行了什么动作（`done` = 执行了实际工作，`skipped` = 无需处理） |

这意味着：
- 正常翻译完成的 job：`level=2, status=done`（执行了翻译动作）
- 回填发现的 `.ai.srt`：`level=2, status=skipped`（发现已有翻译产物，无需处理）
- 两者通过 `level` 都能判断"该文件有翻译字幕"，通过 `status` 区分"是否为本次处理的产物"

---

## 三、 核心业务逻辑

### 3.1 文件名到 Level 的映射规则

扫描器在检测到 `.zh.*.srt` 文件时，根据文件名后缀反推来源等级：

| 文件名模式 | 推断 `funnel_level` | 说明 |
|:--|:--|:--|
| `*.zh.ai.srt` | 2 | LLM 翻译产物 |
| `*.zh.opencc.srt` | 1 | OpenCC 繁简转换产物 |
| 其他 `*.zh.srt` / `*.zh-*.srt` | 0 | 原生中文字幕 |

> **设计决策**：`.ai.srt` 统一归为 Level 2（外置字幕翻译），不区分 Level 2 与 Level 3。原因是文件名无法反推该翻译来源于外置字幕还是内嵌轨道，且这一区分对历史回溯价值不大。

### 3.2 回填记录的字段填充

回填创建的 `subtitle_job` 记录字段规则：

| 字段 | 填充方式 |
|:--|:--|
| `id` | 新生成 UUID |
| `media_path` | 媒体文件完整路径 |
| `status` | **`skipped`**（所有回填统一） |
| `funnel_level` | 按 3.1 规则推断 |
| `output_srt_path` | 触发回填的 `.zh.*.srt` 文件完整路径 |
| `original_srt_path` | NULL |
| `translate_task_id` | NULL（翻译任务记录已随 DB 丢失，无法重建） |
| `cleanup_files` | NULL |
| `source` | `"scheduler"`（回填由定时扫描触发，保持触发来源维度一致；回填身份通过 `level + status` 组合自然识别） |
| `created_at` | 当前时间 |
| `updated_at` | 当前时间 |
| `completed_at` | 当前时间 |
| `error` | NULL |

### 3.3 去重策略

由于触发回填的 `.zh.*.srt` 文件始终存在于磁盘上，每次定时扫描都会进入回填分支。回填函数内部必须实现幂等去重：

```
回填前查询: SELECT id FROM subtitle_job WHERE media_path = ? LIMIT 1
```

- **无记录** → 执行 INSERT
- **有记录**（任意状态） → 跳过

> **与 `create_job()` 的区别**：`create_job()` 的去重 SQL 使用 `NOT IN ('done','failed','skipped')` 排除终态记录，允许对已完成文件重新提交任务（支持失败重试）。回填函数使用全量去重，防止每次扫描重复插入。

### 3.4 回填触发位置

回填逻辑嵌入 `media_scanner.scan_directory()` 的现有跳过分支中：

```python
# 现有逻辑
if has_zh:
    logger.debug(f"Skipping {f}, already has .zh.*.srt subtitle")
    continue

# 改为
if has_zh:
    backfill_job(file_path, zh_subtitle_filename)  # 新增
    continue  # 原有跳过逻辑不变
```

---

## 四、 对现有统计的影响

### 4.1 不受影响的指标
- **success_rate**：公式为 `done / (done + failed)`，回填记录为 `skipped`，不参与分子分母
- **avg_duration_seconds**：仅统计 `status='done'` 的记录，回填不参与
- **avg_translate_seconds**：仅统计有 `translate_task` 的记录，回填无 task 不参与

### 4.2 受影响的指标
- **skipped 计数**：会增加，但语义正确——这些文件确实是"无需处理"的

---

## 五、 影响范围

| 层 | 文件 | 变更 |
|:--|:--|:--|
| 扫描层 | `app/scanner/media_scanner.py` | 跳过分支新增回填调用 |
| 数据层 | `app/core/db.py` | 新增 `backfill_job()` 函数 |
| 测试层 | `app/tests/test_scan_backfill.py` | 新增回填逻辑的单元测试 |

### 不涉及的变更
- **数据库 Schema**：无变更，复用现有 `subtitle_job` 表
- **前端 WebUI**：无变更，回填记录通过现有 History 页面自然展示
- **API 层**：无变更
- **Funnel / Consumer / Translator**：不涉及

---

## 六、 验收标准

1. **空库回填**：清空 DB 后触发扫描，已有 `.zh.ai.srt` / `.zh.opencc.srt` / `.zh.srt` 的媒体文件应在 History 中可见，且 `funnel_level` 正确
2. **幂等性**：连续触发两次扫描，不产生重复的 `subtitle_job` 记录
3. **统计无污染**：回填记录不影响 `success_rate`、`avg_duration`、`avg_translate_seconds`
4. **正常流程不受影响**：无 `.zh.*.srt` 的媒体文件仍正常进入翻译流程
