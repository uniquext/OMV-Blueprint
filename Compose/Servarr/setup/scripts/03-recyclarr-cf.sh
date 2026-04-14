#!/bin/bash

# ==============================================================================
# 03-recyclarr-cf.sh: Recyclarr 质量同步与中文字幕 Custom Format 导入
#
# 功能：
#   1. 初始化 Recyclarr 配置文件 (含 cron_schedule 定时同步)
#   2. 执行首次同步
#   3. 导入中文字幕 Custom Formats
#
# 定时同步：通过 Servarr.yml 中的 CRON_SCHEDULE 环境变量配置 (每天凌晨3点)
# ==============================================================================

SCRIPT_DIR="$(cd "$(dirname "$0")/../.." && pwd)"
source "$(dirname "$0")/common.sh"
load_env "$SCRIPT_DIR/Servarr.env"

log_info "开始阶段 3: 质量配置同步与中文字幕 CF 导入..."

RECYCLARR_TEMPLATE="$SCRIPT_DIR/setup/recyclarr.yml.template"
RECYCLARR_CONFIG="$SCRIPT_DIR/config/recyclarr/recyclarr.yml"

# ==============================================================================
# 1. 配置 Recyclarr (含每天凌晨3点自动同步)
# ==============================================================================
log_info "正在配置 Recyclarr..."

if [ -f "$RECYCLARR_TEMPLATE" ]; then
    mkdir -p "$(dirname "$RECYCLARR_CONFIG")"
    cp "$RECYCLARR_TEMPLATE" "$RECYCLARR_CONFIG"
    # 使用 perl 替代 sed，兼容 macOS 和 Linux
    perl -pi -e "s|\\\$\{SONARR_API_KEY\}|$SONARR_KEY|g" "$RECYCLARR_CONFIG"
    perl -pi -e "s|\\\$\{RADARR_API_KEY\}|$RADARR_KEY|g" "$RECYCLARR_CONFIG"
    perl -pi -e "s|\\\$\{SONARR_BASE_URL\}|http://${SONARR_HOSTNAME}:${SONARR_PORT}|g" "$RECYCLARR_CONFIG"
    perl -pi -e "s|\\\$\{RADARR_BASE_URL\}|http://${RADARR_HOSTNAME}:${RADARR_PORT}|g" "$RECYCLARR_CONFIG"
    log_success "Recyclarr 配置文件已生成 (每天凌晨3点自动同步)"

    log_info "执行 Recyclarr 首次同步..."
    docker exec "$RECYCLARR_HOSTNAME" recyclarr sync > /dev/null 2>&1
    log_success "Recyclarr 首次同步完成"
else
    log_warn "找不到 Recyclarr 模板 $RECYCLARR_TEMPLATE，跳过配置"
fi

# ==============================================================================
# 2. 导入中文字幕 Custom Formats (CF)
# ==============================================================================
import_cf() {
    local svc=$1; local port=$2; local key=$3
    log_info "导入中文字幕 CF 到 $svc..."

    run_python_api "$svc" "$port" "$key" "$SCRIPT_DIR/setup/custom-formats" <<'PYEOF'
import urllib.request, json, sys, os
svc, port, key, cf_dir = sys.argv[1:5]
base = f"http://localhost:{port}/api/v3"
hd = {"X-Api-Key": key, "Content-Type": "application/json"}

def api_call(ep, d=None, m="GET"):
    req = urllib.request.Request(f"{base}/{ep}", data=json.dumps(d).encode() if d else None, headers=hd, method=m)
    with urllib.request.urlopen(req) as r: return json.loads(r.read())

try:
    existing = {x["name"].lower(): x["id"] for x in api_call("customformat")}
    for f in os.listdir(cf_dir):
        if not f.endswith(".json"): continue
        with open(os.path.join(cf_dir, f), 'r') as j: data = json.load(j)
        name = data["name"]
        if name.lower() in existing:
            data["id"] = existing[name.lower()]
            api_call(f"customformat/{data['id']}", d=data, m="PUT")
        else:
            api_call("customformat", d=data, m="POST")
    print(f"  \033[32m[✓]\033[0m {svc} CF 导入完毕")

    # 设置 Profile 分数
    SCORES = {"Language: Chinese Simplified (CHS)": 100, "Language: Chinese Traditional (CHT)": 100, "Language: Chinese Subtitle": 50}
    cfs = {x["name"]: x["id"] for x in api_call("customformat")}
    profiles = api_call("qualityprofile")
    for p in profiles:
        items = p.get("formatItems", [])
        for name, score in SCORES.items():
            if name in cfs:
                found = next((i for i in items if i["format"] == cfs[name]), None)
                if found: found["score"] = score
                else: items.append({"format": cfs[name], "name": name, "score": score})
        p["formatItems"] = items
        api_call(f"qualityprofile/{p['id']}", d=p, m="PUT")
    print(f"  \033[32m[✓]\033[0m {svc} Profile 分数更新完毕")
except Exception as e:
    print(f"  \033[31m[✗]\033[0m {svc} CF 失败: {e}")
PYEOF
}

[ -n "$SONARR_KEY" ] && import_cf "sonarr" "$SONARR_PORT" "$SONARR_KEY"
[ -n "$RADARR_KEY" ] && import_cf "radarr" "$RADARR_PORT" "$RADARR_KEY"
[ -n "$WHISPARR_KEY" ] && import_cf "whisparr" "$WHISPARR_PORT" "$WHISPARR_KEY"
