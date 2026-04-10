#!/bin/bash

# ==============================================================================
# Immich 相册去重对比组 - 模式 A：仅保留目标相册 (immich_album_keep_target.sh)
# ==============================================================================
# 组说明：本工具组包含两个脚本，处理互补的相册去重逻辑：
#   1. immich_album_keep_target.sh: 仅保留指定相册的关联，移除其他相册中的关联。
#   2. immich_album_drop_target.sh: 仅剔除指定相册的关联，保留其他相册中的关联。
# ------------------------------------------------------------------------------
# 当前脚本作用：从同时属于多个相册的资产中，移除【除指定相册外】的所有关联。
# 核心逻辑：查找总关联数 > 1 且属于目标相册的资产，仅保留目标相册的关联，移除其他相册的关联。
# 场景说明：照片同时在多个相册中，执行后照片仅保留在指定相册中。
#
# 用法：bash immich_album_keep_target.sh [需保留的相册名称]
# 参数：
#   [需保留的相册名称]   仅保留该相册的关联，资产从其他相册中移除
# 示例：
#   bash immich_album_keep_target.sh 澄宝宝
# 依赖：Docker (immich-postgres), 环境变量 IMMICH_DB_PASSWORD
# ==============================================================================

# --- 检查路径参数数量 ---
if [ "$#" -lt 1 ]; then
    echo "❌ 缺少参数！至少需要提供 1 个相册名称。"
    echo "用法: bash immich_album_keep_target.sh [需保留的相册名称]"
    exit 1
fi

ALBUM_NAME=$1

echo "📁 保留相册: $ALBUM_NAME"
echo ""

echo "============================================================"
echo "🔍 正在检索数据，准备为资产仅保留相册关联: [$ALBUM_NAME]..."

# --- 执行 SQL ---
docker exec -t immich-postgres env PGPASSWORD=${IMMICH_DB_PASSWORD} psql -U postgres -d immich -c \
"DELETE FROM album_asset aa 
USING album ab_target
WHERE ab_target.\"albumName\" = '$ALBUM_NAME'
  AND aa.\"albumId\" != ab_target.id
  AND aa.\"assetId\" IN (
      SELECT sub_aa.\"assetId\"
      FROM album_asset sub_aa
      WHERE sub_aa.\"albumId\" = ab_target.id
        AND sub_aa.\"assetId\" IN (
            SELECT \"assetId\" FROM album_asset GROUP BY \"assetId\" HAVING COUNT(*) > 1
        )
  );"

echo "============================================================"
if [ $? -eq 0 ]; then
    echo "✅ 清理指令已发送并执行成功。"
else
    echo "❌ 执行过程中出现错误，请检查数据库连接及相册名称是否准确。"
fi
