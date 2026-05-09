# SubtitleTranslator 配置更新 — 设计文档

> 版本: 3.0
> 日期: 2026-05-09
> 状态: 设计完成，待实施
> 迭代优先级: **第一优先级** (WebUI 迭代的前置)

---

## 一、核心原则

**配置唯一来源: config.json**（`app_port` 除外，仅通过环境变量 `APP_PORT` 配置）。移除 `ENV_OVERRIDES` 机制，所有配置变更统一通过 API 写入 `config.json`。

**更新策略: 全量替换 + 总是重启**。

- `PUT /api/config` 接收完整 config.json，原子写入后返回 200，由 BackgroundTasks 延迟执行 `os.execv()` 重启进程
- 重启后所有模块从 config.json 重新加载，天然一致，零不一致窗口
- 唯一例外: `PUT /api/prompts` 修改 prompt 文件后**即时生效，无需重启**（prompt_loader 无缓存，每次翻译从磁盘读取）

**为什么选择"总是重启"**：

- 配置变更频率极低（月级 0-2 次），重启代价仅 3-5 秒
- 避免了热重载带来的跨模块一致性窗口问题（同一字段在不同模块中更新时机不同）
- 无需 translator 配置快照、rate_limiter 参数化、DebounceMap setter、APScheduler reschedule 等复杂改造
- 实现极简，验收项少，维护成本低

---

## 二、更新规则

| 操作 | 写入目标 | 生效方式 | 原因 |
|------|---------|---------|------|
| `PUT /api/config` | config.json | **总是重启** | config.json 被所有模块依赖，重启后天然一致 |
| `PUT /api/prompts` | system_prompt.txt / glossary.json | **即时生效** | prompt_loader 无缓存，每个翻译任务从磁盘读取最新内容 |

---

## 三、配置文件原子写入

config.json 作为**唯一配置来源**，写入必须使用原子操作，防止断电/磁盘满/进程中断时文件损坏：

- **禁止** `open("w") + json.dump()`——`open("w")` 会先将文件截断为 0 字节，若此时中断则文件为空
- **必须** 使用写临时文件 + `os.replace()` 模式：先在同目录创建临时文件 → 写入并 fsync → `os.replace()` 原子替换
- `os.replace()` 在同一文件系统上是内核级原子操作，保证文件要么是旧内容要么是新内容，不存在中间状态

```python
def atomic_write_json(path, data):
    dir_name = os.path.dirname(path)
    fd, tmp_path = tempfile.mkstemp(dir=dir_name, suffix=".tmp")
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as f:
            json.dump(data, f, indent=4, ensure_ascii=False)
            f.flush()
            os.fsync(f.fileno())
    except Exception:
        os.unlink(tmp_path)
        raise
    os.replace(tmp_path, path)
```

---

## 四、os.execv() 资源清理策略

`os.execv()` 直接替换进程镜像，**不执行 Python 层面的清理**（atexit、`__del__`、`finally`、lifespan shutdown 均不触发）。需在调用前做最小化清理：

| 清理项 | 目的 | 实现方式 |
|--------|------|---------|
| 活跃翻译任务 | 重启后自动恢复 | **不干预**——execv 等价于进程中断，重启后 `recover_startup_jobs()` 将 `processing` 状态的 translate_task 重置为 `queued`，重新从头翻译 |
| 日志文件 fd | 防止 fd 泄漏（execv 默认继承 fd，新进程无法引用也无法关闭） | execv 前显式 `handler.close()`；同时在 `setup_logging()` 中为 FileHandler 设置 `FD_CLOEXEC` 标志，作为启动时预防（防止意外 execv 路径遗漏 close） |
| DebounceMap / WorkerPool 队列 | 内存中的等待/排队任务会丢失 | 可接受——Bazarr 会重发通知，watchdog/定时扫描会重新发现文件 |

---

## 五、配置写入流程

```
PUT /api/config {完整配置}
│
├── 0. 获取写锁 (_config_lock)
│   └── 并发 PUT 请求排队，防止写入-重启序列交错
│
├── 1. 校验
│   ├── 结构校验 (JSON 对象 + 顶层键 + 子键完整 + 不含 app_port, 详见 8.8)
│   ├── 必填项检查 (llm.api_url, llm.api_key, llm.model 非空 → 否则 HTTP 422)
│   └── 值合规检查 (详见 8.8 校验矩阵)
│
├── 2. 原子写入
│   └── tempfile → json.dump → fsync → os.replace()
│
├── 3. 返回 HTTP 200 {status: "restarting"}
│
└── 4. BackgroundTasks 延迟执行 execv
    ├── 日志 handler.close()
    └── os.execv()  ← 总是重启
```

