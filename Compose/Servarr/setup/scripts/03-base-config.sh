#!/bin/bash

# ==============================================================================
# 03-base-config.sh: 基础配置注入 (语言/元数据/命名规范/根目录/下载客户端)
#
# 功能：
#   1. 配置 UI 语言和电影/剧集信息语言为中文
#   2. 启用 Kodi/XBMC 元数据刮削
#   3. 设置统一的命名规范 (含 :zh 中文标记)
#   4. 配置默认根目录 /media
#   5. 配置 qBittorrent 下载客户端
#
# 适用于: Radarr, Sonarr, Whisparr
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 3: 基础配置注入 (语言/元数据/命名规范/根目录/下载客户端)..."

CHINESE_LANG_ID=10

# 获取 API Keys (如果环境变量中没有，则尝试从文件读取)
[ -z "$RADARR_KEY" ] && RADARR_KEY=$(get_api_key "radarr")
[ -z "$SONARR_KEY" ] && SONARR_KEY=$(get_api_key "sonarr")
[ -z "$WHISPARR_KEY" ] && WHISPARR_KEY=$(get_api_key "whisparr")

configure_ui_language() {
    local svc=$1
    local port=$2
    local key=$3
    local has_movie_info=$4

    log_info "配置 $svc UI 语言..."

    run_python_api "$svc" "$port" "$key" "$CHINESE_LANG_ID" "$has_movie_info" <<'PYEOF'
import urllib.request, json, sys
svc, port, key, lang_id, has_movie_info = sys.argv[1:6]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    config = api_call("config/ui")
    needs_update = False

    if config.get("uiLanguage") != int(lang_id):
        config["uiLanguage"] = int(lang_id)
        needs_update = True

    if has_movie_info == "true" and config.get("movieInfoLanguage") != int(lang_id):
        config["movieInfoLanguage"] = int(lang_id)
        needs_update = True

    if needs_update:
        api_call("config/ui", d=config, m="PUT")
        print(f"  \033[32m[✓]\033[0m {svc} UI 语言已配置为中文")
    else:
        print(f"  \033[32m[✓]\033[0m {svc} UI 语言已为中文，跳过")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} UI 语言配置失败: {e}")
PYEOF
}

configure_metadata() {
    local svc=$1
    local port=$2
    local key=$3
    local lang_field=$4

    log_info "配置 $svc 元数据刮削..."

    run_python_api "$svc" "$port" "$key" "$CHINESE_LANG_ID" "$lang_field" <<'PYEOF'
import urllib.request, json, sys
svc, port, key, lang_id, lang_field = sys.argv[1:6]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    metadata_list = api_call("metadata")
    xbmc = next((m for m in metadata_list if m.get("implementation") == "XbmcMetadata"), None)

    if not xbmc:
        print(f"  \033[31m[✗]\033[0m {svc} 未找到 XbmcMetadata 实现")
        sys.exit(0)

    needs_update = False

    if not xbmc.get("enable"):
        xbmc["enable"] = True
        needs_update = True

    if lang_field:
        for field in xbmc.get("fields", []):
            if field.get("name") == lang_field and field.get("value") != int(lang_id):
                field["value"] = int(lang_id)
                needs_update = True

    if needs_update:
        api_call(f"metadata/{xbmc['id']}", d=xbmc, m="PUT")
        print(f"  \033[32m[✓]\033[0m {svc} 元数据配置已更新 (Kodi/XBMC 已启用)")
    else:
        print(f"  \033[32m[✓]\033[0m {svc} 元数据配置正确，跳过")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 元数据配置失败: {e}")
PYEOF
}

configure_naming_radarr() {
    local svc=$1
    local port=$2
    local key=$3

    log_info "配置 $svc 命名规范..."

    run_python_api "$svc" "$port" "$key" <<'PYEOF'
import urllib.request, json, sys
svc, port, key = sys.argv[1:4]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

MOVIE_FORMAT = "{Movie CleanTitle:zh} ({Release Year}) {Quality Full}"
MOVIE_FOLDER_FORMAT = "{Movie CleanTitle:zh} ({Release Year})"

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    config = api_call("config/naming")

    if config.get("renameMovies"):
        print(f"  \033[32m[✓]\033[0m {svc} 已启用重命名，跳过")
        sys.exit(0)

    config["renameMovies"] = True
    config["replaceIllegalCharacters"] = True
    config["standardMovieFormat"] = MOVIE_FORMAT
    config["movieFolderFormat"] = MOVIE_FOLDER_FORMAT
    api_call("config/naming", d=config, m="PUT")
    print(f"  \033[32m[✓]\033[0m {svc} 命名规范已配置")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 命名规范配置失败: {e}")
PYEOF
}

configure_naming_sonarr() {
    local svc=$1
    local port=$2
    local key=$3

    log_info "配置 $svc 命名规范..."

    run_python_api "$svc" "$port" "$key" <<'PYEOF'
import urllib.request, json, sys
svc, port, key = sys.argv[1:4]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

STANDARD_FORMAT = "{Series CleanTitleYear:zh} - S{season:00}E{episode:00} - {Episode CleanTitle:zh} {Quality Full}"
DAILY_FORMAT = "{Series CleanTitle:zh} - {Air-Date} - {Episode CleanTitle:zh} {Quality Full}"
ANIME_FORMAT = "{Series CleanTitleYear:zh} - S{season:00}E{episode:00} - {Episode CleanTitle:zh} {Quality Full}"
SERIES_FOLDER_FORMAT = "{Series CleanTitle}"

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    config = api_call("config/naming")

    if config.get("renameEpisodes"):
        print(f"  \033[32m[✓]\033[0m {svc} 已启用重命名，跳过")
        sys.exit(0)

    config["renameEpisodes"] = True
    config["replaceIllegalCharacters"] = True
    config["standardEpisodeFormat"] = STANDARD_FORMAT
    config["dailyEpisodeFormat"] = DAILY_FORMAT
    config["animeEpisodeFormat"] = ANIME_FORMAT
    config["seriesFolderFormat"] = SERIES_FOLDER_FORMAT
    api_call("config/naming", d=config, m="PUT")
    print(f"  \033[32m[✓]\033[0m {svc} 命名规范已配置")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 命名规范配置失败: {e}")
PYEOF
}

