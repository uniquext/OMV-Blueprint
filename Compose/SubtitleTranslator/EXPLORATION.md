# SubtitleTranslator — 探索阶段成果文档

> 生成时间: 2026-04-24
> 状态: 探索阶段完成，所有决策已锁定

---

## 一、项目定位

### 当前阶段（Phase 1）：纯文本翻译管道

**核心目标**：跑通「输入文本 → 输出翻译文本」的整体功能链路。

```
输入: 纯文本文件 (.txt)           输出: 翻译后的纯文本文件（保留原始空行）
      ↓                                ↑
  按行读取 + 空行映射表            按 line_map 回填空行后写回
      ↓                                ↑
  自动编号格式化                  解析 AI 输出
  "ID: 1 | xxxxxxx"              "ID: 1 | xxxxxxx"
      ↓                                ↑
  ┌──────────────────────────────────────────┐
  │  批处理 + 上下文窗口 + 限速 + 断点续传     │
  │  (MT/Chat 模型分流)                       │
  └──────────────────────────────────────────┘
```

### 后续阶段（不在本次范围）

- SRT/ASS 等字幕格式解析与提取
- 翻译文本回写为字幕格式
- 全盘扫描的文件匹配规则（涉及字幕特定逻辑）
- Web UI 前端界面

---

## 二、功能需求汇总

| # | 需求 | 决策/说明 |
|---|------|-----------|
| 1 | 常驻微服务容器 | Docker Compose 部署，常驻运行 |
| 2 | HTTP API 监听 | 通过环境变量配置端口，接收翻译请求 |
| 3 | 队列机制 | 生产者-消费者模型，单消费者线程 |
| 4 | 定时全盘扫描 | 传入秒数间隔（支持一天/一周），未设置则无定时任务 |
| 5 | LLM API 配置 | 环境变量：API URL、模型名、API Key |
| 6 | 分批翻译 | 每次 15-20 句一批 |
| 7 | 上下文窗口 | 仅前文上下文（前5句）+ 20句翻译。Chat: 指令标注；MT: 平铺发送，代码裁剪 |
| 8 | API 限速 | 1000 RPM / 80000 TPM，阈值 75k TPM，自动 sleep |
| 9 | 日志系统 | RotatingFileHandler，10MB/文件，5 备份 |
| 10 | Prompt 热更新 | system_prompt.txt + glossary.json 通过 volume 挂载 |
| 11 | 全盘扫描 | 扫描 Compose 挂载的媒体根目录 |
| 12 | CLI 投递消息 | CLI 通过 HTTP POST 投递任务，不直接执行主程序 |
| 13 | 字幕逻辑暂不涉及 | 当前阶段为纯文本翻译管道 |
| 14 | 输入输出格式 | 输入纯文本 → 空行映射+自动编号 → `ID: n \| text` → 解析后按 line_map 回填空行 |
| 15 | 断点续传 | 每批写入 .tmp（格式 `ID: n \| translated_text`），完成后按 line_map 回填空行 → `原文件名.zh.txt` |
| 16 | 模型类型分流 | 环境变量 `LLM_MODEL_TYPE`（mt/chat），控制 Prompt 构造、术语表注入、上下文发送策略 |

---

## 三、技术选型决策

| 维度 | 决策 | 理由 |
|------|------|------|
| Web 框架 | **FastAPI** | 自动 OpenAPI 文档、WebSocket 支持（未来 Web UI 实时进度）、Pydantic 模型校验、CORS 支持 |
| 队列持久化 | **SQLite** | 单文件零配置、查询灵活、Python 内置、重启恢复、volume 挂载即可 |
| 消费者模型 | **单消费者线程** | 限速逻辑无竞争、实现简单、与全局 TPM 限速天然契合 |
| CLI 交互 | **HTTP POST (curl)** | CLI 只负责投递消息，异步返回 task_id |
| 基础镜像 | **python:3.12-slim** | 官方镜像稳定可靠，alpine 在 ARM/AMD 混合环境下 pip 编译易踩坑 |
| API Key | **单 Key，环境变量存储** | `LLM_API_KEY` 环境变量，不做多 Key 轮询 |
| 代理配置 | **不需要** | LLM API 直连，不走代理 |

