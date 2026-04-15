#!/bin/bash

# ==============================================================================
# Servarr 脚本公共函数库 (common.sh)
# ==============================================================================

# 颜色定义
CHECK="\033[32m[✓]\033[0m"
ERROR="\033[31m[✗]\033[0m"
INFO="\033[34m[i]\033[0m"
WARN="\033[33m[!]\033[0m"

# 日志输出
log_info() { echo -e "$INFO $1"; }
log_success() { echo -e "$CHECK $1"; }
log_error() { echo -e "$ERROR $1"; }
log_warn() { echo -e "$WARN $1"; }

# 确保环境变量已加载
load_env() {
    local env_file="$1"
    if [ -f "$env_file" ]; then
        source "$env_file"
    else
        log_error "找不到环境变量文件: $env_file"
        exit 1
    fi
}

# 注销宿主机层代理变量 (仅影响当前 Shell 进程，不影响容器内环境)
# 原因：宿主机 root 可能配置了全局代理，导致脚本中发往 localhost 的
# curl/urllib 请求被代理拦截，返回 502 Bad Gateway
unset_host_proxy() {
    unset http_proxy https_proxy HTTP_PROXY HTTPS_PROXY
    export no_proxy="localhost,127.0.0.1"
    export NO_PROXY="localhost,127.0.0.1"
}

# 等待 API 响应
wait_for_service() {
    local name=$1
    local port=$2
    local timeout=${3:-30}
    echo -ne "$INFO 等待 $name ($port) 响应... "
    for i in $(seq 1 "$timeout"); do
        if curl -s --noproxy "*" -m 2 "http://localhost:$port" > /dev/null; then
            echo -e "$CHECK 正常"
            return 0
        fi
        sleep 2
    done
    echo -e "$ERROR 超时"
    return 1
}

# 提取 API Key
get_api_key() {
    local service=$1
    local xml_path="$SCRIPT_DIR/config/$service/config.xml"
    if [ -f "$xml_path" ]; then
        local key=$(grep "<ApiKey>" "$xml_path" | sed 's/.*<ApiKey>\(.*\)<\/ApiKey>.*/\1/')
        if [ -n "$key" ]; then
            echo "$key"
            return 0
        fi
    fi
    return 1
}

# Python API 调用辅助函数
run_python_api() {
    python3 - "$@"
}
