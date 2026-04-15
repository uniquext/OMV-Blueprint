#!/bin/bash

# ==============================================================================
# Servarr 一键启动自动化脚本 (startup.sh)
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SCRIPTS_DIR="$SCRIPT_DIR/setup/scripts"

# 加载公共库与环境
source "$SCRIPTS_DIR/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

# 注销宿主机代理，防止 localhost API 调用被代理拦截返回 502
unset_host_proxy

log_info "🚀 开始执行 Servarr 模块化启动流程..."

# 1. 初始化与认证 (并捕获导出的 API Keys)
INIT_OUTPUT=$($SCRIPTS_DIR/01-init.sh)
echo "$INIT_OUTPUT"
eval "$(echo "$INIT_OUTPUT" | grep "^export")"

# 2. 索引器与整合
export PROWLARR_KEY SONARR_KEY RADARR_KEY WHISPARR_KEY
$SCRIPTS_DIR/02-indexers.sh

# 3. 质量同步与 CF 导入
$SCRIPTS_DIR/03-recyclarr-cf.sh

# 4. 字幕系统配置
$SCRIPTS_DIR/04-bazarr.sh

log_success "✨ 所有模块初始化完成！"
log_info "建议访问 Prowlarr 检查同步状态，访问 Jellyseerr 完成初次向导。"