### Web UI 兼容性

FastAPI 为后续 Web UI 天然预留了扩展空间：

- 自动生成 Swagger UI（开箱即用的 API 调试界面）
- WebSocket 支持（实时进度推送）
- StaticFiles 中间件（托管前端 SPA）
- CORS 中间件（跨域支持）

**唯一约束**：API 返回结构化 JSON（RESTful），当前设计已满足。

---

## 四、系统架构

```
┌─────────────────────────────────────────────────────────────────────────┐
│                    SubtitleTranslator 微服务容器                        │
│                                                                         │
│  ┌──────────────┐   ┌──────────────────┐   ┌──────────────────────┐   │
│  │  FastAPI      │   │   SQLite Queue   │   │   Consumer Thread    │   │
│  │  HTTP Server  │──▶│   (持久化)       │──▶│   (单线程消费)        │   │
│  │  :<port>      │   │                  │   │                      │   │
│  │              │   │  task表:          │   │  ┌────────────────┐  │   │
│  │  POST /translate│  │  - id            │   │  │ Rate Limiter   │  │   │
│  │  POST /scan   │   │  - file_path     │   │  │ (TPM限速拦截)  │  │   │
│  │  GET  /progress│  │  - status        │   │  └───────┬────────┘  │   │
│  │               │   │  - progress      │   │          │           │   │
│  └──────────────┘   │  - created_at    │   │          ▼           │   │
│                      └──────────────────┘   │  ┌────────────────┐  │   │
│  ┌──────────────┐                           │  │  LLM API       │  │   │
│  │  Scheduler   │─── 加入队列 ──────────────▶│  │  (HTTP)        │  │   │
│  │  (定时扫描)   │                           │  └────────────────┘  │   │
│  └──────────────┘                           └──────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  Volume 挂载 (热更新)                                           │   │
│  │  ├── /app/prompts/system_prompt.txt    (系统提示词)             │   │
│  │  ├── /app/prompts/glossary.json        (术语表)                 │   │
│  │  ├── /app/data/queue.db                (任务队列持久化)         │   │
│  │  └── /app/logs/                        (日志目录)               │   │
│  └─────────────────────────────────────────────────────────────────┘   │
│                                                                         │
│  ┌─────────────────────────────────────────────────────────────────┐   │
│  │  媒体目录挂载                                                   │   │
│  │  └── /media                      (翻译源文件 + 输出目录)        │   │
│  └─────────────────────────────────────────────────────────────────┘   │
└─────────────────────────────────────────────────────────────────────────┘
```

---

## 五、API 设计

### 5.1 提交翻译任务

```bash
curl -X POST http://<IP>:<PORT>/translate \
  -H "Content-Type: application/json" \
  -d '{"file_path": "/media/Anime/EP01.txt"}'
```

响应（异步，立即返回）：

```json
{
  "task_id": "uuid-string",
  "status": "queued",
  "message": "任务已加入队列"
}
```

### 5.2 全盘扫描

```bash
curl -X POST http://<IP>:<PORT>/scan \
  -H "Content-Type: application/json" \
  -d '{"dir_path": "/media/Anime"}'
```

扫描过滤规则（防止套娃陷阱）：
- **后缀过滤**：只扫描 `.txt` 文件
- **命名过滤**：排除文件名中包含 `.zh.` 或 `.tmp` 的文件
- **存在性过滤**：如果同目录下已存在 `原文件名.zh.txt`，则跳过该文件

响应：

```json
{
  "task_ids": ["uuid-1", "uuid-2", "..."],
  "count": 15,
  "message": "已扫描到 15 个待翻译文件，已加入队列"
}
```

### 5.3 进度查询

```bash
curl -X GET "http://<IP>:<PORT>/progress?file_path=/media/Anime/EP01.txt"
```

响应：

```json
{
  "task_id": "uuid-string",
  "file_path": "/media/Anime/EP01.txt",
  "status": "processing",
  "progress": "120/500 行",
  "percentage": 24.0,
  "current_batch": 6,
  "total_batches": 25,
  "eta_seconds": 180
}
```

`eta_seconds` 计算逻辑: `剩余行数 / BATCH_SIZE * 最近5次请求平均耗时(秒)`

