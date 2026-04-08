#!/bin/bash
# ==============================================================================
# 通用目录扁平化脚本 (精准模式)
# 用法: ./flatten_directory.sh [目标目录路径]
# 作用: 将目标目录下所有【深层嵌套】的文件提取到【目标目录的根部】，并清理空子文件夹。
# 示例: 传入 Camera，则 Camera/2026/1.jpg -> Camera/1.jpg
# ==============================================================================

# 检查参数
TARGET_DIR="$1"

if [ -z "$TARGET_DIR" ]; then
    echo "使用错误! 请提供目标目录路径。"
    echo "用法示例: $0 /path/to/your/photos"
    exit 1
fi

# 检查目录是否存在
if [ ! -d "$TARGET_DIR" ]; then
    echo "错误: 找不到目录 $TARGET_DIR"
    exit 1
fi

# 转换为绝对路径
ABS_TARGET_DIR=$(cd "$TARGET_DIR" && pwd)
cd "$ABS_TARGET_DIR" || exit

# 计数初始化
total_files=$(find . -type f | wc -l)
moved_count=0

echo "正在平铺目录: $ABS_TARGET_DIR"
echo "磁盘资产总数: $total_files (包含已在根部的资产)"

# 查找所有位于子目录中的文件 (mindepth 2 表示排除目标目录根部的已有文件)
# 使用进程替换防止 while 循环在子 shell 中运行导致变量无法累加
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
        echo "  [冲突处理] $filename -> $new_filename"
        mv "$file" "$new_filename"
    else
        echo "  [移动文件] $clean_file -> $filename"
        mv "$file" "$filename"
    fi
    ((moved_count++))
done < <(find . -mindepth 2 -type f)

# 清理所有空子目录
echo "正在清理空文件夹..."
find . -mindepth 1 -type d -empty -delete

echo "================================================"
echo "扁平化完成！"
echo "物理位置: $ABS_TARGET_DIR"
echo "资产总计: $total_files"
echo "提取移动: $moved_count"
