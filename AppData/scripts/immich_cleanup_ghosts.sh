#!/bin/bash

# ==============================================================================
# Immich 幽灵资产强制清理脚本 (适配 v2.5.6)
# ==============================================================================
# 当前脚本作用：解决手动 `rm` 删除物理文件后，Web 端残留"幽灵缩略图"且无法通过 UI 同步的问题。
# 核心逻辑：读取数据库全量资产记录 -> 在 server 容器内校验物理文件存在性 -> 列出幽灵资产 -> 确认后从数据库删除。
# 清理范围：仅删除物理文件已丢失但数据库记录仍存在的"幽灵资产"。
#
# 用法：bash immich_cleanup_ghosts.sh
# 参数：无（脚本无需参数，自动扫描全库）
# 示例：
#   bash immich_cleanup_ghosts.sh
# 依赖：Docker (immich-postgres, immich-server)
# ==============================================================================

DB_CONTAINER="immich-postgres"
SERVER_CONTAINER="immich-server"

echo "============================================================"
echo "🔍 正在读取 Immich 数据库中的全量资产记录..."

# --- 提取所有资产的 ID 和物理路径 ---
docker exec -i "$DB_CONTAINER" psql -U postgres -d immich -t -c "SELECT id, \"originalPath\" FROM asset;" | sed 's/|//g' | awk '{$1=$1;print}' > /tmp/immich_assets.txt

TOTAL=$(wc -l < /tmp/immich_assets.txt | awk '{print $1}')
echo "📊 共检索到 $TOTAL 条记录，正在进入容器内核对物理磁盘..."

MISSING_FILE="/tmp/immich_ghost_ids.txt"
> "$MISSING_FILE"

# --- 一次性将列表灌入 server 容器内进行批量存在性校验 ---
docker exec -i "$SERVER_CONTAINER" sh -c '
while read -r id path; do
    if [ -z "$id" ] || [ -z "$path" ]; then continue; fi
    if [ ! -f "$path" ]; then
        echo "$id|$path"
    fi
done
' < /tmp/immich_assets.txt > "$MISSING_FILE"

GHOST_COUNT=$(wc -l < "$MISSING_FILE" | awk '{print $1}')

if [ "$GHOST_COUNT" -eq 0 ]; then
    echo "============================================================"
    echo "✅ 未发现物理文件丢失的缩略图残留，数据库与磁盘现已有序同步！"
    rm -f /tmp/immich_assets.txt "$MISSING_FILE"
    exit 0
fi

echo "============================================================"
echo "🚨 发现 $GHOST_COUNT 条"幽灵资产"驻留在您的时间轴上！(物理文件已失联)"
echo "--------------------------------------------------------"
awk -F'|' '{print "  🗑️  资产 UUID: " $1 "\n     丢失的路径: " $2}' "$MISSING_FILE"
echo "--------------------------------------------------------"

read -p "⚠️  是否立即从数据库中彻底抹除这 $GHOST_COUNT 条幽灵记录？[y/N]: " confirm
if [ "$confirm" = "y" ] || [ "$confirm" = "Y" ]; then
    echo "🚀 执行数据抹除..."

    SQL_IN=$(awk -F'|' '{printf "'\''%s'\'',", $1}' "$MISSING_FILE" | sed 's/,$//')

    docker exec -i "$DB_CONTAINER" psql -U postgres -d immich -c "DELETE FROM asset WHERE id IN ($SQL_IN);"

    echo "============================================================"
    echo "✅ 强制大扫除完毕！请刷新 Immich 网页，那些灰色缩略图已经永远消失了。"
    echo "🗑️  附：本次成功从数据库中解绑的物理废弃路径如下："
    awk -F'|' '{print "     - " $2}' "$MISSING_FILE"
else
    echo "❌ 操作取消，未修改数据库。"
fi

# --- 清理临时文件 ---
rm -f /tmp/immich_assets.txt "$MISSING_FILE"