### 5.4 队列状态

```bash
curl -X GET http://<IP>:<PORT>/queue
```

响应：

```json
{
  "pending": 3,
  "processing": 1,
  "done": 15,
  "failed": 0,
  "tasks": [
    {
      "task_id": "uuid-1",
      "file_path": "/media/Anime/EP01.txt",
      "status": "processing",
      "progress": "120/500 行"
    }
  ]
}
```

---

## 六、翻译批处理流程

### 6.1 文本预处理（含空行映射）

```
输入文件 (纯文本, 每行一句, 可能含空行)
──────────────────────────────────
Hello, how are you?
                              ← 原始空行
I'm fine, thank you.
What a beautiful day!
...
──────────────────────────────────

        ↓ Step 1: 构建空行映射表 (line_map)

line_map (原始行号 → 编号 ID 或 None):
──────────────────────────────────
line_map[0] = 1       # "Hello, how are you?"
line_map[1] = None    # (空行)
line_map[2] = 2       # "I'm fine, thank you."
line_map[3] = 3       # "What a beautiful day!"
...
──────────────────────────────────

        ↓ Step 2: 仅对非空行自动编号

格式化后 (无标签关键词, 统一所有模型):
──────────────────────────────────
ID: 1 | Hello, how are you?
ID: 2 | I'm fine, thank you.
ID: 3 | What a beautiful day!
...
──────────────────────────────────
```

### 6.2 分批 + 上下文窗口（模型分流）

根据 `LLM_MODEL_TYPE` 环境变量区分两种上下文策略：

#### 6.2.1 Chat 模型 (`LLM_MODEL_TYPE=chat`，默认)

保留语义化上下文指令，术语表正常注入，模型只翻译标注的内容：

```
Batch 1 (ID 1~20, 首批无前文上下文):
  → User Prompt:
    术语表：{...}

    需要翻译的内容：
    ID: 1 | Hello, how are you?
    ...
    ID: 20 | See you tomorrow.
  → API Call → 解析 → 追加写入 .tmp

Batch 2 (ID 21~40):
  → User Prompt:
    术语表：{...}

    前文上下文（仅供参考，不要翻译）：
    ID: 16 | ...
    ...
    ID: 20 | ...

    需要翻译的内容：
    ID: 21 | ...
    ...
    ID: 40 | ...
  → API Call → 解析 → 追加写入 .tmp
```

#### 6.2.2 MT 模型 (`LLM_MODEL_TYPE=mt`)

MT 模型为“文本进，译文出”的纯翻译逻辑，忽略复杂指令。不注入术语表，不使用指令词，上下文直接与翻译内容平铺发送：

```
Batch 1 (ID 1~20, 首批无前文上下文):
  → User Prompt (纯文本平铺):
    ID: 1 | Hello, how are you?
    ...
    ID: 20 | See you tomorrow.             
  → API Call → 追加写入 .tmp

Batch 2 (ID 21~40, 实际发送 25 行):
  → User Prompt (纯文本平铺):
    ID: 16 | ...                ← 前上下文 (5句)
    ...
    ID: 20 | ...
    ID: 21 | ...                ← 正式翻译内容 (20句)
    ...
    ID: 40 | ...             
  → API Call → 代码裁剪：仅保留 ID 21~40 → 追加写入 .tmp

Batch N (最后一批):
  → User Prompt: 前5句上下文 + 剩余行
  → API Call → 代码裁剪 → 追加写入 .tmp → 回填空行 → .zh.txt
```

### 6.3 LLM 输出解析与校验

```
API 返回结果解析流程:
  1. 使用宽容正则逐行匹配: /^ID:\s*(\d+)\s*\|\s*(.+)$/
     → 管道符后直接捕获全部内容，不再要求任何标签关键词
     → 同时兼容模型保留或翻译标签的各种情况
  2. ID 范围过滤:
     解析后仅保留 expected_ids 范围内的行
     ├── Chat 模型: 丢弃上下文行被翻译的噪声
     └── MT 模型: 裁剪前上下文的翻译结果
  3. 行数校验:
     发送 20 句 → 期望返回 20 行解析结果
     ├── 返回 20 行 → 正常写入 .tmp
     ├── 返回 < 20 行 → 该批次重试
     └── 返回 > 20 行 → 仅取 expected_ids 对应行，日志记录异常
  4. ID 连续性校验:
     检查解析出的 ID 是否连续递增
     ├── 连续 → 正常
     └── 不连续 → 该批次重试
```

