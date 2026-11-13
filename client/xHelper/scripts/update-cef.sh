#!/bin/bash
# CEF 升级一条龙（见 docs/design/mac-apps/browser-chromium-migration.md 10.1 / 10.4）
#
# 用法：
#   scripts/update-cef.sh                          # 升到 index 里最新的 stable
#   scripts/update-cef.sh 159.0.3                  # 指定版本（+hash+chromium- 段自动补全）
#   scripts/update-cef.sh 159.0.3+g1a2b3c4+chromium-159.0.8200.1   # 已经写全就直接用
#
# 它只做三件事：回写 CEF_VERSION / CEF_VERSION_AT，跑 fetch-cef.sh 备好新版本产物，告诉你下一步。
# 不碰 .app（framework 由 ⌘B 的 Bundle CEF 增量重装）—— 手动装进正在跑的 app 只会让它下次启动崩。
#
# 升级后必做（少一步就等于没验）：
#   1. ⌘B 构建（重装 framework + 5 个 Helper）
#   2. scripts/cef-regression.sh
#   3. 人眼两项：谷歌登录输假邮箱、百度三站无验证码
set -euo pipefail

export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

SRCROOT="$(cd "$(dirname "$0")/.." && pwd)"
CACHE_ROOT="${CEF_CACHE_DIR:-$HOME/Library/Caches/xhanHelper-cef}"
INDEX_URL="https://cef-builds.spotifycdn.com/index.json"
INDEX_CACHE="${CEF_INDEX_CACHE:-$CACHE_ROOT/index.json}"

log() { echo "[cef] $*"; }

CURRENT="$(tr -d '[:space:]' < "$SRCROOT/CEF_VERSION")"
ARG="${1:-}"

# ── 版本解析：能自己补全就不联网（离线也能按写全的版本升级）
if [[ "$ARG" == *"+chromium-"* ]]; then
  NEW="$ARG"
else
  if [ -f "$INDEX_CACHE" ] && [ -n "$(find "$INDEX_CACHE" -mmin -1440 2>/dev/null)" ]; then
    log "用 24h 内的 index 缓存"
  else
    mkdir -p "$(dirname "$INDEX_CACHE")"
    log "拉 index.json"
    curl -fsSL --retry 2 -m 20 -o "$INDEX_CACHE.part" "$INDEX_URL"
    mv -f "$INDEX_CACHE.part" "$INDEX_CACHE"
  fi
  NEW="$(python3 - "$INDEX_CACHE" "$ARG" <<'PY'
import json, sys
path, want = sys.argv[1], sys.argv[2]
vs = [v for v in json.load(open(path))["macosarm64"]["versions"] if v.get("channel") == "stable"]
if want:
    hit = [v for v in vs if v["cef_version"].startswith(want)]
    if not hit:
        sys.exit("index 里没有匹配 %r 的 stable 版本（写全 x.y.z+gHASH+chromium-A.B.C.D 可跳过匹配）" % want)
    # 同前缀多命中取最新的
    print(max(hit, key=lambda v: tuple(int(n) for n in v["chromium_version"].split(".")))["cef_version"])
else:
    print(max(vs, key=lambda v: tuple(int(n) for n in v["chromium_version"].split(".")))["cef_version"])
PY
)"
fi

[ -n "$NEW" ] || { echo "error: [cef] 解析不出目标版本" >&2; exit 1; }
if [ "$NEW" = "$CURRENT" ]; then
  # 同一个版本不重写日期，否则"pin 于哪天"会被跑脚本这个动作污染
  log "已经是 ${NEW}，只补齐缓存（不改版本文件）"
  "$SRCROOT/scripts/fetch-cef.sh"
  exit 0
fi

log "CEF_VERSION: $CURRENT → $NEW"
printf '%s\n' "$NEW" > "$SRCROOT/CEF_VERSION"
date +%Y-%m-%d > "$SRCROOT/CEF_VERSION_AT"

# 下载 / 编 wrapper / 编 Helper / 造 5 个 Helper.app / 切 current 软链，全在这里
"$SRCROOT/scripts/fetch-cef.sh"

log "产物就绪。接下来："
log "  1) ⌘B 构建（Bundle CEF 会按版本号重装 framework + Helper）"
log "  2) scripts/cef-regression.sh"
log "  3) 人眼：谷歌登录假邮箱、百度三站无验证码"
log "  4) scripts/check-cef-version.sh --strict 确认戳已更新"
