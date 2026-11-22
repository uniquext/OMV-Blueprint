#!/bin/bash

# ==============================================================================
# Immich 日期修复工具 V2 - 多相册批量 API 更新 (immich_fix_date_v2.sh)
# ==============================================================================
# 功能：对 Immich 中指定相册的非 mmexport 资产，从文件名解析 CST 日期，
#       与 localDateTime 对比，差异超过阈值的通过 API 更新 dateTimeOriginal。
# 附加：将 mmexport 和无法解析日期的文件归档到新建相册，便于后续手动处理。
#
# 用法：bash immich_fix_date_v2.sh [参数]
# 参数：
#   -s [IP:端口]       Immich 服务器地址（缺省时交互式输入）
#   -k [API_KEY]       Immich API 密钥（缺省时交互式输入）
#   -a [相册名]        逗号分隔的多个相册名（不指定则扫描全库）
#   --min-diff [秒]    最小差异阈值，低于此值跳过（默认 300 秒）
#   --dry-run          仅预览操作，不实际更新
#   --no-confirm       跳过交互确认（SSH 非交互模式用）
#   --log [路径]       CSV 日志输出路径
#   --no-archive       不创建归档相册
# 示例：
#   bash immich_fix_date_v2.sh --dry-run -s 192.168.1.10:2283 -k YOUR_KEY -a "澄宝宝,双排"
#   bash immich_fix_date_v2.sh -s localhost:2283 -k KEY -a "澄宝宝,双排,唐糖糖,汤圆,旅行,随手拍" --min-diff 300 --log /tmp/fix.csv
# 依赖：curl, jq, date (GNU coreutils)
# ==============================================================================

# 注意：不使用 set -e，因为 bash 算术运算结果为0会触发 errexit
# 不使用 set -u，因为空数组访问在某些 bash 版本下报错
set -o pipefail

# --- 参数解析 ---
DRY_RUN=false
NO_CONFIRM=false
NO_ARCHIVE=false
IMMICH_HOST=""
API_KEY=""
ALBUM_NAMES=""
MIN_DIFF=300
LOG_FILE=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)    DRY_RUN=true; shift ;;
        --no-confirm) NO_CONFIRM=true; shift ;;
        --no-archive) NO_ARCHIVE=true; shift ;;
        -s) IMMICH_HOST="$2"; shift 2 ;;
        -k) API_KEY="$2"; shift 2 ;;
        -a) ALBUM_NAMES="$2"; shift 2 ;;
        --min-diff) MIN_DIFF="$2"; shift 2 ;;
        --log) LOG_FILE="$2"; shift 2 ;;
        *)
            echo "❌ 未知参数: $1"
            echo "用法: bash immich_fix_date_v2.sh [-s IP:端口] [-k API_KEY] [-a 相册名] [--min-diff 秒] [--dry-run] [--log 路径]"
            exit 1 ;;
    esac
done

# --- 交互式补全 ---
if [ -z "$IMMICH_HOST" ]; then read -p "[请输入 Immich 地址 (IP:端口)]: " IMMICH_HOST; fi
if [ -z "$API_KEY" ];    then read -s -p "[请输入 Immich API Key]: " API_KEY; echo ""; fi

BASE_URL="http://$IMMICH_HOST/api"

# --- 依赖检查 ---
for cmd in curl jq date; do
    if ! command -v $cmd &>/dev/null; then echo "❌ 缺少依赖: $cmd"; exit 1; fi
done

# --- 连接测试 ---
SERVER_VER=$(curl -sf "$BASE_URL/server/version" -H "x-api-key: $API_KEY" 2>/dev/null)
if [ -z "$SERVER_VER" ]; then
    echo "❌ 无法连接 Immich 服务器 ($BASE_URL)，请检查地址和 API Key。"
    exit 1
fi
echo "✅ Immich 连接成功: $(echo "$SERVER_VER" | jq -r '"v\(.major).\(.minor).\(.patch)"')"

# ==============================================================================
# 函数定义
# ==============================================================================

