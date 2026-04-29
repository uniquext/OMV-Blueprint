# OpenMediaVault 部署与配置指南

本仓库存放了一套完整、通用的 OpenMediaVault (OMV) 部署指南。教程旨在为用户提供从系统安装到底层存储构建，再到应用容器化的标准操作流程，适合作为标准化 NAS 系统的从零搭建参考。

---

## 仓库结构

```text
OMV/
├── AppData/                     # 公共资源与脚本
│   ├── fonts/                   # 字体资源
│   ├── inject_env/              # 私有环境变量注入配置
│   │   ├── inject.sh            # 环境变量注入脚本
│   │   └── private.env.example  # 私有配置模板（复制后填写）
│   ├── scripts/                 # 运维脚本集
│   └── template/
│       └── gpu-template.yml     # NVIDIA GPU 全局复用模板
├── Compose/                     # Docker Compose 堆栈定义
│   ├── ClashMeta/               # 代理网关堆栈
│   │   ├── config/              # mihomo 配置文件
│   │   └── emergency_bypass.sh  # 代理紧急避险脚本
│   ├── Cloudflared/             # Cloudflare Tunnel 穿透客户端堆栈
│   ├── FileBrowser/             # 文件管理器堆栈
│   ├── Immich/                  # 相册管理堆栈
│   ├── Jellyfin/                # 媒体服务器堆栈
│   ├── Servarr/                 # 媒体自动化堆栈
│   │   ├── startup.sh           # 一键初始化脚本
│   │   └── setup/               # 初始化脚本与自定义格式
│   └── global.env               # 全局环境变量（UID/GID/时区/路径/GPU）
├── docs/                        # 部署文档（按阶段编号）
│   ├── 01-系统安装.md
│   ├── 02-基础环境优化.md
│   ├── 03-数据存储与局域网共享.md
│   ├── 04-系统扩展与容器层构建.md
│   ├── 05-容器与应用服务/       # 各服务的详细配置文档
│   │   ├── ClashMeta/说明.md
│   │   ├── FileBrowser/说明.md
│   │   ├── Immich/说明.md
│   │   ├── Jellyfin/说明.md
│   │   ├── Servarr/说明.md
│   │   └── 说明.md              # 服务总索引
│   └── 06-常见问题与故障排除.md
```

---

## 部署路线图

本指南分为三个阶段。各阶段文档之间存在前后依赖关系，请严格按照下方列表的顺序依次执行。

### 第一阶段：底层系统与存储架构

本阶段涉及系统网络、镜像源、系统时钟等基础环境的初始化设置，以及实体存储设备的挂载与共享分配。

| 序号 | 模块 | 状态 |
| :---: | :--- | :---: |
| 01 | [系统安装与网络初始化](docs/01-系统安装.md) | ✅ |
| 02 | [基础环境优化（源加速与硬件调优）](docs/02-基础环境优化.md) | ✅ |
| 03 | [数据存储与局域网共享（LVM 与 SMB）](docs/03-数据存储与局域网共享.md) | ✅ |

### 第二阶段：运行环境与容器支持

本阶段将引入第三方库以扩展 OMV 的原生系统能力，并部署 Docker 引擎打通应用生态。

| 序号 | 模块 | 状态 |
| :---: | :--- | :---: |
| 04 | [系统扩展与容器引擎构建（Docker 与驱动直通）](docs/04-系统扩展与容器层构建.md) | ✅ |

### 第三阶段：应用层服务

系统基础设施全部就绪后，您可以进入此阶段浏览并按需部署各项上层应用容器。建议优先拉起网络基建类模块（如 05-1 代理网关），以确保后续强依赖境外环境的服务能够畅通无阻。