> `os.execv()` 在 BackgroundTasks 中延迟执行，HTTP 200 响应已先返回客户端。调用方可根据响应 `{status: "restarting"}` 确认写入成功，等待 3-5 秒后重连。

---

## 六、对现有代码的影响

| 模块 | 改动 | 说明 |
|------|------|------|
| `config_loader.py` | **中改** | 移除 `ENV_OVERRIDES` 机制；移除 `sys.exit(1)` (LLM 必填项缺失时日志报 error，服务继续运行但翻译不执行)；从 DEFAULTS 中移除 `app_port`；新增 `atomic_write_json()` 原子写入；`load_config()` 简化为仅读取 config.json + 合并默认值 |
| `api.py` | **小改** | 新增 `GET/PUT /api/config`、`GET/PUT /api/prompts` 端点；`PUT /api/config` 内含编排逻辑 (获取写锁 → 校验 → 原子写入 → 返回 200 → BackgroundTasks 延迟 execv)；并发写入保护 `threading.Lock` |
| `main.py` | **小改** | `setup_logging()` 中为 FileHandler 设置 `FD_CLOEXEC` (防止 execv 后 fd 泄漏)；`app_port` 改为从环境变量 `APP_PORT` 读取 (不再经过 config.json) |

> translator.py、rate_limiter.py、debounce_queue.py **无需改动**——重启后从 config.json 重新加载，自然一致。
>
> funnel.py、scheduler.py、watchdog_monitor.py **无需改动**——funnel.py 和 scheduler.py 在运行时调用 `load_config()`（有缓存，返回启动时快照），watchdog_monitor.py 通过构造函数参数注入配置。重启后均从 config.json 重新加载，自然一致。

---

## 七、实施任务

### Step 1: config_loader + app_port 改造

| # | 任务 | 涉及文件 | 依赖 |
|---|------|---------|------|
| 1.1 | 移除 `ENV_OVERRIDES` 机制；移除 `sys.exit(1)` (必填项缺失时允许运行但翻译报错)；从 DEFAULTS 中移除 `app_port` | `config_loader.py` | 无 |
| 1.2 | 新增 `atomic_write_json()` 原子写入 | `config_loader.py` | 1.1 |
| 1.3 | `setup_logging()` 中为 FileHandler 设置 `FD_CLOEXEC` | `main.py` | 无 |
| 1.4 | `app_port` 改为从环境变量 `APP_PORT` 读取 (默认 9800)，不再经过 config.json | `main.py` | 1.1 |

### Step 2: API 端点

| # | 任务 | 涉及文件 | 依赖 |
|---|------|---------|------|
| 2.1 | `GET /api/config`: 返回 config.json 完整内容 (api_key 脱敏: 保留前4+后4位，≤8位全部脱敏) | `api.py` | 无 |
| 2.2 | `PUT /api/config`: 获取写锁 → 接收完整配置 → 校验 → 原子写入 → 返回 200 {status: restarting} → BackgroundTasks 延迟 execv (不干预活跃任务，重启后自然恢复)；并发写入保护 `_config_lock = threading.Lock()` | `api.py` | 2.1, 1.2 |
| 2.3 | `GET /api/prompts`: 返回 `{system_prompt: string, glossary: object}` | `api.py` | 无 |
| 2.4 | `PUT /api/prompts`: 接收 `{system_prompt?: string, glossary?: object}`，可只更新其中一个（未提供的保持不变）；glossary 必须是合法 JSON 对象；原子写入对应文件 → 即时生效 | `api.py` | 2.3 |

> 本迭代的 API 端点不使用统一响应格式 `{code, message, data}`，直接返回 JSON 数据或 HTTPException。统一响应格式将在 WebUI 迭代中统一实施。

### Step 3: ENV 清理

| # | 任务 | 涉及文件 | 依赖 |
|---|------|---------|------|
| 3.1 | 移除 `SubtitleTranslator.yml` 中 LLM_*/管道配置的 environment 传参（保留 APP_PORT、PUID、PGID、TZ） | `SubtitleTranslator.yml` | 1.1 |
| 3.2 | 精简 `SubtitleTranslator.env` 仅保留 PUID/PGID/TZ/ENV_HOST_PORT/ENV_APP_PORT 变量 | `SubtitleTranslator.env` | 3.1 |

### 验收矩阵

