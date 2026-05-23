# SubtitleTranslator — 产品需求文档 v2.0 (增量)

> 版本: 2.0
> 日期: 2026-05-22
> 基线: PRD v1.0 (2026-05-09)
> 状态: 已确认

---

## 一、变更总览

v2.0 在 v1.0 基础上新增三大能力：**配置 API + WebUI + SSE 实时推送**，并伴随数据模型重构和 API 规范化。

| 维度 | v1.0 | v2.0 |
|------|------|------|
| 配置来源 | config.json + 环境变量覆盖 (ENV_OVERRIDES) | **config.json 唯一来源**，移除 ENV_OVERRIDES |
| app_port | 在 config.json 中 | **仅通过环境变量 APP_PORT**，不在 config.json 中 |
| 配置写入 | 无 API | **PUT /api/config** 原子写入 + 自动重启 |
| Prompt 编辑 | 仅文件系统 | **PUT /api/prompts** 即时生效 |
| 必填项缺失 | sys.exit(1) 退出 | **日志 error，服务继续运行** |
| Job 终态 | done / failed | done / failed / **skipped** |
| funnel_level | -1 为 NULL | **统一为整数 -1** |
| 时间戳 | 无 completed_at / started_at | **completed_at 终态自动设置，started_at 取出时设置** |
| SQLite 模式 | 默认 DELETE journal | **WAL 模式** |
| API 前缀 | /notify, /scan, /health | **/api/notify, /api/scan, /api/health** |
| API 响应格式 | 各端点自定义 | **统一 {code, message, data}** |
| 扫描接口 | POST /scan 需要 dir_path | **POST /api/scan 空请求体，全盘扫描** |
| 扫描节流 | threading.Lock | **threading.Event** |
| WebUI | 无 | **Vue 3 + Naive UI，3 页面** |
| SSE 实时推送 | 无 | **EventBus + SSE + snapshot_id** |
| 任务取消/重试 | 无 | **POST /api/jobs/{id}/cancel, /retry** |
| 目录结构 | 扁平 (api.py, db.py, config_loader.py) | **分层 (api/, core/, pipeline/)** |
| Dockerfile | 单阶段 | **多阶段 (node 编译 + python 运行)** |
| 环境变量 | 20+ 个 (LLM_*/管道配置) | **5 个 (仅 PUID/PGID/TZ/端口)** |

---

## 二、新增功能需求

| # | 需求 | 说明 |
|---|------|------|
| F15 | WebUI 仪表盘 | 实时统计卡片 + 活跃任务列表（L1/L2/L3 四段），SSE 增量推送 |
| F16 | WebUI 历史 | 任务分页列表 + 筛选 + 详情展开 + 日志查看器 |
| F17 | WebUI 配置 | config.json 表单编辑 + Prompt 在线编辑 + 重启遮罩 |
| F18 | SSE 实时推送 | EventBus 跨线程桥接，snapshot_id 一致性校验，30s 心跳保活 |
| F19 | API 规范化 | 统一响应格式 `{code, message, data}`，所有端点前缀 `/api/` |
| F20 | 任务取消与重试 | 取消进行中任务、重试失败任务 |

---

## 三、配置 API

### 3.1 配置唯一来源

config.json 是所有运行时配置的唯一来源。环境变量仅用于基础设施层参数（`APP_PORT`、`PUID`、`PGID`、`TZ`），不再覆盖任何业务配置项。

`app_port` 不属于 config.json，仅通过环境变量 `APP_PORT` 配置（默认 9800）。原因：端口是基础设施层关注点，Docker 端口映射必须与容器内监听端口同步，通过 API 修改 app_port 会导致容器内端口与 Docker 映射不一致。

### 3.2 配置读取

`GET /api/config` 返回 config.json 完整内容（不含 app_port）。api_key 返回时必须脱敏：

| api_key 长度 | 返回值 |
|-------------|--------|
| 空字符串 | `""` |
| ≤ 8 | `••••` |
| > 8 | 前4位 + `••••` + 后4位 |

### 3.3 配置写入

`PUT /api/config` 接收完整 config.json，执行全量替换。写入成功后通过 `os.execv()` 重启进程使配置生效。

**写入流程**：获取写锁 → Pydantic 校验 → 原子写入 → 返回 200 → BackgroundTasks 延迟 execv

