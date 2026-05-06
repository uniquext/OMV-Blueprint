#!/bin/bash

# ==============================================================================
# 07-qbitrr.sh: qBitRR 磁力链接管理初始化与配置
#
# 功能：
#   1. 从模板生成 config.toml，替换环境变量占位符与 API Key
#   2. 配置变更时自动重启 qBitRR 容器
#   3. 等待 qBitRR 服务就绪
#
# 注意：qbitrr 默认 WebUI 端口为 6969，模板中改为 6970。
# 首次启动时容器内自动生成默认配置监听 6969，与 Docker 端口映射 6970 不匹配，
# 因此必须先写入配置再等待服务，而非先等待再写配置。
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 7: qBitRR 磁力链接管理初始化..."

# 获取 API Keys (优先从环境变量取，否则从配置文件读取)
[ -z "$SONARR_KEY" ] && SONARR_KEY=$(get_api_key "sonarr")
[ -z "$RADARR_KEY" ] && RADARR_KEY=$(get_api_key "radarr")

QBITRR_TEMPLATE="$SCRIPT_DIR/setup/qbitrr_config.toml.example"
QBITRR_CONFIG="$SCRIPT_DIR/config/qbitrr/config.toml"

# ==============================================================================
# 1. 生成 qBitRR 配置文件 (必须在等待服务之前完成)
# ==============================================================================
log_info "正在配置 qBitRR..."

QBITRR_CHANGED=false

if [ -f "$QBITRR_TEMPLATE" ]; then
    mkdir -p "$(dirname "$QBITRR_CONFIG")"

    TEMP_CONFIG=$(mktemp)
    cp "$QBITRR_TEMPLATE" "$TEMP_CONFIG"

    # 替换环境变量占位符
    perl -pi -e "s|\\\$\{QBITTORRENT_HOSTNAME\}|$QBITTORRENT_HOSTNAME|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{QBITTORRENT_PORT\}|$QBITTORRENT_PORT|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{TARGET_USER\}|$TARGET_USER|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{TARGET_PASS\}|$QB_PASS|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{SONARR_HOSTNAME\}|$SONARR_HOSTNAME|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{SONARR_PORT\}|$SONARR_PORT|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{RADARR_HOSTNAME\}|$RADARR_HOSTNAME|g" "$TEMP_CONFIG"
    perl -pi -e "s|\\\$\{RADARR_PORT\}|$RADARR_PORT|g" "$TEMP_CONFIG"

    # 替换 API Key 占位符
    if [ -n "$SONARR_KEY" ]; then
        perl -pi -e "s|<YOUR_SONARR_API_KEY>|$SONARR_KEY|g" "$TEMP_CONFIG"
    else
        log_warn "未获取到 Sonarr API Key，qBitRR 的 Sonarr 监控将无法工作"
    fi

    if [ -n "$RADARR_KEY" ]; then
        perl -pi -e "s|<YOUR_RADARR_API_KEY>|$RADARR_KEY|g" "$TEMP_CONFIG"
    else
        log_warn "未获取到 Radarr API Key，qBitRR 的 Radarr 监控将无法工作"
    fi

    # 比较新旧配置，仅在变更时写入
    if [ -f "$QBITRR_CONFIG" ]; then
        if diff -q "$TEMP_CONFIG" "$QBITRR_CONFIG" > /dev/null 2>&1; then
            log_info "qBitRR 配置文件无变化，跳过更新"
        else
            mv "$TEMP_CONFIG" "$QBITRR_CONFIG"
            log_success "qBitRR 配置文件已更新"
            QBITRR_CHANGED=true
        fi
    else
        mv "$TEMP_CONFIG" "$QBITRR_CONFIG"
        log_success "qBitRR 配置文件已生成"
        QBITRR_CHANGED=true
    fi

    rm -f "$TEMP_CONFIG" 2>/dev/null

    # 配置变更时重启容器使配置生效
    if [ "$QBITRR_CHANGED" = true ]; then
        log_info "重启 qBitRR 容器以加载新配置..."
        docker restart "$QBITRR_HOSTNAME" > /dev/null 2>&1
        log_success "qBitRR 容器已重启"
    fi
else
    log_error "找不到 qBitRR 配置模板 $QBITRR_TEMPLATE，跳过配置"
fi

# ==============================================================================
# 2. 等待 qBitRR 服务就绪 (配置写入并重启后)
# ==============================================================================
wait_for_service "qBitRR" "$QBITRR_PORT" 20 || log_warn "qBitRR 服务未就绪，请检查容器日志"