| # | 验收项 | 方式 | 正常路径 | 边界条件 | 异常路径 |
|---|--------|------|---------|---------|---------|
| V1.1 | 移除 ENV_OVERRIDES | Agent验收/Pytest | 环境变量不影响配置值；config.json 值优先，环境变量被忽略 | — | — |
| V1.2 | 移除 sys.exit(1) | Agent验收/Pytest | 启动时 api_key 为空 → 日志报 error，服务继续运行（与 PUT 校验不矛盾：启动时容忍空值，API 写入时拒绝空值） | api_key 为空时翻译任务 → LLM 调用失败 → 重试后标记 failed | — |
| V1.3 | 原子写入 | Agent验收/Pytest | 全量写入后 `load_config()` 返回新值 | 磁盘满 / 进程中断 → 临时文件残留，原文件不损坏（下次写入时清理残留 .tmp） | 写入非法 JSON 不损坏原文件；写入只读文件 → HTTP 500 |
| V1.4 | execv 清理 | 自动化端到端测试 | PUT /api/config → 返回 200 → 进程重启 → `recover_startup_jobs()` 将 processing 的 translate_task 重置为 queued（重新从头翻译）；funneling/extracting/rebuilding 状态的 subtitle_job 标记为 failed 并清理中间文件；translating 状态的 subtitle_job 保持不变（其关联的 translate_task reset 为 queued 后，翻译完成时 consumer_loop 会正常更新 job 状态）；重启后日志 fd 无泄漏 | — | execv 失败 → 日志 CRITICAL 告警，进程继续运行（配置不一致，建议手动重启容器） |
| V1.5 | app_port 独立于 config | Agent验收/Pytest | main.py 从环境变量读取端口，不经过 config.json；config.json 中无 app_port 字段 | 环境变量未设置 → 使用默认值 9800；旧 config.json 残留 app_port → load_config() 忽略该字段，GET 不返回，PUT 提交含有 app_port 会被自动忽略 | — |
| V2.1 | GET /api/config | 自动化端到端测试 | 返回完整配置 (不含 app_port)；api_key 脱敏 | config.json 不存在 → 自动生成默认配置并返回；api_key 长度 ≤ 8 → 全部 `••••`；长度 > 8 → 前4+`••••`+后4；空字符串 → 返回 `""` | — |
| V2.2 | PUT /api/config | 自动化端到端测试 | 写入成功 → 返回 200 {status: restarting} → 3-5 秒后进程重启，GET 返回新值 | 空请求体 → HTTP 422；api_key 为空 → HTTP 422；并发 PUT → 第二个请求 HTTP 409；写入值与当前完全相同 → 仍执行 execv 重启；请求体缺少子键 (如 rpm_limit) → HTTP 422 | 非法值 → HTTP 422 (Pydantic拦截)；文件写入失败 → HTTP 500 (不重启)；含 app_port 等多余字段 → 自动忽略；Content-Type 非 JSON → HTTP 422 |
| V2.3 | GET/PUT /api/prompts | 自动化端到端测试 | GET 返回 `{system_prompt: string, glossary: object}`；PUT 写入后下次翻译用新 prompt | 文件不存在 → GET 返回 `{system_prompt: "", glossary: {}}`；PUT 只提供 system_prompt 不含 glossary → 仅更新 system_prompt，glossary 保持不变；反之亦然 | PUT 空请求体 → HTTP 422；PUT glossary 不是 JSON 对象 (如字符串、数组) → HTTP 422 |
| V3.1 | ENV 清理 | 自动化端到端测试 | 移除 LLM_*/管道 ENV 后容器正常启动；配置管理功能正常；APP_PORT/PUID/PGID/TZ 保留 | 旧 config.json 残留 app_port → 不影响行为 (被忽略)；旧 env 文件残留 → 不影响行为 (config.json 为唯一来源) | — |

---

## 八、实施注意事项

### 8.1 execv 时序与失败处理

`os.execv()` 在 BackgroundTasks 中执行，HTTP 200 响应已先返回。execv 替换进程期间（约 1-2 秒）FastAPI 不可用，调用方收到 200 后应等待 3-5 秒再重连。

若 execv 失败（HTTP 200 已返回），此时 config.json 已写入新值但内存仍是旧值，进程不一致。处理策略：记录日志 error，尝试再次 execv（最多 3 次），若仍失败则日志记录 CRITICAL 告警，建议手动重启容器。

### 8.2 DebounceMap / WorkerPool 队列丢失

重启后内存中的等待/排队任务会丢失。这是可接受的——Bazarr 会重发通知，watchdog/定时扫描会重新发现文件。重启完成后管道会自然恢复。

### 8.3 Prompt 文件原子写入

`PUT /api/prompts` 写入 system_prompt.txt / glossary.json 时也应使用原子写入（tempfile + fsync + os.replace），防止写入中途断电导致文件损坏。

