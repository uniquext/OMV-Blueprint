#!/bin/bash

# ==============================================================================
# 02-indexers.sh: Prowlarr 代理配置、索引器注入与应用整合
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 2: 索引器与其代理配置..."

# 获取 Prowlarr API Key (如果环境变量中没有，则尝试从文件读取)
if [ -z "$PROWLARR_KEY" ]; then
    PROWLARR_KEY=$(get_api_key "prowlarr")
fi

if [ -z "$PROWLARR_KEY" ]; then
    log_error "无法获取 Prowlarr API Key，跳过执行 02-indexers.sh"
    exit 1
fi

# 1. 注入 Prowlarr 全量整合 (Apps, Proxies, QB)
log_info "正在配置 Prowlarr 基础设施 (代理/下载器/应用)..."
run_python_api "$PROWLARR_KEY" "$PROWLARR_PORT" \
               "$SONARR_KEY" "$RADARR_KEY" "$WHISPARR_KEY" "$QB_PASS" \
               "$TARGET_USER" "$QBITTORRENT_HOSTNAME" "$QBITTORRENT_PORT" \
               "$FLARESOLVERR_HOSTNAME" "$FLARESOLVERR_PORT" \
               "$PROWLARR_HOSTNAME" "$PROWLARR_PORT" \
               "$SONARR_HOSTNAME" "$SONARR_PORT" \
               "$RADARR_HOSTNAME" "$RADARR_PORT" \
               "$WHISPARR_HOSTNAME" "$WHISPARR_PORT" <<'PYEOF'
import urllib.request, json, sys, os
key, port, skey, rkey, wkey, qpw, user, qhost, qport = sys.argv[1:10]
fs_host, fs_port, prowlarr_host, prowlarr_port, sonarr_host, sonarr_port, radarr_host, radarr_port, whisparr_host, whisparr_port = sys.argv[10:20]
base = f"http://localhost:{port}/api/v1"; hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read().decode())

def get_or_create_tag(label):
    all_tags = api_call("tag")
    found = next((x for x in all_tags if x["label"].lower() == label.lower()), None)
    if not found:
        found = api_call("tag", d={"label": label}, m="POST")
    return found["id"]

def save(ep, impl, name, fields, tags=None, extra=None):
    try:
        schemas = api_call(f"{ep}/schema")
        schema = next(s for s in schemas if s.get("implementation") == impl)
        schema.update({"name": name, "enable": True})
        if ep == "applications": schema["syncLevel"] = "fullSync"
        if extra: schema.update(extra)
        if tags:
            schema["tags"] = [get_or_create_tag(t) for t in tags]
        for f in schema.get("fields", []):
            if f["name"] in fields: f["value"] = fields[f["name"]]
        api_call(ep, d=schema, m="POST")
        print(f"  \033[32m[✓]\033[0m {name} 配置成功")
    except Exception as e:
        print(f"  \033[31m[✗]\033[0m {name} 失败: {e}")

# 预创建标签
get_or_create_tag("default")
get_or_create_tag("flaresolverr")

# 配置代理 (仅 FlareSolverr)
save("indexerproxy", "FlareSolverr", "FlareSolverr Proxy", {"host": f"http://{fs_host}:{fs_port}/", "requestTimeout": 60}, tags=["flaresolverr"])

# 配置下载器
save("downloadclient", "QBittorrent", "qBittorrent", {"host": qhost, "port": int(qport), "username": user, "password": qpw})

# 配置应用同步 (关联 default 和 flaresolverr 两个标签)
save("applications", "Sonarr", "Sonarr", {"prowlarrUrl": f"http://{prowlarr_host}:{prowlarr_port}", "baseUrl": f"http://{sonarr_host}:{sonarr_port}", "apiKey": skey}, tags=["default", "flaresolverr"])
save("applications", "Radarr", "Radarr", {"prowlarrUrl": f"http://{prowlarr_host}:{prowlarr_port}", "baseUrl": f"http://{radarr_host}:{radarr_port}", "apiKey": rkey}, tags=["default", "flaresolverr"])
save("applications", "Whisparr", "Whisparr", {"prowlarrUrl": f"http://{prowlarr_host}:{prowlarr_port}", "baseUrl": f"http://{whisparr_host}:{whisparr_port}", "apiKey": wkey}, tags=["default", "flaresolverr"])