|  序号  | 模块 | 状态 |
|:----:| :--- | :---: |
|  05  | **[容器与应用库总索引](docs/05-容器与应用服务/说明.md)** | 🚧 施工中 |
| 05-1 | [Clash Meta 显式代理网关](docs/05-容器与应用服务/ClashMeta/说明.md) | ✅ |
| 05-2 | [Immich 高性能相册管理](docs/05-容器与应用服务/Immich/说明.md) | ✅ |
| 05-3 | [FileBrowser 轻量级文件管理器](docs/05-容器与应用服务/FileBrowser/说明.md) | ✅ |
| 05-4 | [Jellyfin 媒体服务器](docs/05-容器与应用服务/Jellyfin/说明.md) | ✅ |
| 05-5 | [Cloudflared Cloudflare Tunnel 穿透客户端](docs/05-容器与应用服务/Cloudflared/说明.md) | ✅ |
| 05-6 | [Servarr 媒体自动化大一统](docs/05-容器与应用服务/Servarr/说明.md) | ✅ |
| 05-7 | [iperf3 网络带宽测试工具](docs/05-容器与应用服务/Iperf3/说明.md) | ✅ |
| 05-8 | [AdGuardHome 私有局域网 DNS 服务](docs/05-容器与应用服务/AdGuardHome/说明.md) | ✅ |
| 05-9 | [Tailscale 极速安全的虚拟内网](docs/05-容器与应用服务/Tailscale/说明.md) | ✅ |

---

## 辅助工具

### 私有配置与系统注入 (`AppData/inject_env/private.env.example` + `AppData/inject_env/inject.sh`)

首次部署时，进入 `AppData/inject_env/` 目录复制模板并填入实际值，再通过注入脚本将变量写入系统环境：

```bash
cd AppData/inject_env
cp private.env.example private.env
# 编辑 private.env，填入 IMMICH_DB_PASSWORD 等敏感值
sudo bash inject.sh private.env
```

`inject.sh` 会将 `private.env` 中的变量幂等注入到 `/etc/environment` 与 `/root/.bashrc`，确保系统服务与 Cron 任务均可读取。

### 运维脚本集 (`AppData/scripts/`)

| 脚本 | 用途 |
| :--- | :--- |
| `compress_arw.sh` | 将当前目录下 ARW 原始文件压缩为 zip 包并删除原文件（避免媒体库扫描） |
| `flatten_directory.sh` | 目录扁平化：将深层嵌套文件提取到目标目录根部，同名冲突自动重命名，清理空子文件夹 |
| `dup_clean_keep_first.sh` | 重复文件清洗模式 A：以第一个目录为基准保留文件，删除其他目录中的同名文件 |
| `dup_clean_drop_first.sh` | 重复文件清洗模式 B：以后续目录为基准保留文件，删除第一个目录中的同名文件 |
| `immich_ingest.sh` | 一键入库：目录扁平化 → 模拟上传(Dry Run) → 确认后正式上传，以源目录名自动创建相册 |
| `immich_album_keep_target.sh` | 相册去重模式 A：对同时属于多个相册的资产，仅保留在指定相册中，从其他相册移除关联 |
| `immich_album_drop_target.sh` | 相册去重模式 B：对同时属于多个相册的资产，从指定相册中移除关联，保留在其他相册 |
| `immich_cleanup_ghosts.sh` | 幽灵资产清理：检测物理文件已丢失但数据库仍残留的"幽灵缩略图"，确认后从数据库抹除 |
| `immich_fix_date_api.sh` | 通过 Immich API 修正日期：从文件名解析日期（支持时间戳/日期格式），更新 EXIF 为空的资产 |
| `immich_fix_date_exif.sh` | 通过 exiftool 写入 EXIF 日期：从文件名解析日期，写入缺失 DateTimeOriginal 的本地文件 |

### GPU 复用模板 (`AppData/template/gpu-template.yml`)

NVIDIA GPU 的全局配置模板，各服务 Compose 文件通过 `extends` 引用，避免重复配置。使用前需确保 `global.env` 中已正确设置 `NVIDIA_VISIBLE_DEVICES` 与 `NVIDIA_DRIVER_CAPABILITIES`。

---

## 异常与排障指南

在装机或调优过程中如遇任何网络阻断、依赖报错（如时间锁死、GPG 签名缺失等）非预期中断情况，请首先查阅专项排雷文档：
- [06 — 常见问题与故障排除](docs/06-常见问题与故障排除.md)