### 8.4 api_key 脱敏边界

- 长度 ≤ 8：全部脱敏为 `••••`
- 长度 > 8：保留前 4 位 + `••••` + 后 4 位
- 空字符串：返回 `""`（不脱敏，因为无内容可泄露）

### 8.5 ENV 清理与迁移

移除 `ENV_OVERRIDES` 后，需同步清理：
- `SubtitleTranslator.yml` 中 LLM_*/管道配置的 environment 传参（保留 APP_PORT、PUID、PGID、TZ）
- `SubtitleTranslator.env` 仅保留 PUID/PGID/TZ/ENV_HOST_PORT/ENV_APP_PORT 变量
- 旧 env 文件残留不影响行为 (config.json 为唯一来源)

> **迁移提醒**: 现有用户可能通过环境变量配置了 LLM 参数（如 `LLM_API_KEY`、`LLM_MODEL`）。升级前需确认 config.json 已包含所有必要配置，升级后环境变量覆盖不再生效。

### 8.6 并发写入保护

`PUT /api/config` 是"写入 → 重启"的复合操作，两个并发请求可能导致写入-重启序列交错（A 写入 → B 写入覆盖 A → A 的重启丢失 B 的写入）。使用 `threading.Lock` 保证串行化：

```python
_config_lock = threading.Lock()

@router.put("/api/config")
async def update_config(config: dict, background_tasks: BackgroundTasks):
    if not _config_lock.acquire(blocking=False):
        raise HTTPException(status_code=409, detail="Another config update is in progress")
    try:
        # 校验 → 原子写入
        ...
        background_tasks.add_task(_do_execv_restart)
        return {"status": "restarting", "message": "配置已写入，进程即将重启"}
    except Exception:
        _config_lock.release()
        raise
```

> 注意：`os.execv()` 成功后锁不会释放（进程被替换），但新进程会重新初始化。若 execv 失败，需在 `_do_execv_restart` 的异常处理中释放锁。

```python
def _do_execv_restart():
    """BackgroundTasks 中延迟执行的 execv 重启，含重试和锁释放"""
    # 强制网络栈缓冲，给前端留出收取 HTTP 200 的时间
    time.sleep(1.0)

    max_attempts = 3
    for attempt in range(max_attempts):
        try:
            for handler in logging.getLogger().handlers:
                if isinstance(handler, logging.FileHandler):
                    handler.close()
            os.execv(sys.executable, [sys.executable] + sys.argv)
        except Exception as e:
            logger.error(f"execv attempt {attempt+1}/{max_attempts} failed: {e}")
            time.sleep(1)
    logger.critical(
        "All execv attempts failed! Config on disk differs from in-memory. "
        "Manual container restart recommended."
    )
    _config_lock.release()
```

### 8.7 app_port 独立于 config.json

`app_port` 从 config.json 中移除，仅通过环境变量 `APP_PORT` 配置（默认 9800）。原因：端口是基础设施层关注点，Docker 端口映射（宿主机:容器）必须与容器内监听端口同步，这天然属于环境变量管辖范围。通过 `PUT /api/config` 修改 app_port 会导致容器内端口与 Docker 映射不一致，服务不可达。

`main.py` 中端口读取方式：
```python
port = int(os.environ.get("APP_PORT", 9800))
uvicorn.run("main:app", host="0.0.0.0", port=port, reload=False)
```

### 8.8 PUT /api/config 基于 Pydantic 的自动校验

放弃手工进行逐层校验，直接使用 FastAPI 原生的 Pydantic `BaseModel` 来完成所有校验工作。这不仅能减少样板代码，还能自动生成完善的 OpenAPI 文档。

**核心设计**：
1. **模型定义**：定义 `ConfigPayload` 及其嵌套的 `LlmConfig`, `PipelineConfig` 等子模型。
2. **忽略未定义字段**：在模型上设置 `model_config = ConfigDict(extra='ignore')`。当用户在 `PUT /api/config` 提交包含 `app_port` 或其他未定义字段时，Pydantic 会自动过滤掉这些字段，而不抛出 422 错误。这样既保证了安全，又极具宽容度。
3. **必填与默认值**：利用 Pydantic 的 Field 设置，例如 `api_url: str = Field(...)` 来强制必填项；类型转换（如数字类型检查）也会自动完成（抛出 HTTP 422）。
4. **值合规检查**：使用 `@field_validator` 或 `Field(ge=0)` 来处理类似 `rpm_limit >= 0` 的边界校验。

> **未知字段处理**：请求体中包含 DEFAULTS 未定义的字段（包括 `app_port`）将被 Pydantic 自动丢弃，不会写入 `config.json`。
