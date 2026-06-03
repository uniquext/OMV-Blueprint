# AudioCleaner

AudioCleaner is a local Go service that converts incompatible audio tracks to AAC for Infuse free playback. It copies video, preserves audio order, validates output, replaces in place, and keeps short-term backups.

The service creates `/app/config/config.json` on first start when it does not already exist. The default media roots are `/media/movie` and `/media/tv`.

Because the container runs as `${PUID}:${PGID}`, make bind-mounted directories writable by that user before first start:

```bash
sudo chown -R ${PUID}:${PGID} config data logs backups test
```

Run from this directory:

```bash
AUDIOCLEANER_MEDIA_PATH=./test docker compose -f AudioCleaner.yml --env-file ../global.env --env-file AudioCleaner.env build
AUDIOCLEANER_MEDIA_PATH=./test docker compose -f AudioCleaner.yml --env-file ../global.env --env-file AudioCleaner.env up -d
curl -fsS http://localhost:9830/api/health
docker exec audiocleaner ffmpeg -version
docker exec audiocleaner ffprobe -version
```

The smoke commands mount `./test` as `/media` so they work on local Docker hosts
without OMV storage paths. Normal compose runs default to `${RESOURCE}/Media`;
set `AUDIOCLEANER_MEDIA_PATH` only when you need a different media root.

## Integration Verification

Run the end-to-end integration runner from this directory:

```bash
bash test/scripts/run-integration.sh
```

The runner is destructive for local AudioCleaner test state. It stops the local
AudioCleaner compose service, rebuilds it, replaces `test/generated`, clears
`data`, `logs`, and `backups`, and writes a test-only `config/config.json`
pointing at `test/generated/media`.

Generated reports are written under `test/reports`:

- `latest-status.json` stores the most recent `/api/status` response.
- `latest-jobs.json` stores the most recent `/api/jobs` response.
- `latest-backups.json` stores the most recent `/api/backups` response.
- `source-probe-summary.jsonl` and `output-probe-summary.jsonl` store ffprobe
  summaries for source and processed media.
