#!/bin/bash

# ==============================================================================
# 06-jellyseerr.sh: Jellyseerr 初始化与 Jellyfin 连接
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 6: Jellyseerr 初始化与 Jellyfin 连接..."

[ -z "$RADARR_KEY" ] && RADARR_KEY=$(get_api_key "radarr")
[ -z "$SONARR_KEY" ] && SONARR_KEY=$(get_api_key "sonarr")

JELLYFIN_PORT=8096
JELLYSEERR_URL="http://localhost:$JELLYSEERR_PORT"
COOKIE_FILE="/tmp/jellyseerr_cookie.txt"

# 等待 Jellyseerr 服务启动
echo -ne "$INFO 等待 Jellyseerr ($JELLYSEERR_PORT) 响应... "
for i in $(seq 1 30); do
    if curl -s --noproxy "*" -m 2 "$JELLYSEERR_URL" > /dev/null 2>&1; then
        echo -e "$CHECK 正常"
        break
    fi
    sleep 2
done
if [ $i -eq 30 ]; then
    echo -e "$ERROR 超时"
    exit 1
fi

# 清理 cookie 文件
rm -f "$COOKIE_FILE"

# 1. 登录 Jellyfin 并初始化
log_info "检查 Jellyseerr 初始化状态..."
PUBLIC_SETTINGS=$(curl -s --noproxy "*" "$JELLYSEERR_URL/api/v1/settings/public")

if echo "$PUBLIC_SETTINGS" | grep -q '"initialized":true'; then
    log_success "Jellyseerr 已初始化，跳过所有配置步骤"
    exit 1
fi

log_info "正在登录 Jellyfin 并初始化 Jellyseerr..."

# 检查是否已配置 Jellyfin (mediaServerType=2 表示 Jellyfin)
if echo "$PUBLIC_SETTINGS" | grep -q '"mediaServerType":2'; then
    # Jellyfin 已配置，只传递用户名密码
    log_info "Jellyfin 已配置，使用现有配置登录..."
    RESPONSE=$(curl -s --noproxy "*" -c "$COOKIE_FILE" -b "$COOKIE_FILE" \
        -X POST \
        -H "Content-Type: application/json" \
        -d "{
            \"username\": \"$JELLYFIN_USER\",
            \"password\": \"$JELLYFIN_PASS\"
        }" \
        "$JELLYSEERR_URL/api/v1/auth/jellyfin")
else
    # Jellyfin 未配置，传递完整配置
    log_info "Jellyfin 未配置，进行初始配置..."
    RESPONSE=$(curl -s --noproxy "*" -c "$COOKIE_FILE" -b "$COOKIE_FILE" \
        -X POST \
        -H "Content-Type: application/json" \
        -d "{
            \"username\": \"$JELLYFIN_USER\",
            \"password\": \"$JELLYFIN_PASS\",
            \"hostname\": \"$JELLYFIN_BASE_URL\",
            \"port\": $JELLYFIN_PORT,
            \"useSsl\": false,
            \"urlBase\": \"\",
            \"email\": \"$JELLYFIN_EMAIL\",
            \"serverType\": 2
        }" \
        "$JELLYSEERR_URL/api/v1/auth/jellyfin")
fi

if echo "$RESPONSE" | grep -q '"id"'; then
    log_success "Jellyfin 登录成功"
elif echo "$RESPONSE" | grep -q 'Jellyfin hostname already configured'; then
    log_info "Jellyfin 已配置，重新登录获取 session..."
    RESPONSE=$(curl -s --noproxy "*" -c "$COOKIE_FILE" -b "$COOKIE_FILE" \
        -X POST \
        -H "Content-Type: application/json" \
        -d "{
            \"username\": \"$JELLYFIN_USER\",
            \"password\": \"$JELLYFIN_PASS\"
        }" \
        "$JELLYSEERR_URL/api/v1/auth/jellyfin")
    
    if echo "$RESPONSE" | grep -q '"id"'; then
        log_success "Jellyfin 登录成功"
    else
        log_error "Jellyfin 登录失败: $RESPONSE"
        exit 1
    fi
else
    log_error "Jellyfin 登录失败: $RESPONSE"
    exit 1
fi