# --- 从文件名解析 CST 日期时间，返回 "YYYY-MM-DD HH:MM:SS" ---
# 优先级：A类(YYYYMMDD) > B类(Unix时间戳)
# 防止 _edit_2108184787954355 等后缀中的长数字被误匹配为时间戳
parse_date_from_filename() {
    local fname="$1"

    # A 类优先：YYYYMMDD[_]HHMMSS（含 MEITU/VID 毫秒后缀变体）
    if [[ "$fname" =~ (20[0-9]{2})(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])[_]?([0-9]{2})([0-9]{2})([0-9]{2}) ]]; then
        local y="${BASH_REMATCH[1]}" m="${BASH_REMATCH[2]}" d="${BASH_REMATCH[3]}"
        local H="${BASH_REMATCH[4]}" M="${BASH_REMATCH[5]}" S="${BASH_REMATCH[6]}"
        echo "${y}-${m}-${d} ${H}:${M}:${S}"
        return
    fi

    # A 类降级：仅日期无时间（YYYY-MM-DD / YYYY_MM_DD）
    if [[ "$fname" =~ (20[0-9]{2})[-_](0[1-9]|1[0-2])[-_](0[1-9]|[12][0-9]|3[01]) ]]; then
        echo "${BASH_REMATCH[1]}-${BASH_REMATCH[2]}-${BASH_REMATCH[3]} 00:00:00"
        return
    fi

    # B 类兜底：Unix 毫秒时间戳（wx_camera_1784977009673 等）
    if [[ "$fname" =~ [^0-9]?(1[0-9]{12})[^0-9]? ]]; then
        local ts_ms="${BASH_REMATCH[1]}"
        local ts_s=$((ts_ms / 1000))
        local year
        year=$(date -d "@$ts_s" '+%Y' 2>/dev/null)
        if [ -n "$year" ] && [ "$year" -ge 2000 ] && [ "$year" -le 2035 ]; then
            # 宿主机已是 CST，date 直接输出本地时间
            date -d "@$ts_s" '+%Y-%m-%d %H:%M:%S' 2>/dev/null
            return
        fi
    fi
}

# --- 从 localDateTime 提取纯日期时间字符串（去掉时区和毫秒）---
strip_local_datetime() {
    echo "$1" | sed 's/T/ /;s/[.][0-9]*//;s/[+].*//;s/Z$//'
}

# --- 计算两个日期时间字符串的差异（秒）---
compute_diff_seconds() {
    local dt1="$1" dt2="$2"
    local ep1 ep2
    ep1=$(date -d "$dt1" '+%s' 2>/dev/null) || return 1
    ep2=$(date -d "$dt2" '+%s' 2>/dev/null) || return 1
    local diff=$((ep1 - ep2))
    echo "${diff#-}"  # 取绝对值
}

# --- 创建相册（若已存在则返回现有 ID）---
create_or_get_album() {
    local album_name="$1"
    local existing_id
    existing_id=$(curl -sf "$BASE_URL/albums" -H "x-api-key: $API_KEY" | \
        jq -r --arg name "$album_name" '.[] | select(.albumName == $name) | .id' | head -1)
    if [ -n "$existing_id" ]; then
        echo "$existing_id"
        return
    fi
    curl -sf -X POST "$BASE_URL/albums" \
        -H "x-api-key: $API_KEY" \
        -H "Content-Type: application/json" \
        -d "{\"albumName\": \"$album_name\"}" | jq -r '.id'
}

