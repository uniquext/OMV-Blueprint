#!/bin/bash

# ==============================================================================
# Immich 日期修复工具 - 方案一：API 远程更新 (immich_fix_date_api.sh)
# ==============================================================================
# 当前脚本作用：对 Immich 中 EXIF 日期为空的资产，从文件名中解析日期，通过 API 更新 localDateTime。
# 核心逻辑：扫描 Immich 资产 -> 跳过已有 EXIF 日期的资产 -> 从文件名解析日期 -> 列表确认 -> API 批量更新。
# 匹配准则：支持 YYYYMMDD、YYYY-MM-DD、YYYY_MM_DD 及毫秒级 Unix 时间戳等文件名格式。
# 扩展功能：支持指定目标日期（-d），只处理该日期下的资产（无论 EXIF 日期是否为空）。
#
# 用法：bash immich_fix_date_api.sh [-s IP:端口] [-k API_KEY] [-a 相册名] [-d 日期] [--dry-run]
# 参数：
#   -s [IP:端口]       Immich 服务器地址（缺省时交互式输入）
#   -k [API_KEY]       Immich API 密钥（缺省时交互式输入）
#   -a [相册名]        仅处理指定相册内的资产（可选，不指定则扫描全库）
#   -d [日期]          目标日期，格式 YYYY-MM-DD（可选，只处理该日期的资产）
#   --dry-run          仅预览操作，不实际更新（强烈建议首次使用时加上）
# 示例：
#   bash immich_fix_date_api.sh --dry-run -s 192.168.1.100:2283 -k your_api_key
#   bash immich_fix_date_api.sh -s 192.168.1.100:2283 -k your_api_key -a 澄宝宝
#   bash immich_fix_date_api.sh -s 192.168.1.100:2283 -k your_api_key -d 2024-03-16
# 依赖：curl, jq
# ==============================================================================

# --- 解析参数 ---
DRY_RUN=false
IMMICH_HOST=""
API_KEY=""
ALBUM_NAME=""
TARGET_DATE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run) DRY_RUN=true; shift ;;
        -s) IMMICH_HOST="$2"; shift 2 ;;
        -k) API_KEY="$2"; shift 2 ;;
        -a) ALBUM_NAME="$2"; shift 2 ;;
        -d) TARGET_DATE="$2"; shift 2 ;;
        *)
            echo "❌ 未知参数: $1"
            echo "用法: bash immich_fix_date_api.sh [-s IP:端口] [-k API_KEY] [-a 相册名] [-d 日期] [--dry-run]"
            exit 1 ;;
    esac
done

# --- 交互式补全 ---
if [ -z "$IMMICH_HOST" ]; then read -p "[请输入 Immich 地址 (IP:端口)]: " IMMICH_HOST; fi
if [ -z "$API_KEY" ];    then read -s -p "[请输入 Immich API Key]: " API_KEY; echo ""; fi

BASE_URL="http://$IMMICH_HOST/api"

# --- 依赖检查 ---
for cmd in curl jq; do
    if ! command -v $cmd &>/dev/null; then echo "❌ 缺少依赖: $cmd"; exit 1; fi
done

# --- 从文件名解析日期 ---
# 支持格式：
#   ① 20240316 / 2024-03-16 / 2024_03_16 / IMG_20240316_xxx
#   ② 毫秒级 Unix 时间戳（如 mmexport1682570382560、wx_camera_1679739894781）
parse_date_from_filename() {
    local fname="$1"

    # --- ② 尝试匹配毫秒级 Unix 时间戳（13 位数字）并且寄存于 2000 年以后 ---
    if [[ "$fname" =~ [^0-9]?(1[0-9]{12})[^0-9]? ]]; then
        local ts_ms="${BASH_REMATCH[1]}"
        local ts_s=$((ts_ms / 1000))
        # 校验时间戳在合理范围（年份 2000-2035）
        local year
        year=$(date -d "@$ts_s" '+%Y' 2>/dev/null)
        if [ -n "$year" ] && [ "$year" -ge 2000 ] && [ "$year" -le 2035 ]; then
            date -d "@$ts_s" '+%Y-%m-%dT%H:%M:%S.000Z' 2>/dev/null
            return
        fi
    fi

    # --- ① YYYYMMDD[弹性匹配 HHMMSS] ---
    if [[ "$fname" =~ (20[0-9]{2})(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])_?([0-9]{6})? ]]; then
        local y="${BASH_REMATCH[1]}" m="${BASH_REMATCH[2]}" d="${BASH_REMATCH[3]}" t="${BASH_REMATCH[4]}"
        if [ -n "$t" ]; then echo "${y}-${m}-${d}T${t:0:2}:${t:2:2}:${t:4:2}.000Z"
        else echo "${y}-${m}-${d}T00:00:00.000Z"; fi
    # --- YYYY-MM-DD 或 YYYY_MM_DD ---
    elif [[ "$fname" =~ (20[0-9]{2})[-_](0[1-9]|1[0-2])[-_](0[1-9]|[12][0-9]|3[01]) ]]; then
        echo "${BASH_REMATCH[1]}-${BASH_REMATCH[2]}-${BASH_REMATCH[3]}T00:00:00.000Z"
    fi
}

