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

## 集成验证

在当前目录运行端到端集成测试：

```bash
bash test/scripts/run-integration.sh
```

测试矩阵的原始物料保留在 `${RESOURCE}/AudioCleanerTestMatrix/source`。如果原始物料不存在，脚本会先生成一份；每次集成测试开始前，脚本会把这份原始物料复制到 `${RESOURCE}/Media/AudioCleanerTestMatrix`，然后只扫描容器内的 `/media/AudioCleanerTestMatrix`。这样 AudioCleaner 处理的是实际 `/media` 挂载下的运行副本，原始矩阵不会被修改。

该测试脚本会清理本地 AudioCleaner 测试状态。它会停止本地 AudioCleaner compose 服务并重新构建，替换 `${RESOURCE}/Media/AudioCleanerTestMatrix`，清空 `data`、`logs`、`backups` 和 `work`，并写入一个测试用的 `config/config.json`。容器内部仍只知道 `/media`，不知道哪些文件是测试数据。

生成的报告会写入 `test/reports`：

- `latest-status.json` 保存最近一次 `/api/status` 响应。
- `latest-jobs.json` 保存最近一次 `/api/jobs` 响应。
- `latest-backups.json` 保存最近一次 `/api/backups` 响应。
- `source-probe-summary.jsonl` 和 `output-probe-summary.jsonl` 保存源媒体和处理后媒体的 ffprobe 摘要。