**校验规则**：
- 使用 Pydantic `BaseModel`（`ConfigPayload` 及嵌套子模型），`extra='ignore'` 自动过滤未定义字段（如 app_port）
- `api_url`、`api_key`、`model` 为必填项，缺失返回 422
- 数值范围约束通过 `@field_validator` / `Field(ge=0)` 实现

**原子写入**：必须使用 tempfile + fsync + os.replace 模式，禁止 `open("w") + json.dump()`，防止断电/进程中断时文件损坏。

**并发保护**：`threading.Lock` 串行化，并发 PUT 返回 409。

**必填项容错**：启动时 api_url/api_key/model 为空时，日志记录 error，服务继续运行但翻译任务执行时将失败。与 API 写入时的强制校验不矛盾——启动时容忍空值，API 写入时拒绝空值。

### 3.4 Prompt 编辑

`PUT /api/prompts` 修改 system_prompt.txt / glossary.json 后即时生效，无需重启（prompt_loader 无缓存，每次翻译从磁盘读取）。

**请求体**：`{system_prompt?: string, glossary?: object}`，可只更新其中一个，未提供的保持不变。glossary 必须是合法 JSON 对象。

写入时使用原子写入（tempfile + fsync + os.replace），防止写入中途断电导致文件损坏。

### 3.5 进程重启 (os.execv)

`PUT /api/config` 写入成功后，由 BackgroundTasks 延迟执行 `os.execv()` 重启进程。HTTP 200 响应先返回客户端，调用方等待 3-5 秒后重连。

**重启前清理**：

| 清理项 | 处理方式 |
|--------|---------|
| WorkerPool | 调用 `worker_pool.shutdown(wait=True, timeout=5)`，等待当前事务完成 |
| 日志文件 fd | 显式 `handler.close()`；`setup_logging()` 中为 FileHandler 设置 `FD_CLOEXEC` 标志 |
| 活跃翻译任务 | 不干预，重启后 `recover_startup_jobs()` 自动恢复 |
| DebounceMap / WorkerPool 队列 | 内存队列丢失可接受，Bazarr/watchdog/定时扫描会重新覆盖 |

**execv 失败处理**：重试最多 3 次（间隔 1 秒），仍失败则记录 CRITICAL 日志告警并释放写锁，建议手动重启容器。

### 3.6 API 规范化

- **路由前缀**：所有端点统一使用 `/api/` 前缀（`/api/notify`、`/api/scan`、`/api/health`），旧路径返回 404
- **统一响应格式**：所有 API（除 SSE）返回 `{code, message, data}`
- **scan 改造**：`POST /api/scan` 移除 `dir_path` 请求体，改为空请求体全盘扫描（使用配置的 `scan_dir`）
- **扫描节流**：使用 `threading.Event` 替代 `threading.Lock`，`locked()` 跨线程不可靠

### 3.7 环境变量

仅保留基础设施层变量：

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `ENV_HOST_PORT` | 9800 | 宿主机端口映射 |
| `ENV_APP_PORT` | 9800 | 容器内端口 |
| `PUID` | 1000 | 用户 ID |
| `PGID` | 1000 | 组 ID |
| `TZ` | Asia/Shanghai | 时区 |

---

## 四、WebUI

### 4.1 技术架构

| 维度 | 选型 | 理由 |
|------|------|------|
| 前端框架 | Vue 3 + Naive UI | 模板语法直觉友好，中文生态强 |
| 构建工具 | Vite | 编译 .vue → 纯 HTML/CSS/JS，开发热更新 |
| 实时通信 | SSE | 单向推送，协议极简，浏览器自动重连 |
| 部署 | 嵌入 FastAPI StaticFiles | 单容器，单端口 :9800 |
| 认证 | 暂不需要 | 内网直接访问 |

**生产部署**：Dockerfile 多阶段构建，Stage 1 (`node:20-alpine`) 编译 Vue → `dist/`，Stage 2 (`python:3.9-slim`) 拷贝产物 + 运行 FastAPI。SPA 兜底路由：非 `/api/` 前缀的 GET 请求返回 `index.html`。

**开发模式**：前端 Vite dev server (:5173) + 后端 FastAPI (:9800)，`CORSMiddleware` 允许跨域。

### 4.2 仪表盘

**统计卡片**（SSE 实时刷新）：等待数 / 排队数 / 翻译中 / 完成 / 跳过 / 失败 / 成功率 / 端到端耗时 / 纯翻译耗时 / 扫描状态

**全盘扫描按钮**：调用 `POST /api/scan`，扫描进行中时禁用（依据 `GET /api/stats` 的 `scanning` 字段）

