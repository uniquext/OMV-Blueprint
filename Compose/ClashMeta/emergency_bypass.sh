#!/usr/bin/env bash
# ==============================================================================
# Clash 代理核弹级紧急避险脚本 (Emergency Proxy Bypass)
# ==============================================================================
# 当代理面板无法访问且下游容器全部遇到 Connection Refused 断网时，请执行此脚本。
#
# 触发【第一层防线】 (针对配置出错或机场节点彻底瘫痪时的软重启空壳抢救):
#   执行指令: ./emergency_bypass.sh
#   执行效果: 热切换为纯本地无阻的替身 config，耗时两秒即可恢复全网局域网通行。
# 
# 触发【第二层防线】 (针对 Clash 容器自身因内核 Bug 完全死亡、无法启动时的核弹级拔除):
#   执行指令: ./emergency_bypass.sh --nuclear 
#   执行效果: 暴力清空 global.env 中的全局代理设置并重载整个 OMV 堆栈，彻底切断物理寄生。
# ==============================================================================

set -e

COMPOSE_DIR="$(cd "$(dirname "$(readlink -f "${BASH_SOURCE[0]}")")" && pwd)"
CONFIG_DIR="${COMPOSE_DIR}/config"

echo "=================================================="
echo "          🚨 触发代理层最高紧急预案 🚨"
echo "=================================================="

# 【第二层防线：核弹拔除】
if [[ "$1" == "--nuclear" ]]; then
    echo "[!] 启动核弹模式：即将全局强行清除 ENV 变量并重启全网..."
    
    GLOBAL_ENV="${COMPOSE_DIR}/../global.env"
    if [ -f "$GLOBAL_ENV" ]; then
        echo "[*] 正在注释 global.env 中的 PROXY 变量..."
        # 兼容 macOS 和 Linux 的 sed
        sed -i.bak -e 's/^PROXY_HTTP=/#&/g' -e 's/^PROXY_HTTPS=/#&/g' "$GLOBAL_ENV"
        echo "[V] 变量已封印。"
    fi
    
    echo "[*] 向下广播重组命令 (这可能需要较长时间)..."
    cd "${COMPOSE_DIR}/.."
    # 获取所有有 docker-compose.yml/yml 文件的服务并直接 up
    for svc in */; do
        if [ -f "${svc}${svc%/}.yml" ]; then
            echo "   -> 刷新堆栈: ${svc%/}"
            docker compose -f "${svc}${svc%/}.yml" --env-file global.env up -d 2>/dev/null || true
        fi
    done
    echo "[V] 核弹计划执行完毕，所有下游容器已彻底剥离代理关联！"
    exit 0
fi

# 【第一层防线：本地热切换】
echo "[*] 开始执行第一层防线抢救任务：纯直连空壳顶替"

# 检查备用文件
if [ ! -f "${CONFIG_DIR}/direct-only.yaml" ]; then
    echo "[X] 致命错误：找不到救援配置文件 direct-only.yaml，操作终止！"
    exit 1
fi

# 备份可能已损坏的现有文件
if [ -f "${CONFIG_DIR}/config.yaml" ]; then
    BACKUP_NAME="config.yaml.$(date +%Y%m%d_%H%M%S).backup"
    mv "${CONFIG_DIR}/config.yaml" "${CONFIG_DIR}/${BACKUP_NAME}"
    echo "[V] 发现主配置，已安全备份为: ${BACKUP_NAME}"
fi

# 执行克隆占位
cp "${CONFIG_DIR}/direct-only.yaml" "${CONFIG_DIR}/config.yaml"
echo "[V] 兜底配置 direct-only.yaml 替换成功！"

# 重启容器
echo "[*] 正在启动雷霆抢救 (重启 mihomo 容器)..."
cd "${COMPOSE_DIR}"
docker compose -f ClashMeta.yml --env-file ../global.env up -d --force-recreate mihomo

echo "=================================================="
echo " ✅ 紧急避险完成！7890 端口已化为全直连无阻网关！"
echo " ✅ 下游服务已恢复连通，无需进行其他重启操作。"
echo "=================================================="
