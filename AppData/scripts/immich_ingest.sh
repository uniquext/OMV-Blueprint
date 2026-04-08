#!/bin/bash
# ==============================================================================
# Immich 一键集成入库脚本 (Standard Integration)
# 用法: ./immich_ingest.sh [-d 源目录] [-s IP:端口] [-k API_KEY]
# 逻辑: 检查环境 -> 目录扁平化 -> 模拟运行 -> 用户确认 -> 正式上传
# 示例: sh immich_ingest.sh -d /mnt/Cache/澄宝宝 -s 192.168.1.100:2283 -k your_api_key
#       sh immich_ingest.sh -d /mnt/Cache/澄宝宝   # 缺省参数将交互式补全
# ==============================================================================

SCRIPT_DIR=$(dirname "$(readlink -f "$0")")
FLATTEN_SCRIPT="$SCRIPT_DIR/flatten_directory.sh"

SRC_DIR=""
IMMICH_HOST=""
API_KEY=""

# --- 解析命名参数 ---
while getopts "d:s:k:" opt; do
    case $opt in
        d) SRC_DIR="$OPTARG" ;;
        s) IMMICH_HOST="$OPTARG" ;;
        k) API_KEY="$OPTARG" ;;
        *)
            echo "❌ 未知参数: -$OPTARG"
            echo "用法: sh immich_ingest.sh [-d 源目录] [-s IP:端口] [-k API_KEY]"
            exit 1
            ;;
    esac
done

# --- 交互式补全：缺省参数时询问用户 ---
if [ -z "$SRC_DIR" ]; then
    read -p "[请输入待上传目录路径]: " SRC_DIR
fi

if [ ! -d "$SRC_DIR" ]; then
    echo "❌ 找不到目录: $SRC_DIR"
    exit 1
fi

if [ ! -f "$FLATTEN_SCRIPT" ]; then
    echo "❌ 找不到扁平化脚本: $FLATTEN_SCRIPT"
    exit 1
fi

if [ -z "$IMMICH_HOST" ]; then
    read -p "[请输入 Immich 地址 (IP:端口，例：192.168.1.100:2283)]: " IMMICH_HOST
fi

if [ -z "$API_KEY" ]; then
    read -s -p "[请输入 Immich API Key]: " API_KEY
    echo ""
fi

# --- 构建完整 URL 和相册名 ---
IMMICH_URL="http://$IMMICH_HOST/api"
# 以源目录名作为相册名（与容器内挂载路径对齐）
ALBUM_NAME=$(basename "$SRC_DIR")

echo ""
echo "===================================="
echo "📁 源目录: $SRC_DIR"
echo "📂 相册名称: $ALBUM_NAME"
echo "🌐 服务地址: $IMMICH_URL"
echo "===================================="

# --- 步骤 1：目录扁平化 ---
echo ">>>> 步骤 1: 正在进行目录扁平化..."
sh "$FLATTEN_SCRIPT" "$SRC_DIR"

# --- 步骤 2：Dry Run 模拟 ---
echo ">>>> 步骤 2: 正在启动模拟上传 (Dry Run)..."
docker run --rm -it \
  -v "$SRC_DIR":/import/"$ALBUM_NAME":ro \
  ghcr.io/immich-app/immich-cli:latest \
  -u "$IMMICH_URL" \
  -k "$API_KEY" \
  upload /import --album --recursive --dry-run

# --- 步骤 3：确认正式提交 ---
read -p ">>>> 模拟运行结束。是否开始正式入库? (y/n): " confirm
if [[ "$confirm" == [yY] || "$confirm" == [yY][eE][sS] ]]; then
    echo ">>>> 步骤 3: 正在执行真实上传..."
    docker run --rm -it \
      -v "$SRC_DIR":/import/"$ALBUM_NAME":ro \
      ghcr.io/immich-app/immich-cli:latest \
      -u "$IMMICH_URL" \
      -k "$API_KEY" \
      upload /import --album --recursive
    echo ">>>> 🎉 入库任务圆满完成！"
else
    echo ">>>> 操作已取消。"
fi