# 2. 初始化 Jellyseerr 用户登录密码
log_info "检查 Jellyseerr 用户密码状态..."
HAS_PASSWORD=$(curl -s --noproxy "*" -b "$COOKIE_FILE" "$JELLYSEERR_URL/api/v1/user/1/settings/password")
if echo "$HAS_PASSWORD" | grep -q '"hasPassword":true'; then
    log_success "Jellyseerr 用户密码已设置，跳过"
else
    # 检查密码长度 (Jellyseerr 要求至少 8 个字符)
    PASS_LEN=${#TARGET_PASS}
    if [ "$PASS_LEN" -lt 8 ]; then
        log_warn "Jellyseerr 用户密码未设置"
        log_warn "原因: TARGET_PASS 长度为 $PASS_LEN 个字符，Jellyseerr 要求至少 8 个字符"
        log_warn "请登录 Jellyseerr WebUI 手动设置密码: http://localhost:$JELLYSEERR_PORT/users/1/settings/password"
    else
        log_info "正在设置 Jellyseerr 用户密码..."
        
        PASS_RESPONSE=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
            -X POST \
            -H "Content-Type: application/json" \
            -d "{\"newPassword\": \"$TARGET_PASS\"}" \
            -w "\n%{http_code}" \
            "$JELLYSEERR_URL/api/v1/user/1/settings/password")
        
        HTTP_CODE=$(echo "$PASS_RESPONSE" | tail -n 1)
        if [ "$HTTP_CODE" = "204" ]; then
            log_success "Jellyseerr 用户密码设置成功"
        else
            log_warn "Jellyseerr 用户密码设置失败: HTTP $HTTP_CODE"
            log_warn "请登录 Jellyseerr WebUI 手动设置密码: http://localhost:$JELLYSEERR_PORT"
        fi
    fi
fi

# 3. 配置媒体服务器（同步库）
log_info "同步 Jellyfin 媒体库..."

# 3.1 同步媒体库列表
LIBRARIES=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
    "$JELLYSEERR_URL/api/v1/settings/jellyfin/library?sync=true")

if echo "$LIBRARIES" | grep -q '"id"'; then
    # 提取所有媒体库 ID
    LIBRARY_IDS=$(echo "$LIBRARIES" | python3 -c "
import json, sys
try:
    libs = json.load(sys.stdin)
    print(','.join([lib['id'] for lib in libs]))
except:
    sys.exit(1)
" 2>/dev/null)
    
    if [ -n "$LIBRARY_IDS" ]; then
        log_info "启用媒体库: $LIBRARY_IDS"
        
        # 3.2 启用所有媒体库
        ENABLE_RESPONSE=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
            "$JELLYSEERR_URL/api/v1/settings/jellyfin/library?enable=$LIBRARY_IDS" \
            -w "\n%{http_code}")
        
        HTTP_CODE=$(echo "$ENABLE_RESPONSE" | tail -n 1)
        if [ "$HTTP_CODE" = "200" ]; then
            log_success "Jellyfin 媒体库启用完成"
        else
            log_warn "Jellyfin 媒体库启用返回 HTTP $HTTP_CODE"
        fi
    else
        log_warn "未找到有效的媒体库 ID"
    fi
else
    log_warn "Jellyfin 媒体库同步失败或无媒体库"
fi

