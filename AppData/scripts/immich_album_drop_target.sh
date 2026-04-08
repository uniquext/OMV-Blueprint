#!/bin/bash

# ==============================================================================
# Immich 相册去重对比组 - [模式 B：仅剔除目标相册] (immich_album_drop_target.sh)
# 作用：从同时属于多个相册的资产中，移除【指定相册】的关联。
#       （即：照片最终会从传入参数代表的相册中消失，但保留在其他相册中）
# 场景：解决手机 App 自动同步 (Camera) 与 CLI 上传 (指定相册) 产生的冗余关联。
# 示例：照片1同时在相册A和相册B中，执行 "sh immich_album_drop_target.sh A" 后，照片1将保留在相册B中，从A中消失。
# ==============================================================================

# 检查参数
if [ -z "$1" ]; then
    echo "❌ 缺少参数！"
    echo "用法: sh immich_album_drop_target.sh [待剔除的相册名称]"
    echo "示例: sh immich_album_drop_target.sh Camera"
    exit 1
fi

ALBUM_NAME=$1

echo "🔍 正在检索数据，准备从冗余关联中剔除相册: [$ALBUM_NAME]..."

# 执行 SQL (逻辑：找出那些属于指定相册且总关联数 > 1 的资产，将它们从该指定相册中解除关联)
# 注意：SECRET_IMMICH_DB_PASSWORD 等变量依赖环境变量或预注入。

docker exec -t immich-postgres env PGPASSWORD=${IMMICH_DB_PASSWORD} psql -U postgres -d immich -c \
"DELETE FROM album_asset aa 
USING album ab_target
WHERE ab_target.\"albumName\" = '$ALBUM_NAME'
  AND aa.\"albumId\" = ab_target.id  -- 删除正是目标相册的记录
  AND aa.\"assetId\" IN (
      -- 找出那些在目标相册里，且总关联数 > 1 的资产
      SELECT sub_aa.\"assetId\"
      FROM album_asset sub_aa
      WHERE sub_aa.\"albumId\" = ab_target.id
        AND sub_aa.\"assetId\" IN (
            SELECT \"assetId\" FROM album_asset GROUP BY \"assetId\" HAVING COUNT(*) > 1
        )
  );"

if [ $? -eq 0 ]; then
    echo "✅ 清理指令已发送并执行成功。"
    echo "⚠️  注意：请务必前往 Immich Web -> Administration -> Jobs 手动运行 'Storage Template Migration' 任务，以触发磁盘物理路径的映射更新。"
else
    echo "❌ 执行过程中出现错误，请检查数据库连接及相册名称是否准确。"
fi