echo ""
echo "============================================================"
echo "🔍 正在扫描 Immich 资产，请稍候..."

# --- 验证目标日期格式（如果提供）---
if [ -n "$TARGET_DATE" ]; then
    if ! [[ "$TARGET_DATE" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]]; then
        echo "❌ 日期格式错误，请使用 YYYY-MM-DD 格式"
        exit 1
    fi
    echo "📅 目标日期筛选: $TARGET_DATE"
fi

# --- 阶段一：扫描并收集待修改项 ---
declare -a ASSET_IDS=()
declare -a ASSET_FNAMES=()
declare -a ASSET_DATES=()
NO_DATE=0
SKIPPED_DATE=0

# 如果指定了相册名，先通过相册 API 查找相册 ID，再拉取相册内资产
if [ -n "$ALBUM_NAME" ]; then
    echo "🔍 正在查找相册「$ALBUM_NAME」..."
    ALBUMS=$(curl -sf "$BASE_URL/albums" \
        -H "x-api-key: $API_KEY" 2>/dev/null)
    if [ -z "$ALBUMS" ]; then
        echo "❌ 获取相册列表失败，请检查地址和 API Key。"
        exit 1
    fi
    ALBUM_ID=$(echo "$ALBUMS" | jq -r --arg name "$ALBUM_NAME" '.[] | select(.albumName == $name) | .id' | head -1)
    if [ -z "$ALBUM_ID" ]; then
        echo "❌ 未找到名为「$ALBUM_NAME」的相册，请确认名称是否完全一致。"
        exit 1
    fi
    echo "  ✅ 找到相册 ID: $ALBUM_ID"
    echo "🔍 正在获取相册资产..."
    RESPONSE=$(curl -sf "$BASE_URL/albums/$ALBUM_ID?withoutAssets=false" \
        -H "x-api-key: $API_KEY" 2>/dev/null)
    if [ -n "$TARGET_DATE" ]; then
        # 如果指定了目标日期，包含所有资产（不管EXIF日期是否为空），但会检查当前日期
        ASSETS_RAW=$(echo "$RESPONSE" | jq -c '.assets[] | {id: .id, name: .originalFileName, dateTimeOriginal: .exifInfo.dateTimeOriginal}')
    else
        # 未指定目标日期，只处理EXIF日期为空的资产
        ASSETS_RAW=$(echo "$RESPONSE" | jq -c '.assets[] | select(.exifInfo.dateTimeOriginal == null or .exifInfo.dateTimeOriginal == "") | {id: .id, name: .originalFileName, dateTimeOriginal: .exifInfo.dateTimeOriginal}')
    fi
else
    # 未指定相册：分页拉取全库
    echo "🔍 未指定相册，正在扫描全库资产..."
    ASSETS_RAW=""
    PAGE=1

    while true; do
        RESPONSE=$(curl -sf -X POST "$BASE_URL/search/metadata" \
            -H "x-api-key: $API_KEY" \
            -H "Content-Type: application/json" \
            -d "{\"page\": $PAGE, \"size\": 500}" 2>/dev/null)
        if [ -z "$RESPONSE" ]; then
            echo "❌ API 请求失败，请检查地址 ($BASE_URL) 和 API Key 是否正确。"
            exit 1
        fi
        if [ -n "$TARGET_DATE" ]; then
            # 如果指定了目标日期，包含所有资产（不管EXIF日期是否为空），但会检查当前日期
            PAGE_ASSETS=$(echo "$RESPONSE" | jq -c '.assets.items[] | {id: .id, name: .originalFileName, dateTimeOriginal: .exifInfo.dateTimeOriginal}')
        else
            # 未指定目标日期，只处理EXIF日期为空的资产
            PAGE_ASSETS=$(echo "$RESPONSE" | jq -c '.assets.items[] | select(.exifInfo.dateTimeOriginal == null or .exifInfo.dateTimeOriginal == "") | {id: .id, name: .originalFileName, dateTimeOriginal: .exifInfo.dateTimeOriginal}')
        fi
        ASSETS_RAW+="$PAGE_ASSETS"$'\n'
        NEXT_PAGE=$(echo "$RESPONSE" | jq '.assets.nextPage')
        if [ "$NEXT_PAGE" = "null" ] || [ -z "$NEXT_PAGE" ]; then break; fi
        ((PAGE++))
    done
