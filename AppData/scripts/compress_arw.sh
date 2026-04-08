#!/bin/bash

# 设置压缩包名称
ZIP_NAME="ARW原片.zip"

# 统计当前目录下 ARW 文件数量 (不区分大小写)
COUNT=$(find . -maxdepth 1 -iname "*.ARW" | wc -l)

if [ "$COUNT" -eq 0 ]; then
    echo "--- 提示：当前目录下没有发现 .ARW 文件 ---"
    exit 0
fi

echo "发现 $COUNT 个 .ARW 文件，正在压缩..."

# 检查系统是否安装了 zip 工具
if ! command -v zip &> /dev/null; then
    echo "错误：系统未安装 'zip' 工具。请运行 'apt update && apt install zip -y' 安装。"
    exit 1
fi

# 执行压缩操作 (使用 -m 参数可以在压缩后自动删除原文件，-j 丢弃路径名)
# 如果你想保留原文件，请去掉 -m
zip -m "$ZIP_NAME" ./*.ARW ./*.arw 2>/dev/null

echo "--- 压缩完成！---"
echo "已生成压缩包：$(pwd)/$ZIP_NAME"
echo "原 .ARW 文件已安全移入压缩包，Jellyfin 将不再扫描它们。"
