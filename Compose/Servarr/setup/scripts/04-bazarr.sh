#!/bin/bash

# ==============================================================================
# 04-bazarr.sh: Bazarr 字幕语言、应用绑定与提供商配置
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 4: Bazarr 字幕自动化配置..."

if ! wait_for_service "Bazarr" "$BAZARR_PORT" 20; then
    log_error "Bazarr 服务未就绪，跳过阶段 4 全部配置"
    exit 1
fi

# 核心依赖补齐 (确保容器内具备 sqlite3 指令)
docker exec "$BAZARR_HOSTNAME" apk add --no-cache sqlite > /dev/null 2>&1

# 1. 创建语言配置文件 (Language Profiles)
# Bazarr API 不支持创建语言配置文件，必须直接操作数据库
log_info "正在管理 Bazarr 语言配置文件..."
CONTAINER_DB="/config/db/bazarr.db"

# 前置守卫：容器内数据库文件是否存在
if ! docker exec "$BAZARR_HOSTNAME" test -f "$CONTAINER_DB"; then
    log_error "Bazarr 数据库文件不存在: $CONTAINER_DB (服务可能尚未完成首次初始化)"
    exit 1
fi

PROFILE_COUNT=$(docker exec "$BAZARR_HOSTNAME" sqlite3 "$CONTAINER_DB" "SELECT COUNT(*) FROM table_languages_profiles;" 2>&1)
if [ $? -ne 0 ]; then
    log_error "sqlite3 查询语言配置文件失败: $PROFILE_COUNT"
    exit 1
fi

if [ "$PROFILE_COUNT" -eq 0 ]; then
    # 创建简繁中文语言配置文件
    # zh = 简体中文, zt = 繁体中文
    # items 中的 id 字段是必需的，用于 cutoff 和健康检查
    docker exec "$BAZARR_HOSTNAME" sqlite3 "$CONTAINER_DB" \
        "INSERT INTO table_languages_profiles (profileId, cutoff, originalFormat, items, name, mustContain, mustNotContain, tag) VALUES (1, NULL, 0, '[{\"id\": 1, \"language\": \"zh\", \"audio_exclude\": \"False\", \"audio_only_include\": \"False\", \"hi\": \"False\", \"forced\": \"False\"}, {\"id\": 2, \"language\": \"zt\", \"audio_exclude\": \"False\", \"audio_only_include\": \"False\", \"hi\": \"False\", \"forced\": \"False\"}]', 'chinese', '[]', '[]', NULL);"
    log_success "语言配置文件创建成功 (简体中文 + 繁体中文)"
else
    log_info "语言配置文件已存在，跳过创建"
fi

# 1.5 启用语言 (Enable Languages)
# 必须在 table_settings_languages 中启用语言，前端才能正确显示
log_info "正在启用简繁中文字幕语言..."
ENABLED_COUNT=$(docker exec "$BAZARR_HOSTNAME" sqlite3 "$CONTAINER_DB" "SELECT COUNT(*) FROM table_settings_languages WHERE code2 IN ('zh', 'zt') AND enabled=1;" 2>&1)
if [ $? -ne 0 ]; then
    log_error "sqlite3 查询语言启用状态失败: $ENABLED_COUNT"
    exit 1
fi

if [ "$ENABLED_COUNT" -lt 2 ]; then
    docker exec "$BAZARR_HOSTNAME" sqlite3 "$CONTAINER_DB" "UPDATE table_settings_languages SET enabled=1 WHERE code2 IN ('zh', 'zt');"
    log_success "已启用简体中文和繁体中文"
else
    log_info "简繁中文已启用，跳过"
fi

# 2. 配置 Bazarr 语言、提供商与应用绑定
log_info "正在注入 Bazarr 配置..."
run_python_api "$SCRIPT_DIR/config/bazarr/config/config.yaml" \
               "$SONARR_KEY" "$RADARR_KEY" \
               "$SONARR_HOSTNAME" "$SONARR_PORT" \
               "$RADARR_HOSTNAME" "$RADARR_PORT" <<'PYEOF'
import yaml, sys, os
config_path, skey, rkey, sonarr_host, sonarr_port, radarr_host, radarr_port = sys.argv[1:8]

if not os.path.exists(config_path):
    print(f"  \033[31m[✗]\033[0m 找不到 Bazarr 配置文件: {config_path}")
    sys.exit(1)

try:
    with open(config_path, 'r', encoding='utf-8') as f: config = yaml.safe_load(f)
    if not config: config = {}

    # 通用设置
    config.setdefault('general', {})
    config['general']['use_sonarr'] = True
    config['general']['use_radarr'] = True

    # 字幕最低分设置
    config['general']['minimum_score'] = 70
    config['general']['minimum_score_movie'] = 70

    # 启用字幕站 (Gestdown, YIFY, AnimeTosho)
    config['general'].setdefault('enabled_providers', [])
    for p in ["gestdown", "yifysubtitles", "animetosho"]:
        if p not in config['general']['enabled_providers']:
            config['general']['enabled_providers'].append(p)

    # 默认语言配置文件设置 (profileId=1 为上面创建的 chinese 配置)
    config['general']['serie_default_enabled'] = True
    config['general']['serie_default_profile'] = 1
    config['general']['movie_default_enabled'] = True
    config['general']['movie_default_profile'] = 1

    # 绑定 Sonarr
    config.setdefault('sonarr', {})
    config['sonarr'].update({'apikey': skey, 'ip': sonarr_host, 'port': int(sonarr_port), 'enabled': True})

    # 绑定 Radarr
    config.setdefault('radarr', {})
    config['radarr'].update({'apikey': rkey, 'ip': radarr_host, 'port': int(radarr_port), 'enabled': True})

    with open(config_path, 'w', encoding='utf-8') as f:
        yaml.dump(config, f, default_flow_style=False, allow_unicode=True)
    print("  \033[32m[✓]\033[0m Bazarr YAML 配置更新成功")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m Bazarr 失败: {e}")
PYEOF

# 重启 Bazarr 使其强制读取配置
docker restart "$BAZARR_HOSTNAME" > /dev/null 2>&1
log_success "Bazarr 重启完成"