fi

# --- 解析并分类资产 ---
while IFS= read -r asset; do
    [ -z "$asset" ] && continue
    ASSET_ID=$(echo "$asset" | jq -r '.id')
    FNAME=$(echo "$asset" | jq -r '.name')
    CURRENT_DATE=$(echo "$asset" | jq -r '.dateTimeOriginal // empty')
    
    # 如果指定了目标日期，检查当前资产日期是否匹配
    if [ -n "$TARGET_DATE" ]; then
        if [ -n "$CURRENT_DATE" ]; then
            # 提取日期部分（YYYY-MM-DD）进行比较
            CURRENT_DATE_ONLY=$(echo "$CURRENT_DATE" | cut -d'T' -f1)
            if [ "$CURRENT_DATE_ONLY" != "$TARGET_DATE" ]; then
                ((SKIPPED_DATE++))
                continue
            fi
        else
            # 如果资产没有日期信息，跳过（除非要处理无日期的资产）
            ((SKIPPED_DATE++))
            continue
        fi
    fi

    PARSED=$(parse_date_from_filename "$FNAME")
    if [ -z "$PARSED" ]; then
        ((NO_DATE++))
    else
        ASSET_IDS+=("$ASSET_ID")
        ASSET_FNAMES+=("$FNAME")
        ASSET_DATES+=("$PARSED")
    fi
done <<< "$ASSETS_RAW"


TOTAL=${#ASSET_IDS[@]}

if [ "$TOTAL" -eq 0 ]; then
    if [ -n "$TARGET_DATE" ] && [ "$SKIPPED_DATE" -gt 0 ]; then
        echo "✅ 未发现目标日期 ($TARGET_DATE) 下可从文件名解析日期的资产。（日期不匹配: $SKIPPED_DATE 条，无法解析文件名: $NO_DATE 条）"
    else
        echo "✅ 未发现可从文件名解析日期的资产。（无法解析文件名: $NO_DATE 条）"
    fi
    exit 0
fi

# --- 阶段二：列出待修改项，等待确认 ---
if [ -n "$TARGET_DATE" ] && [ "$SKIPPED_DATE" -gt 0 ]; then
    echo "📋 以下 $TOTAL 条资产将被更新（目标日期: $TARGET_DATE，日期不匹配: $SKIPPED_DATE 条，无法解析文件名: $NO_DATE 条）："
else
    echo "📋 以下 $TOTAL 条资产将被更新（无法解析文件名: $NO_DATE 条）："
fi
echo ""
printf "  %-4s  %-55s  %s\n" "#" "文件名" "解析日期"
printf "  %-4s  %-55s  %s\n" "----" "-------------------------------------------------------" "-------------------"
for i in "${!ASSET_IDS[@]}"; do
    printf "  %-4s  %-55s  →  %s\n" "[$((i+1))]" "${ASSET_FNAMES[$i]}" "${ASSET_DATES[$i]}"
done
echo ""

if $DRY_RUN; then
    echo "⚠️  [DRY-RUN 模式] 仅预览，不执行实际更新。"
    echo "    确认无误后，去掉 --dry-run 参数重新运行以实际更新。"
    exit 0
fi

read -p "是否确认更新以上 $TOTAL 条资产? (y/n): " confirm
if [[ "$confirm" != [yY] && "$confirm" != [yY][eE][sS] ]]; then
    echo "❌ 已取消。"
    exit 0
fi

# --- 阶段三：执行更新 ---
echo ""
echo "🚀 开始更新..."
UPDATED=0
for i in "${!ASSET_IDS[@]}"; do
    curl -sf -X PUT "$BASE_URL/assets/${ASSET_IDS[$i]}" \
        -H "x-api-key: $API_KEY" \
        -H "Content-Type: application/json" \
        -d "{\"dateTimeOriginal\": \"${ASSET_DATES[$i]}\"}" > /dev/null
    echo "  ✅ [${ASSET_FNAMES[$i]}] → ${ASSET_DATES[$i]}"
    ((UPDATED++))
done

echo "============================================================"
if [ -n "$TARGET_DATE" ] && [ "$SKIPPED_DATE" -gt 0 ]; then
    echo "✅ 完成！已更新: $UPDATED 条，日期不匹配: $SKIPPED_DATE 条，无法从文件名解析: $NO_DATE 条。"
else
    echo "✅ 完成！已更新: $UPDATED 条，无法从文件名解析: $NO_DATE 条。"
fi
