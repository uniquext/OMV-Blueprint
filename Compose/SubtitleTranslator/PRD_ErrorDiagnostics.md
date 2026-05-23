# SubtitleTranslator — 翻译错误诊断 (Error Diagnostics) 产品需求文档

> 日期: 2026-05-23
> 状态: 已确认 (准备开发)

---

## 一、 背景与目标

在使用大模型（LLM）进行字幕翻译时，由于模型的非确定性（幻觉），经常会出现模型未按严格行数返回结果、漏翻、合并行或输出无意义格式的现象。当前系统一旦遇到这类解析失败（如 `Line count validation failed`），会直接将整个任务标记为 `Failed`。

由于这种失败的根本原因往往和当前的上下文（System Prompt、特定的一批台词）及 LLM 侧的设置（Temperature、具体模型版本）强相关。为便于后续精准排障与提示词优化，迫切需要建立一套**“翻译错误诊断快照”**机制。

**核心目标：**
当翻译任务遭遇彻底失败时，持久化保存当时的完整上下文和报错原因，并在 WebUI 的历史记录中提供友好的对开面板对比视图。

---

## 二、 核心业务逻辑

1. **一对多关系（方案 B）**：一个翻译任务 (`translate_task`) 可以对应多条错误记录。尽管绝大多数情况下任务在第一次严重错误后就会终止（1条记录），但架构上必须支持一对多，为未来的“部分跳过”容错机制留出空间。
2. **记录时机**：**仅当 API 请求重试次数全部耗尽**（`attempt >= max_retries`）或在解析阶段抛出不可恢复的异常（如 `ValueError`）且即将抛出终态 Failed 前，才进行错误快照写入。如果前几次请求失败但后续重试成功了，则**不记录**，保持数据库整洁。
3. **前端懒加载**：由于 `System Prompt` 和 `LLM Response` 动辄数千字符，为防止撑爆常规的 `/api/history` 列表接口，错误诊断数据必须通过专属接口**按需加载**。

---

## 三、 数据层设计 (Database)

在 SQLite 数据库中新增 `translation_errors` 表：

```sql
CREATE TABLE IF NOT EXISTS translation_errors (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    task_id TEXT NOT NULL,           -- 外键，关联 translate_task(id)
    batch_idx INTEGER,               -- 发生错误的批次号
    error_reason TEXT NOT NULL,      -- 错误摘要，例如 "Line count validation failed. Expected 20, got 19"
    system_prompt TEXT,              -- 案发时使用的系统提示词
    glossary TEXT,                   -- 案发时使用的术语表
    user_prompt TEXT,                -- 发送给 LLM 的具体批次台词文本
    llm_response TEXT,               -- LLM 真实返回的脏数据（可能为空）
    llm_settings TEXT,               -- JSON，包含发起请求时的 {model, temperature}
    created_at TEXT NOT NULL,        -- 创建时间
    FOREIGN KEY(task_id) REFERENCES translate_task(id)
);
```

---

## 四、 API 接口设计

新增一个详情获取接口：

**GET** `/api/tasks/{task_id}/errors`
- **功能**：获取指定任务的所有诊断错误快照。
- **返回数据结构**：
  ```json
  {
    "code": 200,
    "message": "success",
    "data": [
      {
        "id": 1,
        "task_id": "xxx-xxx",
        "batch_idx": 15,
        "error_reason": "Line count validation failed...",
        "system_prompt": "You are a professional translator...",
        "glossary": "{\"Term\": \"Translation\"}",
        "user_prompt": "ID: 1 | Hello\nID: 2 | World",
        "llm_response": "你好世界 (合并了行号)",
        "llm_settings": {"model": "Qwen2.5", "temperature": 0.5},
        "created_at": "2026-05-23T22:30:00Z"
      }
    ]
  }
  ```

---

## 五、 WebUI 展现层设计

### 5.1 列表页 (保持现状)
* 历史 (History) 列表的渲染逻辑和接口不作任何改变。任务失败时，状态列依旧展示红色的 `Failed` 标签。
* 点击进入抽屉 (Drawer) 详情后，在顶部或醒目位置保留原有的红条 `Error Alert`。

### 5.2 诊断弹窗交互
1. 在抽屉详情的红色 `Error Alert` 上绑定鼠标手型（`cursor: pointer`）和点击事件。
2. 用户点击后，触发 `/api/tasks/{task_id}/errors` 网络请求。
3. 弹出名为 **"LLM Diagnostics (模型诊断)"** 的模态框 (`<n-modal>`)。

### 5.3 弹窗内部排版 (一对多)
由于返回的是错误数组，弹窗内容使用 **折叠面板 (`<n-collapse>`)** 承载：
* **面板标题**：`Batch #{batch_idx} - {error_reason}`
* **内部结构**：
  * **MetaData 栏**：使用 `<n-tag>` 展示使用的 Model 名字、Temperature。
  * **Context 区域**：
    * 包含可折叠的 `System Prompt` 和 `Glossary` 模块（默认折叠，避免视觉干扰）。
  * **核心对比区域**：
    * 采用上下排列（或两栏左右均分）的方式。
    * 上方/左侧显示 **User Prompt**（发出去的内容）。
    * 下方/右侧显示 **LLM Response**（返回的脏数据）。
    * 文本展示需使用等宽字体或 `<n-code>` 组件，确保换行符和行号清晰对齐，方便用户一眼看出大模型在哪一行出现了吞字或幻觉。
