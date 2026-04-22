#!/bin/bash

# ==============================================================================
# 01-init.sh: 服务初始化与安全加固
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 1: 服务初始化与认证配置..."

# 1. qBittorrent 密码初始化
init_qbittorrent() {
    wait_for_service "qBittorrent" "$QBITTORRENT_PORT" 20 || return 1

    log_info "检查 qBittorrent 登录状态..."
    if curl -s --noproxy "*" -i -X POST -d "username=$TARGET_USER&password=$QB_PASS" "http://localhost:$QBITTORRENT_PORT/api/v2/auth/login" | grep -iq "set-cookie: SID="; then
        log_success "qBittorrent 凭据已生效"
    else
        log_info "尝试从 Docker 日志获取临时密码并行初始化..."
        local temp_pass=$(docker logs "$QBITTORRENT_HOSTNAME" 2>&1 | grep "temporary password" | tail -n 1 | awk '{print $NF}')
        if [ -n "$temp_pass" ]; then
            log_info "使用临时密码: $temp_pass"
            local resp=$(curl -s --noproxy "*" -i -X POST -d "username=$TARGET_USER&password=$temp_pass" "http://localhost:$QBITTORRENT_PORT/api/v2/auth/login")
            if echo "$resp" | grep -iq "set-cookie: SID="; then
                local sid=$(echo "$resp" | grep -oE 'SID=[^;]+' | cut -d'=' -f2)
                curl -s --noproxy "*" -b "SID=$sid" "http://localhost:$QBITTORRENT_PORT/api/v2/app/setPreferences" \
                    -d "json={\"web_ui_username\":\"$TARGET_USER\",\"web_ui_password\":\"$QB_PASS\"}" > /dev/null
                log_success "qBittorrent 密码已重置为 $QB_PASS"
            fi
        else
            log_error "无法获取 qBittorrent 初始密码，请检查容器状态"
        fi
    fi
}

# 2. 配置 Servarr 认证 (Forms Mode)
# 参数: service port api_key api_version (v1 或 v3)
configure_auth() {
    local service=$1; local port=$2; local api_key=$3; local api_version=$4
    log_info "配置 $service ($port) 登录认证..."

    run_python_api "$service" "$port" "$api_key" "$TARGET_USER" "$TARGET_PASS" "$api_version" <<'PYEOF'
import urllib.request, json, sys
svc, port, key, user, pw, api_ver = sys.argv[1:7]
url = f"http://localhost:{port}/api/{api_ver}/config/host"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}
try:
    with urllib.request.urlopen(urllib.request.Request(url, headers=hd)) as r: config = json.loads(r.read())
    if (config.get("authenticationMethod") == "forms" and
        config.get("authenticationRequired") == "enabled" and
        config.get("username") == user):
        print(f"  \033[32m[✓]\033[0m {svc} 认证已配置，跳过")
    else:
        config.update({"authenticationMethod": "forms", "authenticationRequired": "enabled", "username": user, "password": pw, "passwordConfirmation": pw})
        urllib.request.urlopen(urllib.request.Request(url, data=json.dumps(config).encode(), headers=hd, method="PUT"))
        print(f"  \033[32m[✓]\033[0m {svc} 认证配置完毕")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} 失败: {e}")
PYEOF
}

# 执行初始化
init_qbittorrent

# 获取 API Keys 并配置认证
# 服务配置映射表: "容器名:端口:配置目录名:前缀:API版本"
SERVICES=(
    "$PROWLARR_HOSTNAME:$PROWLARR_PORT:prowlarr:PROWLARR:v1"
    "$SONARR_HOSTNAME:$SONARR_PORT:sonarr:SONARR:v3"
    "$RADARR_HOSTNAME:$RADARR_PORT:radarr:RADARR:v3"
    "$WHISPARR_HOSTNAME:$WHISPARR_PORT:whisparr:WHISPARR:v3"
)

for svc_entry in "${SERVICES[@]}"; do
    IFS=':' read -r svc port dir_name svc_prefix api_ver <<< "$svc_entry"

    wait_for_service "$svc" "$port" 15
    key=$(get_api_key "$dir_name")
    if [ -n "$key" ]; then
        configure_auth "$svc" "$port" "$key" "$api_ver"
        echo "export ${svc_prefix}_KEY=$key"
    fi
done