### 6.4 断点续传 & 空行回填

```
启动任务时:
  ├── 检查 原文件名.zh.tmp 是否存在
  │   ├── 不存在 → 从第 1 行开始
  │   └── 存在 → 读取数据库中已完成的批次序号 → 从下一批次继续
  │
  └── 翻译全部完成
      └── 原文件名.zh.tmp → 按 line_map 回填空行 → 重命名为 原文件名.zh.txt

.tmp 文件内容: 保留 ID 编号，便于断点续传对齐
  ID: 1 | 你好
  ID: 2 | 我很好，谢谢
  ID: 3 | 多么美好的一天！
  ...

最终输出 (.zh.txt): 根据 line_map 回填空行后的纯译文
  你好
                              ← 空行回填 (line_map[1] = None)
  我很好，谢谢
  多么美好的一天！
  ...

写入策略（原子化）:
  - 不逐行追加，按批次（Batch）原子化写入
  - 每个批次翻译完成后，一次性将该批次全部译文行写入 .tmp
  - 同时在 SQLite 中记录已完成的批次序号
  - 断电恢复时：读取数据库中的批次序号 → 校验 .tmp 行数是否对齐 → 继续下一批次
  - 校验失败（行数不匹配）→ 截断 .tmp 到上一个完整批次，重新翻译当前批次
```

---

## 七、限速拦截器设计

```
┌─────────────────────────────────────────────────────────┐
│                Rate Limiter (单线程，无锁)                │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  状态:                                                  │
│    - minute_start: 当前分钟窗口起始时间                  │
│    - tokens_this_minute: 当前分钟已消耗的 token 总数     │
│    - requests_this_minute: 当前分钟已发送的请求数        │
│                                                         │
│  每次请求前:                                            │
│    1. 检查 RPM: requests_this_minute >= 900? sleep      │
│    2. 检查 TPM: tokens_this_minute >= 75000? sleep      │
│                                                         │
│  每次请求后:                                            │
│    1. 读取 response.usage.total_tokens                  │
│    2. tokens_this_minute += total_tokens                │
│    3. requests_this_minute += 1                         │
│    4. 判断是否跨分钟 → 重置计数器                       │
│    5. 如超阈值 → time.sleep(剩余秒数)                   │
│                                                         │
│  阈值:                                                  │
│    RPM: 900 (留 100 余量)                               │
│    TPM: 75000 (留 5000 余量)                            │
│                                                         │
│  目标: 绝对不抛出 HTTP 429 错误                         │
└─────────────────────────────────────────────────────────┘
```

### 重试策略

```
┌─────────────────────────────────────────────────────────┐
│              翻译失败重试 (指数退避)                       │
├─────────────────────────────────────────────────────────┤
│                                                         │
│  最大重试次数: 3                                         │
│  重试间隔: 5s → 10s → 20s (指数退避)                    │
│                                                         │
│  行为:                                                  │
│    1. API 调用失败 → 等待 5s 重试                        │
│    2. 仍失败 → 等待 10s 重试                             │
│    3. 仍失败 → 等待 20s 重试                             │
│    4. 3次仍失败 → 标记任务为 failed，写入日志             │
│    5. 继续消费队列中的下一个任务（不阻塞队列）            │
│                                                         │
│  日志记录: 每次重试记录 task_id, batch_id, 重试次数, 错误 │
└─────────────────────────────────────────────────────────┘
```

---

## 八、日志设计

```
日志文件: /app/logs/subtitle_translator.log
轮转策略: RotatingFileHandler
  - maxBytes: 10MB
  - backupCount: 5
  → subtitle_translator.log
  → subtitle_translator.log.1
  → subtitle_translator.log.2
  → ...
  → subtitle_translator.log.5

日志内容:
  - 每次 API 调用: task_id, batch_id, token_usage, 响应时间
  - 限速事件: sleep 时长, 当前 TPM 累计
  - 任务生命周期: 入队, 开始, 进度, 完成, 失败
  - 异常事件: API 错误, 文件读写错误, 重试
```

