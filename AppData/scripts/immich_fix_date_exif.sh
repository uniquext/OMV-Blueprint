#!/bin/bash

# ==============================================================================
# Immich 日期修复工具 - 方案二：EXIF 本地写入 (immich_fix_date_exif.sh)
# ==============================================================================
# 当前脚本作用：对本地目录中没有 DateTimeOriginal 的文件，从文件名解析日期，通过 exiftool 写入 EXIF。
# 核心逻辑：扫描目录 -> 跳过已有 EXIF 日期的文件 -> 从文件名解析日期 -> 列表确认 -> 批量写入。
# 匹配准则：支持 YYYYMMDD、YYYY-MM-DD、YYYY_MM_DD 及毫秒级 Unix 时间戳等文件名格式。
# 写入策略：使用 -P 参数保留文件系统时间，默认保留 _original 备份文件。
#
# 用法：bash immich_fix_date_exif.sh [-d 目录路径] [--dry-run] [--no-backup]
# 参数：
#   -d [目录路径]      待处理的照片目录路径（缺省时交互式输入）
#   --dry-run          仅预览操作，不实际写入（强烈建议首次使用时加上）
#   --no-backup        不保留 exiftool 的 _original 备份文件
# 示例：
#   bash immich_fix_date_exif.sh --dry-run -d /mnt/Cache/DCIM
#   bash immich_fix_date_exif.sh -d /mnt/Cache/DCIM --no-backup
# 依赖：exiftool
# ==============================================================================

# --- 解析参数 ---
DRY_RUN=false
NO_BACKUP=false
TARGET_DIR=""

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)   DRY_RUN=true;   shift ;;
        --no-backup) NO_BACKUP=true; shift ;;
        -d) TARGET_DIR="$2"; shift 2 ;;
        *)
            echo "❌ 未知参数: $1"
            echo "用法: bash immich_fix_date_exif.sh [-d 目录路径] [--dry-run] [--no-backup]"
            exit 1 ;;
    esac
done

# --- 交互式补全 ---
if [ -z "$TARGET_DIR" ]; then
    read -p "[请输入待处理的照片目录路径]: " TARGET_DIR
fi

# --- 校验目录是否存在 ---
if [ ! -d "$TARGET_DIR" ]; then
    echo "❌ 找不到目录: $TARGET_DIR"
    exit 1
fi

# --- 依赖检查 ---
if ! command -v exiftool &>/dev/null; then
    echo "❌ 缺少依赖: exiftool"
    echo "  安装方式: apt install libimage-exiftool-perl  (Debian/Ubuntu)"
    echo "            brew install exiftool               (macOS)"
    exit 1
fi

# --- 从文件名解析日期（exiftool 格式：YYYY:MM:DD HH:MM:SS）---
# 支持格式：
#   ① 20240316 / 2024-03-16 / 2024_03_16 / IMG_20240316_xxx / MTXX_MH20230814_175211138
#   ② 毫秒级 Unix 时间戳（如 mmexport1682570382560、wx_camera_1679739894781）
parse_date_from_filename() {
    local fname="$1"

    # --- ② 尝试匹配毫秒级 Unix 时间戳（13 位数字）---
    if [[ "$fname" =~ [^0-9]?(1[0-9]{12})[^0-9]? ]]; then
        local ts_ms="${BASH_REMATCH[1]}"
        local ts_s=$((ts_ms / 1000))
        local year
        year=$(date -d "@$ts_s" '+%Y' 2>/dev/null)
        if [ -n "$year" ] && [ "$year" -ge 2000 ] && [ "$year" -le 2035 ]; then
            date -d "@$ts_s" '+%Y:%m:%d %H:%M:%S' 2>/dev/null
            return
        fi
    fi

    # --- ① YYYYMMDD[弹性匹配 HHMMSS] ---
    if [[ "$fname" =~ (20[0-9]{2})(0[1-9]|1[0-2])(0[1-9]|[12][0-9]|3[01])_?([0-9]{6})? ]]; then
        local y="${BASH_REMATCH[1]}" m="${BASH_REMATCH[2]}" d="${BASH_REMATCH[3]}" t="${BASH_REMATCH[4]}"
        if [ -n "$t" ]; then echo "${y}:${m}:${d} ${t:0:2}:${t:2:2}:${t:4:2}"
        else echo "${y}:${m}:${d} 00:00:00"; fi
    # --- YYYY-MM-DD 或 YYYY_MM_DD ---
    elif [[ "$fname" =~ (20[0-9]{2})[-_](0[1-9]|1[0-2])[-_](0[1-9]|[12][0-9]|3[01]) ]]; then
        echo "${BASH_REMATCH[1]}:${BASH_REMATCH[2]}:${BASH_REMATCH[3]} 00:00:00"
    fi
}

