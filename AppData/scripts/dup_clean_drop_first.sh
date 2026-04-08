#!/bin/bash

# ==============================================================================
# 重复文件清洗工具组 - 模式 B：丢弃首目录 (Drop First)
# ==============================================================================
# 组说明：本工具组包含两个脚本，处理互补的清洗逻辑：
#   1. dup_clean_keep_first.sh: 保留第1个目录，删除后续目录中的同名文件。
#   2. dup_clean_drop_first.sh: 保留后续所有目录，仅删除第1个目录中的同名文件。
# ------------------------------------------------------------------------------
# 当前脚本作用：以多个“基准目录”为标准，清理“目标目录”中的同名冗余文件。
# 核心逻辑：提取后续所有目录中的文件名全集，并在【第一个传入目录】中删除与其匹配的文件。
# 匹配准则：仅对比文件名（basename），不对比文件内容。
#
# 用法：sh dup_clean_drop_first.sh [--dry-run] [待清理目标目录] [基准目录1] [基准目录2] ...
# 参数：
#   --dry-run           仅预览操作，不实际删除（强烈建议首次使用时加上）
#   [待清理目标目录]     将在这个目录中查找并删除同名文件
#   [基准目录N]          保留这些目录中的文件作为比对标准
# 示例：
#   sh dup_clean_drop_first.sh --dry-run /mnt/Wait_To_Delete /mnt/Keep_A /mnt/Keep_B
# ==============================================================================

# --- 解析 --dry-run 参数 ---
DRY_RUN=false
if [ "$1" = "--dry-run" ]; then
    DRY_RUN=true
    shift
fi

# --- 检查路径参数数量 ---
if [ "$#" -lt 2 ]; then
    echo "❌ 缺少参数！至少需要提供 2 个路径。"
    echo "用法: sh dup_clean_drop_first.sh [--dry-run] [待清理目标目录] [基准目录1] [基准目录2] ..."
    exit 1
fi

# --- 校验所有目录是否存在 ---
for dir in "$@"; do
    if [ ! -d "$dir" ]; then
        echo "❌ 找不到目录: $dir"
        exit 1
    fi
done

TARGET_DIR="$1"
shift
BASE_DIRS=("$@")

echo "🗑️ 目标目录（将被清理）: $TARGET_DIR"
for d in "${BASE_DIRS[@]}"; do
    echo "📁 基准目录（保留标准）: $d"
done
if $DRY_RUN; then
    echo "⚠️  [DRY-RUN 模式] 仅预览，不执行实际删除"
fi
echo ""

# --- 临时文件 ---
TMP_DIR=$(mktemp -d)
BASE_LIST="$TMP_DIR/base.txt"
DUP_NAMES="$TMP_DIR/dup_names.txt"

# --- 提取所有基准目录的文件名并合并 ---
for d in "${BASE_DIRS[@]}"; do
    find "$d" -type f | while read -r f; do basename "$f"; done
done | sort -u > "$BASE_LIST"

TOTAL_DELETED=0
TOTAL_DUP=0

echo "============================================================"
echo "🔍 正在对比并清理目标目录: $TARGET_DIR"

# 找出目标目录中，文件名存在于基准目录的文件
find "$TARGET_DIR" -type f | while read -r f; do
    fname=$(basename "$f")
    if grep -qxF "$fname" "$BASE_LIST"; then
        echo "$f"
    fi
done > "$DUP_NAMES"

COUNT=$(wc -l < "$DUP_NAMES" | tr -d ' ')
TOTAL_DUP=$COUNT

if [ "$COUNT" -eq 0 ]; then
    echo "  ✅ 无重名文件"
else
    echo "  ⚠️  发现 $COUNT 个重名文件："
    while IFS= read -r dup_file; do
        echo "    🗑️  $dup_file"
        if ! $DRY_RUN; then
            rm -f "$dup_file"
            TOTAL_DELETED=$((TOTAL_DELETED + 1))
        fi
    done < "$DUP_NAMES"
fi
echo ""

# --- 统计结果 ---
echo "============================================================"
if $DRY_RUN; then
    echo "🔎 [DRY-RUN] 合计发现 $TOTAL_DUP 个重名文件，未执行删除。"
    echo "    确认无误后，去掉 --dry-run 参数重新运行以实际删除。"
else
    echo "✅ 清洗完成，共在目标目录中删除 $TOTAL_DELETED 个重名文件。"
fi

# --- 清理临时文件 ---
rm -rf "$TMP_DIR"