# 4. 配置 Radarr
log_info "配置 Radarr..."
if [ -n "$RADARR_KEY" ]; then
    # 检查 Radarr 是否已配置
    RADARR_EXISTING=$(curl -s --noproxy "*" -b "$COOKIE_FILE" "$JELLYSEERR_URL/api/v1/settings/radarr")
    if [ "$RADARR_EXISTING" = "[]" ]; then
        log_info "测试 Radarr 连接..."
        RADARR_TEST=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
            -X POST \
            -H "Content-Type: application/json" \
            -d "{
                \"hostname\": \"$RADARR_HOSTNAME\",
                \"port\": $RADARR_PORT,
                \"apiKey\": \"$RADARR_KEY\",
                \"useSsl\": false
            }" \
            "$JELLYSEERR_URL/api/v1/settings/radarr/test")
        
        if echo "$RADARR_TEST" | grep -q '"profiles"'; then
            # 提取第一个 profile 和 rootFolder
            RADARR_PROFILE_ID=$(echo "$RADARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('profiles'):
        print(data['profiles'][0]['id'])
except:
    sys.exit(1)
" 2>/dev/null)
            
            RADARR_PROFILE_NAME=$(echo "$RADARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('profiles'):
        print(data['profiles'][0]['name'])
except:
    sys.exit(1)
" 2>/dev/null)
            
            RADARR_ROOT_FOLDER=$(echo "$RADARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('rootFolders'):
        print(data['rootFolders'][0]['path'])
except:
    sys.exit(1)
" 2>/dev/null)
            
            if [ -n "$RADARR_PROFILE_ID" ] && [ -n "$RADARR_ROOT_FOLDER" ]; then
                log_info "添加 Radarr 配置..."
                RADARR_RESPONSE=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
                    -X POST \
                    -H "Content-Type: application/json" \
                    -d "{
                        \"name\": \"Radarr\",
                        \"hostname\": \"$RADARR_HOSTNAME\",
                        \"port\": $RADARR_PORT,
                        \"apiKey\": \"$RADARR_KEY\",
                        \"useSsl\": false,
                        \"activeProfileId\": $RADARR_PROFILE_ID,
                        \"activeProfileName\": \"$RADARR_PROFILE_NAME\",
                        \"activeDirectory\": \"$RADARR_ROOT_FOLDER\",
                        \"is4k\": false,
                        \"isDefault\": true,
                        \"minimumAvailability\": \"released\",
                        \"tags\": [],
                        \"syncEnabled\": false,
                        \"preventSearch\": false,
                        \"tagRequests\": false,
                        \"overrideRule\": []
                    }" \
                    "$JELLYSEERR_URL/api/v1/settings/radarr" \
                    -w "\n%{http_code}")
                
                HTTP_CODE=$(echo "$RADARR_RESPONSE" | tail -n 1)
                if [ "$HTTP_CODE" = "201" ]; then
                    log_success "Radarr 配置完成"
                else
                    log_warn "Radarr 配置返回 HTTP $HTTP_CODE"
                fi
            elif [ -z "$RADARR_ROOT_FOLDER" ]; then
                log_warn "Radarr 未配置 Root Folder，请先在 Radarr 中添加媒体根目录"
                log_warn "Radarr WebUI: http://localhost:$RADARR_PORT/settings/mediamanagement"
            else
                log_warn "无法获取 Radarr profile"
            fi
        else
            log_warn "Radarr 连接测试失败: $RADARR_TEST"
        fi
    else
        log_success "Radarr 已配置，跳过"
    fi
else
    log_warn "未找到 Radarr API Key"
fi

# 5. 配置 Sonarr
log_info "配置 Sonarr..."
if [ -n "$SONARR_KEY" ]; then
    # 检查 Sonarr 是否已配置
    SONARR_EXISTING=$(curl -s --noproxy "*" -b "$COOKIE_FILE" "$JELLYSEERR_URL/api/v1/settings/sonarr")
    if [ "$SONARR_EXISTING" = "[]" ]; then
        log_info "测试 Sonarr 连接..."
        SONARR_TEST=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
            -X POST \
            -H "Content-Type: application/json" \
            -d "{
                \"hostname\": \"$SONARR_HOSTNAME\",
                \"port\": $SONARR_PORT,
                \"apiKey\": \"$SONARR_KEY\",
                \"useSsl\": false
            }" \
            "$JELLYSEERR_URL/api/v1/settings/sonarr/test")
        
        if echo "$SONARR_TEST" | grep -q '"profiles"'; then
            # 提取第一个 profile 和 rootFolder
            SONARR_PROFILE_ID=$(echo "$SONARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('profiles'):
        print(data['profiles'][0]['id'])
except:
    sys.exit(1)
" 2>/dev/null)
            
            SONARR_PROFILE_NAME=$(echo "$SONARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('profiles'):
        print(data['profiles'][0]['name'])
except:
    sys.exit(1)
" 2>/dev/null)
            
            SONARR_ROOT_FOLDER=$(echo "$SONARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('rootFolders'):
        print(data['rootFolders'][0]['path'])
except:
    sys.exit(1)
" 2>/dev/null)
            
            SONARR_LANGUAGE_PROFILE_ID=$(echo "$SONARR_TEST" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    if data.get('languageProfiles'):
        print(data['languageProfiles'][0]['id'])
except:
    pass
" 2>/dev/null)
            
            if [ -n "$SONARR_PROFILE_ID" ] && [ -n "$SONARR_ROOT_FOLDER" ]; then
                log_info "添加 Sonarr 配置..."
                
                # 构建 JSON，可选添加 languageProfileId
                SONARR_JSON="{
                    \"name\": \"Sonarr\",
                    \"hostname\": \"$SONARR_HOSTNAME\",
                    \"port\": $SONARR_PORT,
                    \"apiKey\": \"$SONARR_KEY\",
                    \"useSsl\": false,
                    \"activeProfileId\": $SONARR_PROFILE_ID,
                    \"activeProfileName\": \"$SONARR_PROFILE_NAME\",
                    \"activeDirectory\": \"$SONARR_ROOT_FOLDER\",
                    \"is4k\": false,
                    \"isDefault\": true,
                    \"seriesType\": \"standard\",
                    \"animeSeriesType\": \"anime\",
                    \"tags\": [],
                    \"syncEnabled\": false,
                    \"preventSearch\": false,
                    \"tagRequests\": false,
                    \"overrideRule\": [],
                    \"enableSeasonFolders\": false,
                    \"monitorNewItems\": \"all\""
                
                if [ -n "$SONARR_LANGUAGE_PROFILE_ID" ]; then
                    SONARR_JSON="$SONARR_JSON,
                    \"activeLanguageProfileId\": $SONARR_LANGUAGE_PROFILE_ID"
                fi
                
                SONARR_JSON="$SONARR_JSON
                }"
                
                SONARR_RESPONSE=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
                    -X POST \
                    -H "Content-Type: application/json" \
                    -d "$SONARR_JSON" \
                    "$JELLYSEERR_URL/api/v1/settings/sonarr" \
                    -w "\n%{http_code}")
                
                HTTP_CODE=$(echo "$SONARR_RESPONSE" | tail -n 1)
                if [ "$HTTP_CODE" = "201" ]; then
                    log_success "Sonarr 配置完成"
                else
                    log_warn "Sonarr 配置返回 HTTP $HTTP_CODE"
                fi
            elif [ -z "$SONARR_ROOT_FOLDER" ]; then
                log_warn "Sonarr 未配置 Root Folder，请先在 Sonarr 中添加媒体根目录"
                log_warn "Sonarr WebUI: http://localhost:$SONARR_PORT/settings/mediamanagement"
            else
                log_warn "无法获取 Sonarr profile"
            fi
        else
            log_warn "Sonarr 连接测试失败: $SONARR_TEST"
        fi
    else
        log_success "Sonarr 已配置，跳过"
    fi
else
    log_warn "未找到 Sonarr API Key"
fi

# 6. 配置 Radarr/Sonarr → Jellyfin 通知连接
# 目的: Radarr/Sonarr 导入电影/剧集后自动通知 Jellyfin 刷新媒体库
# 解决: Jellyseerr 请求长期停留在"处理中"状态的问题
log_info "配置 Radarr/Sonarr → Jellyfin 通知连接..."

# 从 Jellyseerr 设置中获取 Jellyfin API Key（Jellyseerr 初始化时自动生成）
JELLYFIN_SETTINGS=$(curl -s --noproxy "*" -b "$COOKIE_FILE" "$JELLYSEERR_URL/api/v1/settings/jellyfin")
JELLYFIN_API_KEY=$(echo "$JELLYFIN_SETTINGS" | python3 -c "
import json, sys
try:
    data = json.load(sys.stdin)
    print(data.get('apiKey', ''))
except:
    print('')
" 2>/dev/null)

if [ -z "$JELLYFIN_API_KEY" ]; then
    log_warn "无法从 Jellyseerr 获取 Jellyfin API Key，跳过通知连接配置"
    log_warn "请在 Radarr/Sonarr Settings → Connect 中手动添加 Jellyfin 连接"
else
    # 提取 Jellyfin 主机地址（去除 http:// 前缀，供容器间通讯使用）
    JELLYFIN_HOST=$(echo "$JELLYFIN_BASE_URL" | sed 's|^https\?://||' | sed 's|/$||')

    configure_jellyfin_notify() {
        local svc=$1
        local port=$2
        local key=$3

        python3 - "$svc" "$port" "$key" "$JELLYFIN_HOST" "$JELLYFIN_PORT" "$JELLYFIN_API_KEY" <<'PYEOF'
import urllib.request, json, sys, re

svc, port, key, jf_host, jf_port, jf_api_key = sys.argv[1:7]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    # 检查是否已存在 Jellyfin/MediaBrowser 通知连接
    existing = api_call("notification")
    if any(n.get("implementation") == "MediaBrowser" for n in existing):
        print(f"  \033[32m[✓]\033[0m {svc} → Jellyfin 通知连接已存在，跳过")
        sys.exit(0)

    # 获取 MediaBrowser (Jellyfin) 通知 schema
    # 注: Radarr v6 / Sonarr v4 中 Jellyfin 的 implementation 名称为 "MediaBrowser"
    schemas = api_call("notification/schema")
    schema = next((s for s in schemas if s["implementation"] == "MediaBrowser"), None)
    if not schema:
        print(f"  \033[31m[✗]\033[0m {svc} 未找到 MediaBrowser/Jellyfin 通知 schema")
        sys.exit(1)

    # 安全处理主机名（去除可能残留的协议前缀）
    host = re.sub(r'^https?://', '', jf_host).rstrip('/')

    # 配置通知触发事件
    schema.update({
        "name": "Jellyfin",
        "enable": True,
        "onGrab": False,               # 抓取时不通知（尚未下载完成）
        "onDownload": True,            # 导入完成时通知（核心触发点）
        "onUpgrade": True,             # 质量升级时通知
        "onRename": True,              # 重命名时通知
        "onMovieFileDelete": True,     # [Radarr] 电影文件删除时通知
        "onImportComplete": True,      # [Sonarr] 导入完成时通知
        "onEpisodeFileDelete": True,   # [Sonarr] 剧集文件删除时通知
    })

    # 填写 Jellyfin 连接参数
    for f in schema.get("fields", []):
        if f["name"] == "host": f["value"] = host
        elif f["name"] == "port": f["value"] = int(jf_port)
        elif f["name"] == "apiKey": f["value"] = jf_api_key
        elif f["name"] == "updateLibrary": f["value"] = True
        elif f["name"] == "notify": f["value"] = False

    api_call("notification", d=schema, m="POST")
    print(f"  \033[32m[✓]\033[0m {svc} → Jellyfin 通知连接配置成功 (导入时自动刷新库)")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} Jellyfin 通知配置失败: {e}")
PYEOF
    }

    if [ -n "$RADARR_KEY" ]; then
        configure_jellyfin_notify "radarr" "$RADARR_PORT" "$RADARR_KEY"
    fi

    if [ -n "$SONARR_KEY" ]; then
        configure_jellyfin_notify "sonarr" "$SONARR_PORT" "$SONARR_KEY"
    fi
fi

# 7. 配置 Jellyseerr 主设置
log_info "配置 Jellyseerr 主设置..."
MAIN_SETTINGS_RESPONSE=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
    -X POST \
    -H "Content-Type: application/json" \
    -d "{
        \"locale\": \"zh-CN\",
        \"originalLanguage\": \"zh|en|ja\",
        \"streamingRegion\": \"US\"
    }" \
    "$JELLYSEERR_URL/api/v1/settings/main" \
    -w "\n%{http_code}")

HTTP_CODE=$(echo "$MAIN_SETTINGS_RESPONSE" | tail -n 1)
if [ "$HTTP_CODE" = "200" ]; then
    log_success "Jellyseerr 主设置配置完成"
    log_info "  - Display Language: zh-CN (简体中文)"
    log_info "  - Discover Language: zh|en|ja (中文/英文/日文)"
    log_info "  - Streaming Region: US (美国)"
else
    log_warn "Jellyseerr 主设置配置返回 HTTP $HTTP_CODE"
fi

# 8. 完成初始化
log_info "完成 Jellyseerr 初始化..."
INIT_RESPONSE=$(curl -s --noproxy "*" -b "$COOKIE_FILE" \
    -X POST \
    "$JELLYSEERR_URL/api/v1/settings/initialize" \
    -w "\n%{http_code}")

HTTP_CODE=$(echo "$INIT_RESPONSE" | tail -n 1)
if [ "$HTTP_CODE" = "200" ]; then
    log_success "Jellyseerr 初始化完成"
else
    log_warn "Jellyseerr 初始化返回 HTTP $HTTP_CODE"
fi

# 清理 cookie 文件
rm -f "$COOKIE_FILE"
