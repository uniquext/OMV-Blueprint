#!/bin/bash

# ==============================================================================
# OMV 系统级环境变量注入脚本 (inject.sh)
# ==============================================================================
# 功能：将指定的 .env 变量注入到系统全局环境及 root 环境。
#
# 特性：
#   - 幂等设计：支持反复多次运行，自动覆盖旧记录，不产生重复行。
#   - 全量注入：自动解析 .env 中所有合法 KEY=VALUE 变量。
#   - 安全加固：使用单引号包裹变量值，防止 $ 等特殊字符被 Shell 误解析。
#   - 系统全局：同步注入 /etc/environment，确保服务和 Cron 任务均能读取。
#
# 使用方式：
#   sudo bash inject.sh private.env
# ==============================================================================

set -euo pipefail

# ─── 颜色定义 ─────────────────────────────────────────────────────────────────
GREEN="\033[32m"; RED="\033[31m"; BLUE="\033[34m"; RESET="\033[0m"
CHECK="${GREEN}[✓]${RESET}"; ERROR="${RED}[✗]${RESET}"; INFO="${BLUE}[i]${RESET}"

# ─── 参数校验 ─────────────────────────────────────────────────────────────────
if [ $# -lt 1 ]; then
    echo -e "${ERROR} 错误: 请提供配置文件路径。"
    echo -e "${INFO} 用法: sudo bash inject.sh private.env"
    exit 1
fi

ENV_FILE="$1"

if [ ! -f "$ENV_FILE" ]; then
    echo -e "${ERROR} 配置文件不存在: $ENV_FILE"
    exit 1
fi

# ─── 解析私有变量 ─────────────────────────────────────────────────────────────
echo -e "${INFO} 正在解析变量: $ENV_FILE ..."

declare -A SECRETS
while IFS= read -r line || [[ -n "$line" ]]; do
    [[ "$line" =~ ^[[:space:]]*# ]] && continue
    [[ -z "${line// }" ]] && continue
    
    if [[ "$line" =~ ^([A-Za-z_][A-Za-z0-9_]*)=(.*)$ ]]; then
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"
        
        # 清理值的引号
        value="${value%\"}"
        value="${value#\"}"
        value="${value%\'}"
        value="${value#\'}"
        
        SECRETS["$key"]="$value"
    fi
done < "$ENV_FILE"

if [ ${#SECRETS[@]} -eq 0 ]; then
    echo -e "${ERROR} 未找到有效变量。"
    exit 1
fi

# ─── 注入目标定义 ─────────────────────────────────────────────────────────────
MARKER_START="# >>> OMV SYSTEM SECRETS (inject.sh 自动生成) >>>"
MARKER_END="# <<< OMV SYSTEM SECRETS <<<"

inject_to_file() {
    local target_file=$1
    local mode=$2 # "env" (KEY=VAL) 或 "bash" (export KEY='VAL')
    
    echo -e "${INFO} 正在处理: $target_file ..."
    
    # 1. 清理旧块
    sed -i "/$MARKER_START/,/$MARKER_END/d" "$target_file"
    
    # 2. 构造新内容
    {
        echo "$MARKER_START"
        for key in $(echo "${!SECRETS[@]}" | tr ' ' '\n' | sort); do
            if [ "$mode" == "bash" ]; then
                echo "export ${key}='${SECRETS[$key]}'"
            else
                # /etc/environment 通常不使用 export 关键字
                echo "${key}=\"${SECRETS[$key]}\""
            fi
        done
        echo "$MARKER_END"
    } >> "$target_file"
}

# ─── 执行注入 ─────────────────────────────────────────────────────────────────
# 注入到系统全局环境
inject_to_file "/etc/environment" "env"

# 注入到 root 的 bash 配置
inject_to_file "/root/.bashrc" "bash"

# ─── 完成 ────────────────────────────────────────────────────────────────────
echo -e "${CHECK} 注入成功！已同步 ${#SECRETS[@]} 个变量至系统级环境。"
echo ""
echo -e "${GREEN}✨ 操作完成！${RESET}"
echo -e "${INFO} 立即生效 root 环境环境："
echo -e "    ${BLUE}source /root/.bashrc${RESET}"
echo -e "${INFO} 立即生效系统全局环境："
echo -e "    ${BLUE}请执行: source /etc/environment (或重启系统/服务)${RESET}"