**活跃任务列表**（SSE 实时刷新 / 增量更新）：

| 阶段 | 状态 | 数据来源 |
|------|------|---------|
| L1 | ⏳ 等待中 | `GET /api/queue/debouncing` ← DebounceMap 内存快照 |
| L2 | 📋 排队中 | `GET /api/queue/pending` ← FIFO Queue + Worker Pool 内存快照 |
| L2 | 🔍 漏斗/提取 | `GET /api/jobs?status=funneling&status=extracting` ← SQLite |
| L3 | 🌐 翻译/回写 | `GET /api/jobs?status=translating&status=rebuilding` ← SQLite |

### 4.3 历史

- 分页列表 + 筛选（状态 / 漏斗级别 / 日期范围）
- 任务详情（点击展开：media_path, funnel_level, original_srt, output_srt, 耗时, 错误信息）
- 统计汇总：`成功率 = done / (done + failed)`，`skipped` 单独展示不计入分母
- 日志查看器（`GET /api/logs`，终端风格展示）
- 操作按钮：进行中任务 [取消]，失败任务 [重试]

### 4.4 配置

- **config.json 编辑**：表单化展示，保存时调用 `PUT /api/config`，触发重启遮罩
- **Prompt 编辑**：system_prompt.txt + glossary.json 在线编辑，保存时调用 `PUT /api/prompts`，即时生效
- **重启遮罩**：保存配置后弹出"系统正在重启"遮罩，SSE 重连成功后自动撤销（SSE 实现前使用定时轮询 `/api/health` 检测重启完成）

### 4.5 数据模型

WebUI 的统计卡片、历史页面、耗时展示依赖以下数据模型：

**SubtitleJob**：

```sql
CREATE TABLE IF NOT EXISTS subtitle_job (
    id TEXT PRIMARY KEY,
    media_path TEXT NOT NULL,
    status TEXT NOT NULL,           -- funneling / extracting / translating / rebuilding / done / failed / skipped
    funnel_level INTEGER,           -- 0 / 1 / 2 / 3 / -1（统一为整数，-1 不再为 NULL）
    original_srt_path TEXT,
    output_srt_path TEXT,
    cleanup_files TEXT,
    translate_task_id TEXT,
    source TEXT DEFAULT 'scheduler', -- scheduler / notify / scan / startup
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    error TEXT,
    completed_at TEXT               -- 终态时自动设置
);
```

**TranslateTask**：

```sql
CREATE TABLE IF NOT EXISTS translate_task (
    id TEXT PRIMARY KEY,
    file_path TEXT NOT NULL,
    status TEXT NOT NULL,           -- queued / processing / done / failed
    progress TEXT,
    current_batch INTEGER DEFAULT 0,
    total_batches INTEGER DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    error TEXT,
    completed_at TEXT,              -- 终态时自动设置
    started_at TEXT                 -- fetch_next_task() 时自动设置
);
```

**关键规则**：

- `skipped` 终态：Level 0（已含中文字幕）和 Level -1（无法提取字幕）的 Job 标记为 `skipped`，不计入成功率分母
- `completed_at`：`update_job_status()` 检测终态 `TERMINAL_STATES = {"done", "failed", "skipped"}` 时自动设置
- `started_at`：`fetch_next_task()` 取出任务时自动设置（仅 translate_task）
- `source`：追踪任务来源，用于历史页面筛选
- `funnel_level`：统一为整数（-1 不再为 NULL），简化筛选查询
- 去重联动：`db.create_job()` 去重查询扩展为 `NOT IN ('done', 'failed', 'skipped')`
- WAL 模式：`init_db()` 中启用 `PRAGMA journal_mode=WAL`，提升 WebUI 查询性能

### 4.6 WebUI 相关 API

| 方法 | 路径 | 功能 |
|------|------|------|
| GET | /api/stats | 实时统计（含 snapshot_id） |
| GET | /api/queue/debouncing | DebounceMap 快照 |
| GET | /api/queue/pending | WorkerPool 快照 |
| GET | /api/jobs | Job 分页查询（status / funnel_level / 日期范围） |
| GET | /api/tasks | Task 分页查询 |
| GET | /api/jobs/stats | 终态 Job 统计（done / skipped / failed 计数 + 成功率） |
| GET | /api/logs | 日志文件尾部读取（查询参数 `lines`，默认 200） |
| POST | /api/jobs/{job_id}/cancel | 取消任务 |
| POST | /api/jobs/{job_id}/retry | 重试任务 |