---

## 九、Prompt 热更新机制

```
宿主机                                    容器内
─────────────────────────────          ─────────────────────────
/srv/.../AppData/subtitle/             /app/prompts/ (volume映射)
├── system_prompt.txt  ─────────────▶  system_prompt.txt      (所有模型共用)
├── glossary.json      ─────────────▶  glossary.json          (仅 Chat 模型注入)
└── prompt_guide.md                        (参考文档，不挂载进容器)

读取策略:
  - 每次翻译任务开始前，从磁盘读取 system_prompt.txt
  - 不做内存缓存 → 修改即生效，无需重启容器
  - glossary.json 格式: {"term": "翻译", ...}
  - 切换模型类型时，用户需同步修改 system_prompt.txt

参考文档 (prompt_guide.md):
  - 说明 MT 模型与 Chat 模型对 Prompt 的不同响应特征
  - 提供两类模型的推荐 Prompt 模板，供用户切换时参考
```

---

## 十、环境变量清单

```bash
# 服务配置
APP_PORT=9800                          # HTTP 监听端口

# LLM API 配置
LLM_API_URL=https://api.example.com/v1/chat/completions
LLM_MODEL=gpt-4o-mini
LLM_MODEL_TYPE=chat                    # 模型类型: chat (通用对话) / mt (专用翻译)
LLM_API_KEY=sk-xxxxxxxxxxxxxxxx

# 定时扫描 (秒)，0 或不设置则无定时任务
SCAN_INTERVAL=86400                    # 默认每天扫描一次 (86400秒)
SCAN_DIR=/media                        # 全盘扫描的根目录

# 限速配置
TPM_LIMIT=75000                        # 每分钟 token 上限 (留余量)
RPM_LIMIT=900                          # 每分钟请求上限 (留余量)

# 批处理配置
BATCH_SIZE=20                          # 每批翻译行数
CONTEXT_SIZE=5                         # 上下文窗口行数

# 网络配置
LLM_TIMEOUT=120                        # API 请求超时(秒)，单次20+5句响应可能30s+
```

---

## 十一、Docker Compose 目录结构

```
Compose/SubtitleTranslator/
├── SubtitleTranslator.env             # 环境变量
├── SubtitleTranslator.yml             # Docker Compose 定义
├── Dockerfile                         # 容器构建定义 (python:3.12-slim)
├── app/                               # Python 应用代码
│   ├── main.py                        # 入口：FastAPI + Scheduler + Consumer
│   ├── api.py                         # HTTP API 路由
│   ├── consumer.py                    # 消费者线程 + 限速拦截器
│   ├── translator.py                  # 翻译核心逻辑 (批处理/上下文/断点续传)
│   ├── queue_db.py                    # SQLite 队列持久化
│   ├── scheduler.py                   # 定时扫描调度
│   ├── prompt_loader.py               # Prompt 热加载
│   └── requirements.txt               # Python 依赖
├── prompts/                           # Volume 挂载源 (宿主机)
│   ├── system_prompt.txt
│   └── glossary.json
└── logs/                              # Volume 挂载源 (宿主机)
```

---

## 十二、风险与未知

| 风险 | 影响 | 缓解措施 |
|------|------|----------|
| LLM API 不稳定/超时 | 翻译任务卡住 | 超时重试 + 任务状态标记 |
| 上下文窗口格式与模型理解偏差 | 翻译质量下降 | Prompt 调优 + 测试 |
| 单消费者吞吐量不足 | 队列积压 | 当前限速下已是最优，Phase 1 可接受 |
| 容器重启时 .tmp 文件状态不一致 | 断点续传异常 | 按批次原子化写入 + 数据库记录已完成批次序号，重启后校验 |
| 全盘扫描"套娃"陷阱 | 翻译产出文件被重复扫描入队，无限循环 | 后缀过滤(.txt) + 命名过滤(排除.zh./.tmp) + 存在性过滤(已有.zh.txt则跳过) |
| LLM 输出格式不严格 | 翻译行数对不上，时间轴错位 | 正则解析 + 行数校验(发20句回来不足则重试) |


