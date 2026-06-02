# AudioCleaner

AudioCleaner is a local Go service that converts incompatible audio tracks to AAC for Infuse free playback. It copies video, preserves audio order, validates output, replaces in place, and keeps short-term backups.

The service creates `/app/config/config.json` on first start when it does not already exist. The default media roots are `/media/movie` and `/media/tv`.

Because the container runs as `${PUID}:${PGID}`, make bind-mounted directories writable by that user before first start:

```bash
sudo chown -R ${PUID}:${PGID} config data logs backups test
```

Run from this directory:

```bash
docker compose -f AudioCleaner.yml --env-file ../global.env --env-file AudioCleaner.env up -d --build
```