# --- 批量添加资产到相册（每批 100 个）---
add_assets_to_album() {
    local album_id="$1"
    shift
    local ids=("$@")
    local batch_size=100
    local total=${#ids[@]}
    for ((i=0; i<total; i+=batch_size)); do
        local batch=("${ids[@]:i:batch_size}")
        local json_ids=""
        for id in "${batch[@]}"; do
            [ -n "$json_ids" ] && json_ids+=","
            json_ids+="\"$id\""
        done
        curl -sf -X PUT "$BASE_URL/albums/$album_id/assets" \
            -H "x-api-key: $API_KEY" \
            -H "Content-Type: application/json" \
            -d "{\"ids\": [$json_ids]}" > /dev/null
    done
}

# ==============================================================================
# 主流程
# ==============================================================================

echo ""
echo "============================================================"
echo "🔍 Immich 日期修复工具 V2"
echo "   差异阈值: ${MIN_DIFF}s | Dry-Run: $DRY_RUN"
echo "============================================================"

# --- 初始化 CSV 日志 ---
if [ -n "$LOG_FILE" ]; then
    echo "相册,资产ID,文件名,旧dateTimeOriginal,新dateTimeOriginal,差异秒数,操作" > "$LOG_FILE"
fi

# --- 解析相册列表 ---
declare -a ALBUM_ID_LIST=()
declare -a ALBUM_NAME_LIST=()

if [ -n "$ALBUM_NAMES" ]; then
    IFS=',' read -ra NAMES <<< "$ALBUM_NAMES"
    ALL_ALBUMS=$(curl -sf "$BASE_URL/albums" -H "x-api-key: $API_KEY")
    for name in "${NAMES[@]}"; do
        name=$(echo "$name" | xargs)  # 去除前后空格
        aid=$(echo "$ALL_ALBUMS" | jq -r --arg n "$name" '.[] | select(.albumName == $n) | .id' | head -1)
        if [ -z "$aid" ]; then
            echo "⚠️  未找到相册「$name」，跳过。"
            continue
        fi
        ALBUM_ID_LIST+=("$aid")
        ALBUM_NAME_LIST+=("$name")
    done
else
    echo "❌ 必须通过 -a 参数指定至少一个相册名。"
    exit 1
fi

if [ ${#ALBUM_ID_LIST[@]} -eq 0 ]; then
    echo "❌ 没有有效的相册可处理。"
    exit 1
fi

echo "📁 待处理相册: ${ALBUM_NAME_LIST[*]}"
echo ""

# --- 全局收集器（去重）---
declare -A SEEN_ASSETS=()            # 去重用
declare -a FIX_IDS=()                # 需要修复的资产 ID
declare -a FIX_NAMES=()              # 文件名
declare -a FIX_OLD_DATES=()          # 旧 dateTimeOriginal
declare -a FIX_NEW_DATES=()          # 新 dateTimeOriginal（CST+08:00）
declare -a FIX_DIFFS=()              # 差异秒数
declare -a FIX_ALBUMS=()             # 所属相册名
declare -a MMEXPORT_IDS=()           # mmexport 资产 ID
declare -a MMEXPORT_NAMES=()         # mmexport 文件名
declare -a UNPARSEABLE_IDS=()        # 无法解析的资产 ID
declare -a UNPARSEABLE_NAMES=()      # 无法解析的文件名

TOTAL_SCANNED=0
SKIPPED_MMEXPORT=0
SKIPPED_SMALL_DIFF=0
SKIPPED_NO_TS=0
SKIPPED_NO_EXIF=0

# --- 逐相册扫描 ---
for idx in "${!ALBUM_ID_LIST[@]}"; do
    ALBUM_ID="${ALBUM_ID_LIST[$idx]}"
    ALBUM_NAME="${ALBUM_NAME_LIST[$idx]}"

    echo "🔍 正在扫描相册「$ALBUM_NAME」..."
    RESPONSE=$(curl -sf "$BASE_URL/albums/$ALBUM_ID?withoutAssets=false" \
        -H "x-api-key: $API_KEY" 2>/dev/null)
    if [ -z "$RESPONSE" ]; then
        echo "  ⚠️  获取相册数据失败，跳过。"
        continue
    fi

    ASSETS_JSON=$(echo "$RESPONSE" | jq -c '.assets[]')
    ALBUM_COUNT=0
    ALBUM_FIX=0

    while IFS= read -r asset; do
        [ -z "$asset" ] && continue
        ASSET_ID=$(echo "$asset" | jq -r '.id')

        # 去重：同一资产可能出现在多个相册中
        if [[ -n "${SEEN_ASSETS[$ASSET_ID]+x}" ]]; then continue; fi
        SEEN_ASSETS[$ASSET_ID]=1
        ALBUM_COUNT=$((ALBUM_COUNT + 1))
        TOTAL_SCANNED=$((TOTAL_SCANNED + 1))

        FNAME=$(echo "$asset" | jq -r '.originalFileName')
        EXIF_DATE=$(echo "$asset" | jq -r '.exifInfo.dateTimeOriginal // empty')
        LOCAL_DT=$(echo "$asset" | jq -r '.localDateTime // empty')

        # 1. 过滤 mmexport 前缀
        if [[ "$FNAME" == mmexport* ]]; then
            SKIPPED_MMEXPORT=$((SKIPPED_MMEXPORT + 1))
            MMEXPORT_IDS+=("$ASSET_ID")
            MMEXPORT_NAMES+=("$FNAME")
            continue
        fi

        # 2. 尝试从文件名解析日期
        PARSED=$(parse_date_from_filename "$FNAME")
        if [ -z "$PARSED" ]; then
            SKIPPED_NO_TS=$((SKIPPED_NO_TS + 1))
            UNPARSEABLE_IDS+=("$ASSET_ID")
            UNPARSEABLE_NAMES+=("$FNAME")
            continue
        fi

        # 3. 跳过无 EXIF 日期的（理论上不存在，但防御）
        if [ -z "$EXIF_DATE" ] || [ -z "$LOCAL_DT" ]; then
            SKIPPED_NO_EXIF=$((SKIPPED_NO_EXIF + 1))
            continue
        fi

        # 4. 计算差异：文件名CST时间 vs localDateTime 的显示时间
        LOCAL_STRIPPED=$(strip_local_datetime "$LOCAL_DT")
        DIFF=$(compute_diff_seconds "$PARSED" "$LOCAL_STRIPPED" 2>/dev/null)
        if [ -z "$DIFF" ]; then continue; fi

        # 5. 差异低于阈值则跳过
        if [ "$DIFF" -lt "$MIN_DIFF" ]; then
            SKIPPED_SMALL_DIFF=$((SKIPPED_SMALL_DIFF + 1))
            continue
        fi

        # 6. 收集需要修复的资产
        NEW_DATE="${PARSED// /T}+08:00"
        FIX_IDS+=("$ASSET_ID")
        FIX_NAMES+=("$FNAME")
        FIX_OLD_DATES+=("$EXIF_DATE")
        FIX_NEW_DATES+=("$NEW_DATE")
        FIX_DIFFS+=("$DIFF")
        FIX_ALBUMS+=("$ALBUM_NAME")
        ALBUM_FIX=$((ALBUM_FIX + 1))

    done <<< "$ASSETS_JSON"

    echo "  📊 扫描 $ALBUM_COUNT 条 | 需修复 $ALBUM_FIX 条"
done

# ==============================================================================
# 预览报告
# ==============================================================================

TOTAL_FIX=${#FIX_IDS[@]}
TOTAL_MMEXPORT=${#MMEXPORT_IDS[@]}
TOTAL_UNPARSEABLE=${#UNPARSEABLE_IDS[@]}

echo ""
echo "============================================================"
echo "📋 扫描汇总"
echo "============================================================"
echo "  总扫描资产:       $TOTAL_SCANNED"
echo "  ─────────────────────────────"
echo "  🔴 需修复 (差异≥${MIN_DIFF}s):  $TOTAL_FIX"
echo "  🟡 mmexport (归档):     $TOTAL_MMEXPORT"
echo "  🟡 无法解析文件名 (归档): $TOTAL_UNPARSEABLE"
echo "  🟢 差异<${MIN_DIFF}s (跳过):    $SKIPPED_SMALL_DIFF"
echo "  🟢 无EXIF日期 (跳过):    $SKIPPED_NO_EXIF"
echo "============================================================"

# --- 列出需修复的资产 ---
if [ "$TOTAL_FIX" -gt 0 ]; then
    echo ""
    echo "📋 以下 $TOTAL_FIX 条资产将被更新 dateTimeOriginal："
    echo ""
    printf "  %-4s  %-10s  %-50s  %-12s  %s\n" "#" "相册" "文件名" "差异" "修改"
    printf "  %-4s  %-10s  %-50s  %-12s  %s\n" "----" "----------" "--------------------------------------------------" "------------" "-----------------------------------"
    for i in "${!FIX_IDS[@]}"; do
        local_old=$(strip_local_datetime "${FIX_OLD_DATES[$i]}")
        local_new="${FIX_NEW_DATES[$i]}"
        diff_h=$(echo "${FIX_DIFFS[$i]}" | awk '{printf "%.1fh", $1/3600}')
        printf "  %-4s  %-10s  %-50s  %-12s  %s → %s\n" \
            "[$((i+1))]" "${FIX_ALBUMS[$i]}" "${FIX_NAMES[$i]}" "$diff_h (${FIX_DIFFS[$i]}s)" \
            "$local_old" "$local_new"
    done
fi

# --- 列出无法解析的文件 ---
if [ "$TOTAL_UNPARSEABLE" -gt 0 ]; then
    echo ""
    echo "📋 无法解析文件名的资产 ($TOTAL_UNPARSEABLE 条)："
    for i in "${!UNPARSEABLE_IDS[@]}"; do
        echo "  ⏭  ${UNPARSEABLE_NAMES[$i]}"
    done
fi

# --- Dry-Run 退出 ---
if $DRY_RUN; then
    echo ""
    echo "⚠️  [DRY-RUN 模式] 仅预览，不执行实际更新。"
    echo "    确认无误后，去掉 --dry-run 参数重新运行以实际执行。"
    # 即使 dry-run 也写日志（标记为 dry-run）
    if [ -n "$LOG_FILE" ] && [ "$TOTAL_FIX" -gt 0 ]; then
        for i in "${!FIX_IDS[@]}"; do
            echo "${FIX_ALBUMS[$i]},${FIX_IDS[$i]},${FIX_NAMES[$i]},${FIX_OLD_DATES[$i]},${FIX_NEW_DATES[$i]},${FIX_DIFFS[$i]},dry-run" >> "$LOG_FILE"
        done
        echo "    日志已写入: $LOG_FILE"
    fi
    exit 0
fi

# --- 确认执行 ---
if [ "$TOTAL_FIX" -eq 0 ] && [ "$TOTAL_MMEXPORT" -eq 0 ] && [ "$TOTAL_UNPARSEABLE" -eq 0 ]; then
    echo ""
    echo "✅ 没有需要处理的资产。"
    exit 0
fi

if ! $NO_CONFIRM; then
    echo ""
    read -p "是否确认执行以上操作? (y/n): " confirm
    if [[ "$confirm" != [yY] && "$confirm" != [yY][eE][sS] ]]; then
        echo "❌ 已取消。"
        exit 0
    fi
fi

# ==============================================================================
# 执行阶段
# ==============================================================================

# --- 1. 执行日期修复 ---
if [ "$TOTAL_FIX" -gt 0 ]; then
    echo ""
    echo "🚀 开始更新 dateTimeOriginal..."
    UPDATED=0
    FAILED=0
    for i in "${!FIX_IDS[@]}"; do
        HTTP_CODE=$(curl -sf -o /dev/null -w "%{http_code}" \
            -X PUT "$BASE_URL/assets/${FIX_IDS[$i]}" \
            -H "x-api-key: $API_KEY" \
            -H "Content-Type: application/json" \
            -d "{\"dateTimeOriginal\": \"${FIX_NEW_DATES[$i]}\"}")

        if [ "$HTTP_CODE" = "200" ]; then
            echo "  ✅ [${FIX_NAMES[$i]}] → ${FIX_NEW_DATES[$i]}"
            UPDATED=$((UPDATED + 1))
            ACTION="updated"
        else
            echo "  ❌ [${FIX_NAMES[$i]}] HTTP $HTTP_CODE"
            FAILED=$((FAILED + 1))
            ACTION="failed:$HTTP_CODE"
        fi

        # 写入 CSV 日志
        if [ -n "$LOG_FILE" ]; then
            echo "${FIX_ALBUMS[$i]},${FIX_IDS[$i]},${FIX_NAMES[$i]},${FIX_OLD_DATES[$i]},${FIX_NEW_DATES[$i]},${FIX_DIFFS[$i]},$ACTION" >> "$LOG_FILE"
        fi
    done
    echo "  ────────────────────────────"
    echo "  更新成功: $UPDATED | 失败: $FAILED"
fi

# --- 2. 创建归档相册并添加资产 ---
if ! $NO_ARCHIVE; then
    if [ "$TOTAL_MMEXPORT" -gt 0 ]; then
        echo ""
        echo "📁 创建归档相册「mmexport待处理」..."
        MMEXPORT_ALBUM_ID=$(create_or_get_album "mmexport待处理")
        if [ -n "$MMEXPORT_ALBUM_ID" ]; then
            echo "  相册 ID: $MMEXPORT_ALBUM_ID"
            echo "  添加 $TOTAL_MMEXPORT 个资产..."
            add_assets_to_album "$MMEXPORT_ALBUM_ID" "${MMEXPORT_IDS[@]}"
            echo "  ✅ 已添加 $TOTAL_MMEXPORT 个 mmexport 资产到归档相册"
        else
            echo "  ❌ 创建相册失败"
        fi
    fi

    if [ "$TOTAL_UNPARSEABLE" -gt 0 ]; then
        echo ""
        echo "📁 创建归档相册「无法解析待处理」..."
        UNPARSEABLE_ALBUM_ID=$(create_or_get_album "无法解析待处理")
        if [ -n "$UNPARSEABLE_ALBUM_ID" ]; then
            echo "  相册 ID: $UNPARSEABLE_ALBUM_ID"
            echo "  添加 $TOTAL_UNPARSEABLE 个资产..."
            add_assets_to_album "$UNPARSEABLE_ALBUM_ID" "${UNPARSEABLE_IDS[@]}"
            echo "  ✅ 已添加 $TOTAL_UNPARSEABLE 个无法解析资产到归档相册"
        else
            echo "  ❌ 创建相册失败"
        fi
    fi
fi

# ==============================================================================
# 最终统计
# ==============================================================================
echo ""
echo "============================================================"
echo "✅ 执行完成！"
echo "   日期修复: ${UPDATED:-0} 条更新 | ${FAILED:-0} 条失败"
echo "   mmexport 归档: $TOTAL_MMEXPORT 条"
echo "   无法解析归档: $TOTAL_UNPARSEABLE 条"
if [ -n "$LOG_FILE" ]; then
    echo "   日志文件: $LOG_FILE"
fi
echo "============================================================"