echo ""
echo "============================================================"
echo "🔍 正在扫描目录: $TARGET_DIR"

# --- 阶段一：扫描并收集待修改项 ---
declare -a TARGET_FILES=()
declare -a TARGET_DATES=()
ALREADY_HAS_EXIF=0
NO_DATE=0

while IFS= read -r filepath; do
    fname=$(basename "$filepath")
    existing=$(exiftool -s3 -DateTimeOriginal "$filepath" 2>/dev/null)
    if [ -n "$existing" ]; then
        ((ALREADY_HAS_EXIF++))
        continue
    fi
    parsed=$(parse_date_from_filename "$fname")
    if [ -z "$parsed" ]; then
        echo "  ⏭  [文件名无日期] $fname"
        ((NO_DATE++))
    else
        TARGET_FILES+=("$filepath")
        TARGET_DATES+=("$parsed")
    fi
done < <(find "$TARGET_DIR" -type f \( \
    -iname "*.jpg" -o -iname "*.jpeg" -o -iname "*.png" \
    -o -iname "*.heic" -o -iname "*.heif" \
    -o -iname "*.mp4" -o -iname "*.mov" -o -iname "*.avi" \
\))

TOTAL=${#TARGET_FILES[@]}

if [ "$TOTAL" -eq 0 ]; then
    echo ""
    echo "✅ 未发现可从文件名解析日期的文件。"
    echo "   已有 EXIF 日期（跳过）: $ALREADY_HAS_EXIF 个文件"
    echo "   文件名无法解析: $NO_DATE 个文件"
    exit 0
fi

# --- 阶段二：列出待修改项，等待确认 ---
echo ""
echo "📋 以下 $TOTAL 个文件将被写入 EXIF 日期："
echo "   （已有 EXIF: $ALREADY_HAS_EXIF，文件名无法解析: $NO_DATE）"
echo ""
printf "  %-4s  %-55s  %s\n" "#" "文件名" "解析日期"
printf "  %-4s  %-55s  %s\n" "----" "-------------------------------------------------------" "-------------------"
for i in "${!TARGET_FILES[@]}"; do
    printf "  %-4s  %-55s  →  %s\n" "[$((i+1))]" "$(basename "${TARGET_FILES[$i]}")" "${TARGET_DATES[$i]}"
done
echo ""

if $DRY_RUN; then
    echo "⚠️  [DRY-RUN 模式] 仅预览，不执行实际写入。"
    echo "    确认无误后，去掉 --dry-run 参数重新运行以实际写入。"
    exit 0
fi

read -p "是否确认写入以上 $TOTAL 个文件? (y/n): " confirm
if [[ "$confirm" != [yY] && "$confirm" != [yY][eE][sS] ]]; then
    echo "❌ 已取消。"
    exit 0
fi

# --- 阶段三：执行写入 ---
echo ""
echo "🚀 开始写入..."
UPDATED=0
for i in "${!TARGET_FILES[@]}"; do
    fname=$(basename "${TARGET_FILES[$i]}")
    if $NO_BACKUP; then
        exiftool -P -overwrite_original_in_place \
            -DateTimeOriginal="${TARGET_DATES[$i]}" \
            -CreateDate="${TARGET_DATES[$i]}" \
            "${TARGET_FILES[$i]}" > /dev/null 2>&1
    else
        exiftool -P \
            -DateTimeOriginal="${TARGET_DATES[$i]}" \
            -CreateDate="${TARGET_DATES[$i]}" \
            "${TARGET_FILES[$i]}" > /dev/null 2>&1
    fi
    echo "  ✅ $fname → ${TARGET_DATES[$i]}"
    ((UPDATED++))
done

echo "============================================================"
echo "✅ 写入完成！已处理: $UPDATED 个文件"
if ! $NO_BACKUP && [ "$UPDATED" -gt 0 ]; then
    echo ""
    echo "💾 备份文件已保存为 [原文件名]_original，确认无误后可用以下命令批量删除："
    echo "   find \"$TARGET_DIR\" -name \"*_original\" -delete"
fi
