#!/bin/bash

# ==============================================================================
# ARW 原片压缩归档脚本 (compress_arw.sh)
# ==============================================================================
# 当前脚本作用：将指定目录下的所有 .ARW 索尼原片文件压缩打包为 ZIP 归档。
# 核心逻辑：扫描指定目录 ARW 文件 -> 检查 zip 工具依赖 -> 执行压缩（使用 -m 压缩后删除原文件）。
# 压缩策略：使用 -m 参数压缩后自动删除原文件，-j 丢弃路径名；如需保留原文件请去掉 -m。
# 应用场景：Jellyfin 等媒体服务器不再扫描 ARW 原片，节省磁盘空间。
#
# 用法：bash compress_arw.sh [目标目录路径]
# 参数：
#   [目标目录路径]     包含 .ARW 文件的目录路径，压缩包将生成在该目录下
# 示例：
#   bash compress_arw.sh /mnt/Cache/DCIM
# 依赖：zip
# ==============================================================================

# --- 检查路径参数数量 ---
if [ "$#" -lt 1 ]; then
    echo "❌ 缺少参数！至少需要提供 1 个路径。"
    echo "用法: bash compress_arw.sh [目标目录路径]"
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

ZIP_NAME="ARW原片.zip"

echo "📁 目标目录: $ABS_TARGET_DIR"
echo ""

# --- 统计当前目录下 ARW 文件数量 ---
COUNT=$(find . -maxdepth 1 -iname "*.ARW" | wc -l)

if [ "$COUNT" -eq 0 ]; then
    echo "✅ 当前目录下没有发现 .ARW 文件，无需压缩。"
    exit 0
fi

echo "📊 发现 $COUNT 个 .ARW 文件，正在压缩..."

# --- 依赖检查 ---
if ! command -v zip &> /dev/null; then
    echo "❌ 缺少依赖: zip"
    echo "  安装方式: apt update && apt install zip -y  (Debian/Ubuntu)"
    echo "            brew install zip                  (macOS)"
    exit 1
fi

# --- 执行压缩操作 ---
zip -m "$ZIP_NAME" ./*.ARW ./*.arw 2>/dev/null

echo "============================================================"
echo "✅ 压缩完成！"
echo "📦 已生成压缩包: $ABS_TARGET_DIR/$ZIP_NAME"
echo "🗑️  原 .ARW 文件已安全移入压缩包"
