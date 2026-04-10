#!/bin/bash

# ==============================================================================
# 通用目录扁平化脚本 (精准模式)
# ==============================================================================
# 当前脚本作用：将目标目录下所有【深层嵌套】的文件提取到【目标目录的根部】，并清理空子文件夹。
# 核心逻辑：递归查找 mindepth≥2 的文件，mv 到目标目录根部；同名冲突时追加时间戳重命名。
# 冲突处理：若根部已存在同名文件，自动追加 Unix 纳秒时间戳避免覆盖。
#
# 用法：bash flatten_directory.sh [目标目录路径]
# 参数：
#   [目标目录路径]     需要扁平化的目录，其下所有子目录中的文件将被提取到根部
# 示例：
#   bash flatten_directory.sh /mnt/Cache/Camera
# ==============================================================================

# --- 检查路径参数数量 ---
if [ "$#" -lt 1 ]; then
    echo "❌ 缺少参数！请提供目标目录路径。"
    echo "用法: bash flatten_directory.sh [目标目录路径]"
    exit 1
fi

TARGET_DIR="$1"

# --- 校验目录是否存在 ---
if [ ! -d "$TARGET_DIR" ]; then
    echo "❌ 找不到目录: $TARGET_DIR"
    exit 1
fi

# --- 转换为绝对路径 ---
ABS_TARGET_DIR=$(cd "$TARGET_DIR" && pwd)
cd "$ABS_TARGET_DIR" || exit

# --- 计数初始化 ---
total_files=$(find . -type f | wc -l)
moved_count=0

echo "📁 目标目录: $ABS_TARGET_DIR"
echo "📊 磁盘资产总数: $total_files (包含已在根部的资产)"
echo ""

echo "============================================================"
echo "🔍 正在扁平化目录: $ABS_TARGET_DIR"

# --- 查找所有位于子目录中的文件并移动到根部 ---
while read -r file; do
    # 移除文件名前面的 ./ 
    clean_file="${file#./}"
    filename=$(basename "$file")
    
    # 冲突处理：如果目标根目录已存在同名文件
    if [ -f "$filename" ]; then
        timestamp=$(date +%s%N)
        extension="${filename##*.}"
        basename_no_ext="${filename%.*}"
        new_filename="${basename_no_ext}_${timestamp}.${extension}"
        echo "  ⚠️  [冲突处理] $filename -> $new_filename"
        mv "$file" "$new_filename"
    else
        echo "  📦 [移动文件] $clean_file -> $filename"
        mv "$file" "$filename"
    fi
    ((moved_count++))
done < <(find . -mindepth 2 -type f)

# --- 清理所有空子目录 ---
echo "🧹 正在清理空文件夹..."
find . -mindepth 1 -type d -empty -delete

echo "============================================================"
echo "✅ 扁平化完成！"
echo "📁 物理位置: $ABS_TARGET_DIR"
echo "📊 资产总计: $total_files"
echo "📦 提取移动: $moved_count"