# 2. 注入常用索引器
# - default: 普通站点，直接访问
# - flaresolverr: 需要 Cloudflare 验证的站点
print("\n正在注入常用索引器清单...")
INDEXERS = [
    {"name": "Nyaa.si", "impl": "Cardigann", "def": "nyaasi", "tag": "default"},
    {"name": "DMHY", "impl": "Cardigann", "def": "dmhy", "tag": "flaresolverr"},
    {"name": "Mikan", "impl": "Cardigann", "def": "mikan", "tag": "default"},
    {"name": "1337x", "impl": "Cardigann", "def": "1337x", "tag": "flaresolverr"},
    {"name": "ACG.RIP", "impl": "Cardigann", "def": "acgrip", "tag": "flaresolverr"},
    {"name": "Bangumi Moe", "impl": "Cardigann", "def": "bangumi-moe", "tag": "default"},
    {"name": "YTS", "impl": "Cardigann", "def": "yts", "tag": "default"},
    {"name": "EZTV", "impl": "Cardigann", "def": "eztv", "tag": "flaresolverr"},
    {"name": "showRSS", "impl": "Cardigann", "def": "showrss", "tag": "default"},
    {"name": "The Pirate Bay", "impl": "Cardigann", "def": "thepiratebay", "tag": "flaresolverr"},
    {"name": "RuTor", "impl": "Cardigann", "def": "rutor", "tag": "default"},
    {"name": "OneJAV", "impl": "Cardigann", "def": "onejav", "tag": "flaresolverr"},
    {"name": "0Magnet", "impl": "Cardigann", "def": "0magnet", "tag": "default"},
    {"name": "Free JAV Torrent", "impl": "Cardigann", "def": "freejavtorrent", "tag": "default"},
    {"name": "PornRips", "impl": "Cardigann", "def": "pornrips", "tag": "default"},
    {"name": "sukebei.nyaa.si", "impl": "Cardigann", "def": "sukebeinyaasi", "tag": "default"},
]

schemas = api_call("indexer/schema")
existing = {x["name"].lower(): x["id"] for x in api_call("indexer")}

for idx in INDEXERS:
    name = idx["name"]
    if name.lower() in existing: continue
    print(f"  ➜ 注入 {name}...", end=" ", flush=True)
    try:
        schema = next(s for s in schemas if s["implementation"] == idx["impl"] and
                      any(f["name"]=="definitionFile" and f["value"]==idx["def"] for f in s["fields"]))
        schema.update({"name": name, "enable": True, "appProfileId": 1, "priority": 25})
        schema["tags"] = [get_or_create_tag(idx["tag"])]
        api_call("indexer", d=schema, m="POST")
        print("\033[32m[✓]\033[0m")
    except Exception as e:
        print(f"\033[31m[✗]\033[0m {e}")
PYEOF

# ==============================================================================
# 3. 在各 Arr 服务中配置 qBittorrent 下载客户端
# ==============================================================================
log_info "正在配置各 Arr 服务的下载客户端..."

configure_download_client() {
    local svc=$1; local port=$2; local key=$3
    log_info "配置 $svc 下载客户端..."

    run_python_api "$svc" "$port" "$key" "$QBITTORRENT_HOSTNAME" "$QBITTORRENT_PORT" "$TARGET_USER" "$QB_PASS" <<'PYEOF'
import urllib.request, json, sys
svc, port, key, qhost, qport, quser, qpass = sys.argv[1:8]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    existing = api_call("downloadclient")
    if any(x["name"] == "qBittorrent" for x in existing):
        print(f"  \033[32m[✓]\033[0m {svc} qBittorrent 已存在")
    else:
        schemas = api_call("downloadclient/schema")
        schema = next(s for s in schemas if s["implementation"] == "QBittorrent")
        schema.update({
            "name": "qBittorrent",
            "enable": True,
            "fields": [
                {"name": "host", "value": qhost},
                {"name": "port", "value": int(qport)},
                {"name": "username", "value": quser},
                {"name": "password", "value": qpass},
                {"name": "category", "value": ""},
                {"name": "priority", "value": 1},
                {"name": "useSsl", "value": False}
            ]
        })
        for f in schema.get("fields", []):
            if f["name"] in ["host", "port", "username", "password"]:
                f["value"] = {"host": qhost, "port": int(qport), "username": quser, "password": qpass}.get(f["name"], f.get("value"))
        api_call("downloadclient", d=schema, m="POST")
        print(f"  \033[32m[✓]\033[0m {svc} qBittorrent 配置成功")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 失败: {e}")
PYEOF
}

[ -n "$SONARR_KEY" ] && configure_download_client "sonarr" "$SONARR_PORT" "$SONARR_KEY"
[ -n "$RADARR_KEY" ] && configure_download_client "radarr" "$RADARR_PORT" "$RADARR_KEY"
[ -n "$WHISPARR_KEY" ] && configure_download_client "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY"
