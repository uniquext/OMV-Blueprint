# AudioCleaner

AudioCleaner 是一个本地 Go 服务，用于将不兼容的音轨转换为 AAC，以便 Infuse 免费版播放。它会复制视频流、保留音轨原顺序、校验输出文件、原地替换媒体文件，并保留短期备份。

服务首次启动时，如果 `/app/config/config.json` 不存在，会自动创建该配置文件。容器内媒体根目录固定为 `/media`。默认扫描整个 `/media`；如果后续配置多个扫描路径，应理解为 `/media` 下的扫描白名单路径，而不是多个宿主机根目录。

容器会以 `${PUID}:${PGID}` 运行，因此首次启动前需要创建 bind mount 目录，并确保该用户可写：

```bash
mkdir -p config data logs backups work
sudo chown -R ${PUID}:${PGID} config data logs backups work /path/to/media
```

在当前目录执行：

```bash
docker compose -f AudioCleaner.yml --env-file ../global.env --env-file AudioCleaner.env build
docker compose -f AudioCleaner.yml --env-file ../global.env --env-file AudioCleaner.env up -d
curl -fsS http://localhost:9830/api/health
docker exec audiocleaner ffmpeg -version
docker exec audiocleaner ffprobe -version
```

正常 compose 运行时使用 `${RESOURCE}/Media:/media`。本地验证时应调整 `../global.env` 中的 `RESOURCE`，使 `${RESOURCE}/Media` 指向宿主机媒体目录；该目录在容器内始终挂载为 `/media`。

## WebUI

AudioCleaner V1.1 提供 Vue/Vite WebUI，由同一个 Go 服务提供静态文件。默认入口：

```bash
open http://localhost:9830/
```

Hash 路由：

- `/#/`：概览；Dashboard 不包含 Scan All。
- `/#/history`：历史记录。
- `/#/files`：文件列表、未解决备份还原和删除。
- `/#/settings`：Scan、Media、Pipeline、Audio、Validation、UI 六模块独立编辑、校验、确认和保存。
- `/#/logs`：最近日志和 SSE 状态。

模块更新使用 `If-Match` revision 和独立 `PATCH /api/config/{module}`。Audio、Validation、UI 热更新；Media 重载 Watcher；Pipeline 重载 Worker 池；Scan 保存后立即强制重启服务。配置始终以完整 `config.json` 原子写入，应用或重启失败时回滚。

`ui.language` 支持 `zh-CN` 和 `en-US`，保存后在当前浏览器热生效。Notifications 和整份 Config PUT 已从 Settings 合同移除。

备份采用固定的 `safety_only` 策略，不提供保留期或模式配置。