configure_naming_whisparr() {
    local svc=$1
    local port=$2
    local key=$3

    log_info "配置 $svc 命名规范..."

    run_python_api "$svc" "$port" "$key" <<'PYEOF'
import urllib.request, json, sys
svc, port, key = sys.argv[1:4]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

# Whisparr 命名格式定义
MOVIE_FORMAT = "{Scene Code} {Movie CleanTitle:zh} ({Release Year}) [{Quality Full}]"
MOVIE_FOLDER_FORMAT = "movies/{Scene CleanPerformersFemale:zh:1}/{Scene Code} {Movie CleanTitle:zh} ({Release Date})"
SCENE_FORMAT = "{Scene CleanTitle:zh} {Scene CleanPerformers:zh} {Release Date} [{Quality Full}]"
SCENE_FOLDER_FORMAT = "scenes/{Studio CleanTitle:zh}/{Scene CleanTitle} ({Release Year})"

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    config = api_call("config/naming")

    if config.get("renameMovies") and config.get("renameScenes"):
        print(f"  \033[32m[✓]\033[0m {svc} 已启用重命名，跳过")
        sys.exit(0)

    config["renameMovies"] = True
    config["renameScenes"] = True
    config["replaceIllegalCharacters"] = True
    config["colonReplacementFormat"] = "smart"
    config["standardMovieFormat"] = MOVIE_FORMAT
    config["movieFolderFormat"] = MOVIE_FOLDER_FORMAT
    config["standardSceneFormat"] = SCENE_FORMAT
    config["sceneFolderFormat"] = SCENE_FOLDER_FORMAT
    api_call("config/naming", d=config, m="PUT")
    print(f"  \033[32m[✓]\033[0m {svc} 命名规范已配置")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 命名规范配置失败: {e}")
PYEOF
}

configure_root_folder() {
    local svc=$1
    local port=$2
    local key=$3
    local root_path=$4

    log_info "配置 $svc 根目录..."

    run_python_api "$svc" "$port" "$key" "$root_path" <<'PYEOF'
import urllib.request, json, sys
svc, port, key, root_path = sys.argv[1:5]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    root_folders = api_call("rootfolder")
    
    if root_folders and len(root_folders) > 0:
        print(f"  \033[32m[✓]\033[0m {svc} 已有根目录，跳过默认设置")
        sys.exit(0)
    
    new_folder = {"path": root_path}
    api_call("rootfolder", d=new_folder, m="POST")
    print(f"  \033[32m[✓]\033[0m {svc} 根目录 {root_path} 已添加")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 根目录配置失败: {e}")
PYEOF
}

configure_download_client() {
    local svc=$1
    local port=$2
    local key=$3

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
    qb_existing = next((x for x in existing if x["name"] == "qBittorrent"), None)
    schemas = api_call("downloadclient/schema")
    schema = next(s for s in schemas if s["implementation"] == "QBittorrent")
    schema.update({"name": "qBittorrent", "enable": True})
    for f in schema.get("fields", []):
        if f["name"] == "host": f["value"] = qhost
        elif f["name"] == "port": f["value"] = int(qport)
        elif f["name"] == "username": f["value"] = quser
        elif f["name"] == "password": f["value"] = qpass
    if qb_existing:
        schema["id"] = qb_existing["id"]
        api_call(f"downloadclient/{qb_existing['id']}", d=schema, m="PUT")
        print(f"  \033[32m[✓]\033[0m {svc} qBittorrent 已存在，配置已更新")
    else:
        api_call("downloadclient", d=schema, m="POST")
        print(f"  \033[32m[✓]\033[0m {svc} qBittorrent 配置成功")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 失败: {e}")
PYEOF
}

if [ -n "$RADARR_KEY" ]; then
    log_info "配置 Radarr..."
    configure_ui_language "radarr" "$RADARR_PORT" "$RADARR_KEY" "true"
    configure_metadata "radarr" "$RADARR_PORT" "$RADARR_KEY" "movieMetadataLanguage"
    configure_naming_radarr "radarr" "$RADARR_PORT" "$RADARR_KEY"
    configure_root_folder "radarr" "$RADARR_PORT" "$RADARR_KEY" "/media"
    configure_download_client "radarr" "$RADARR_PORT" "$RADARR_KEY"
fi

if [ -n "$SONARR_KEY" ]; then
    log_info "配置 Sonarr..."
    configure_ui_language "sonarr" "$SONARR_PORT" "$SONARR_KEY" "false"
    configure_metadata "sonarr" "$SONARR_PORT" "$SONARR_KEY" ""
    configure_naming_sonarr "sonarr" "$SONARR_PORT" "$SONARR_KEY"
    configure_root_folder "sonarr" "$SONARR_PORT" "$SONARR_KEY" "/media"
    configure_download_client "sonarr" "$SONARR_PORT" "$SONARR_KEY"
fi

if [ -n "$WHISPARR_KEY" ]; then
    log_info "配置 Whisparr..."
    configure_ui_language "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY" "false"
    configure_metadata "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY" "movieMetadataLanguage"
    configure_naming_whisparr "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY"
    configure_root_folder "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY" "/media"
    configure_download_client "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY"
fi

log_success "基础配置注入完成"
