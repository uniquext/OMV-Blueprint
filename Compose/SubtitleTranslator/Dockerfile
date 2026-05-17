# 第一阶段：前端构建
FROM node:20-alpine AS frontend-builder
WORKDIR /web
# 仅复制 package.json 进行依赖安装，利用缓存
COPY web/package*.json ./
RUN npm install
# 复制所有前端代码并构建
COPY web/ ./
RUN npm run build

# 第二阶段：后端运行
FROM python:3.9-slim
WORKDIR /app

# 安装后端依赖
COPY app/requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt

# 复制后端代码
COPY app/ ./

# 从第一阶段复制前端构建产物到后端的 dist 目录
# main.py 中配置了挂载 ./dist 目录
COPY --from=frontend-builder /web/dist ./dist

# 暴露端口（容器内部端口）
EXPOSE 9800

# 启动 FastAPI 服务
CMD ["uvicorn", "main:app", "--host", "0.0.0.0", "--port", "9800"]