**GET /api/stats 响应**：

```json
{
  "debouncing": 0, "queued": 0, "active_workers": 0,
  "funneling": 0, "extracting": 0, "translating": 0, "rebuilding": 0,
  "done": 0, "skipped": 0, "failed": 0,
  "success_rate": 0.0,
  "avg_duration_seconds": 0,
  "avg_translate_seconds": 0,
  "scanning": false,
  "snapshot_id": 42
}
```

**任务取消**：`funneling`/`extracting`/`rebuilding` → 标记 `failed` + 清理中间文件；`translating` → 关联 translate_task 也标记 `failed` + 清理中间文件；`debouncing`/`queued` → 从内存队列移除

**任务重试**：清理旧产物（output_srt、cleanup_files 中列出的文件）→ 重置 Job 状态为 `funneling`，清空 error / funnel_level 等字段 → 重新提交至 WorkerPool

### 4.7 目录结构

```
app/
├── api/           router.py, response.py, sse.py
├── core/          config_loader.py, config_models.py, db.py
├── pipeline/      debounce_queue.py, funnel.py, consumer.py
├── scanner/       media_scanner.py, watchdog_monitor.py, scheduler.py
├── subtitle/      lang_utils.py, srt_handler.py, opencc_handler.py, ffmpeg_handler.py
├── translate/     translator.py, prompt_loader.py, rate_limiter.py, eta_tracker.py
├── constants.py
├── dist/          (Vue 构建产物)
└── web/           (Vue 3 前端源码，开发时使用)
```

---

## 五、SSE 实时推送

### 5.1 EventBus 事件总线

在 FastAPI `lifespan` 中初始化全局单例，基于 `asyncio.Queue` 实现发布/订阅。

**跨线程桥接**：FastAPI 运行在异步事件循环中，FunnelWorkerPool 和 consumer 运行在同步线程中。保存 `asyncio` 事件循环引用，同步线程通过 `asyncio.run_coroutine_threadsafe(event_bus.publish(event), loop)` 安全抛投至主事件循环。

**统一埋点**：事件发布在 `db.update_job_status()` 和 `db.create_job()` 内部完成，业务层（funnel.py、consumer.py）无需显式调用 EventBus，避免分散埋点遗漏。

### 5.2 SSE 端点

`GET /api/events` — SSE 服务端流，实时推送状态变更事件。

**事件类型**：

```json
// job_created
{"event": "job_created", "data": {"job_id": "uuid", "media_path": "/media/...", "source": "bazarr", "snapshot_id": 42}}

// job_status_changed
{"event": "job_status_changed", "data": {"job_id": "uuid", "status": "translating", "media_path": "/media/...", "snapshot_id": 43}}

// task_progress
{"event": "task_progress", "data": {"task_id": "uuid", "current_batch": 3, "total_batches": 25, "snapshot_id": 44}}

// scan_completed
{"event": "scan_completed", "data": {"count": 15, "snapshot_id": 45}}
```

**心跳保活**：每 30 秒发送 `:ping\n\n` 注释帧，防止反向代理/负载均衡器因空闲超时断开连接。

**snapshot_id**：EventBus 维护的单调递增计数器，每次 `publish()` 自增。前端检测不连续时触发快照拉取。

### 5.3 前端 SSE 策略

1. **初始对齐**：页面加载时通过 `GET /api/stats` 和 `GET /api/queue/*` 获取一次基线数据
2. **增量维护**：收到 `job_status_changed` 时直接在本地修改统计数值，不发起 HTTP 请求
3. **宏观重置**：收到 `scan_completed` 等可能导致大量数据涌入的事件时，触发全量 HTTP 快照拉取
4. **一致性校验**：`snapshot_id` 不连续时（如 SSE 重连期间丢失事件），自动拉取 `/api/stats` 快照恢复状态
5. **指数退避重连**：SSE 断线后自动重连，重连成功后静默拉取快照

### 5.4 配置重启遮罩

`PUT /api/config` 触发 `os.execv()` 重启时，前端体验：

1. SSE 连接断开 → 弹出"系统正在应用配置并重启"全局遮罩
2. SSE 重连成功 → 自动撤销遮罩并拉取 `/api/stats` 快照恢复页面状态
3. 在 SSE 实现前，使用定时轮询 `/api/health` 检测重启完成
