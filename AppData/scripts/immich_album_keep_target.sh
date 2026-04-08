#!/bin/bash

# ==============================================================================
# Immich 相册去重对比组 - [模式 A：仅保留目标相册] (immich_album_keep_target.sh)
# 作用：从同时属于多个相册的资产中，移除【除指定相册外】的所有关联。
#       （即：照片最终会仅保留在传入参数代表的相册中，从其他相册中消失）
# 示例：照片1同时在相册A和相册B中，执行 "sh immich_album_keep_target.sh A" 后，照片1将只保留在相册A中。
# ==============================================================================

# 检查参数
if [ -z "$1" ]; then
    echo "❌ 缺少参数！"
    echo "用法: sh immich_album_keep_target.sh [需保留的相册名称]"
    echo "示例: sh immich_album_keep_target.sh 澄宝宝"
    exit 1
fi

ALBUM_NAME=$1

echo "🔍 正在检索数据，准备为资产仅保留相册关联: [$ALBUM_NAME]..."

# 执行 SQL
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

if [ $? -eq 0 ]; then
    echo "✅ 清理指令已发送并执行成功。"
else
    echo "❌ 执行过程中出现错误。"
fi
