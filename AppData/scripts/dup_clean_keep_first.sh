#!/bin/bash

# ==============================================================================
# 重复文件清洗工具组 - 模式 A：保留首目录 (Keep First)
# ==============================================================================
# 组说明：本工具组包含两个脚本，处理互补的清洗逻辑：
#   1. dup_clean_keep_first.sh: 保留第1个目录，删除后续目录中的同名文件。
#   2. dup_clean_drop_first.sh: 保留后续所有目录，仅删除第1个目录中的同名文件。
# ------------------------------------------------------------------------------
# 当前脚本作用：在多个目录中递归查找文件名相同的文件。
# 核心逻辑：以【第一个传入目录】为基准，保留其中的文件，清理其余目录中的同名文件。
# 匹配准则：仅对比文件名（basename），不对比文件内容。
#
# 用法：sh dup_clean_keep_first.sh [--dry-run] [基准目录] [目录2] [目录3] ...
# 参数：
#   --dry-run   仅预览操作，不实际删除（强烈建议首次使用时加上）
#   [基准目录]  保留该目录中的所有文件，同名文件仅此处保留
#   [目录N]    其中与基准目录同名的文件将被删除
# 示例：
#   sh dup_clean_keep_first.sh --dry-run /mnt/Cache/DCIM /mnt/Media/DCIM
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
    echo "用法: sh dup_clean_keep_first.sh [--dry-run] [基准目录] [目录2] [目录3] ..."
    exit 1
fi

# --- 校验所有目录是否存在 ---
for dir in "$@"; do
    if [ ! -d "$dir" ]; then
        echo "❌ 找不到目录: $dir"
        exit 1
    fi
done

BASE_DIR="$1"
shift
OTHER_DIRS=("$@")

echo "📁 基准目录（保留）: $BASE_DIR"
for d in "${OTHER_DIRS[@]}"; do
    echo "📂 对比目录（清洗）: $d"
done
if $DRY_RUN; then
    echo "⚠️  [DRY-RUN 模式] 仅预览，不执行实际删除"
fi
echo ""

# --- 临时文件 ---
TMP_DIR=$(mktemp -d)
BASE_LIST="$TMP_DIR/base.txt"
DUP_NAMES="$TMP_DIR/dup_names.txt"

# --- 提取基准目录所有文件名 ---
find "$BASE_DIR" -type f | while read -r f; do basename "$f"; done | sort -u > "$BASE_LIST"

TOTAL_DELETED=0
TOTAL_DUP=0

echo "============================================================"

# --- 逐个对比目录 ---
for OTHER_DIR in "${OTHER_DIRS[@]}"; do
    echo "🔍 正在对比: $OTHER_DIR"
    # 找出该目录中，文件名存在于基准目录的文件
    find "$OTHER_DIR" -type f | while read -r f; do
        fname=$(basename "$f")
        if grep -qxF "$fname" "$BASE_LIST"; then
            echo "$f"
        fi
    done > "$DUP_NAMES"

    COUNT=$(wc -l < "$DUP_NAMES" | tr -d ' ')
    TOTAL_DUP=$((TOTAL_DUP + COUNT))

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
done

echo "============================================================"
if $DRY_RUN; then
    echo "🔎 [DRY-RUN] 合计发现 $TOTAL_DUP 个重名文件，未执行删除。"
    echo "    确认无误后，去掉 --dry-run 参数重新运行以实际删除。"
else
    echo "✅ 清洗完成，共删除 $TOTAL_DELETED 个重名文件。"
fi

# --- 清理临时文件 ---
rm -rf "$TMP_DIR"
