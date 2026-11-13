#!/bin/bash
# CEF 版本体检（见 docs/design/mac-apps/browser-chromium-migration.md 10.2）
#
# 两档，同一个脚本：
#   A 构建期：Xcode Run Script "Check CEF"（排在 Bundle CEF 之后）→ 只 warning，构建照常成功
#   B 发版期：scripts/check-cef-version.sh --strict → 超阈值非 0 退出
#
# 查三件事：
#   1. 落后程度：仓库 pin 的内核大版本 vs 最新 stable，以及 pin 的日期
#   2. 装进 app 的 framework 与 pin 是否一致（升级后没重装 → 实际跑的还是老内核）
#   3. 这个版本跑过 cef-regression.sh 没有（戳由那个脚本写）
#
# 离线 / 接口失败：只用旧缓存、跳过"该不该升级"这一条，永不阻塞构建。
# 手动用：scripts/check-cef-version.sh [--strict] [/path/to/xhanHelper.app]
set -uo pipefail

export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

STRICT=0
if [ "${1:-}" = "--strict" ]; then STRICT=1; shift; fi
APP="${1:-}"

SRCROOT="${SRCROOT:-$(cd "$(dirname "$0")/.." && pwd)}"
CACHE_ROOT="${CEF_CACHE_DIR:-$HOME/Library/Caches/xhanHelper-cef}"
INDEX_URL="https://cef-builds.spotifycdn.com/index.json"
INDEX_CACHE="${CEF_INDEX_CACHE:-$CACHE_ROOT/index.json}"
STAMP="${XH_CEF_REGRESSION_STAMP:-$CACHE_ROOT/regression-stamp.json}"

PINNED_FILE="$SRCROOT/CEF_VERSION"
PINNED_AT_FILE="$SRCROOT/CEF_VERSION_AT"
[ -f "$PINNED_FILE" ] || { echo "error: [cef] 找不到 $PINNED_FILE" >&2; exit 1; }
PINNED="$(tr -d '[:space:]' < "$PINNED_FILE")"
PINNED_AT="$(tr -d '[:space:]' < "$PINNED_AT_FILE" 2>/dev/null || true)"
PINNED_MAJOR="$(sed -n 's/.*+chromium-\([0-9][0-9]*\)\..*/\1/p' <<<"$PINNED")"
case "$PINNED_MAJOR" in
  ''|*[!0-9]*) echo "error: [cef] CEF_VERSION 形态不对（要 x.y.z+gHASH+chromium-A.B.C.D）：$PINNED" >&2; exit 1 ;;
esac

warn() { echo "warning: [cef] $*"; }
fail() { echo "error: [cef] $*" >&2; }

# 装进 app 的版本是唯一权威来源（§10.2 第 1 步）。
fw_version() {
  local fw="$1" p v
  for p in "$fw/Versions/Current/Resources/Info.plist" "$fw/Resources/Info.plist"; do
    [ -f "$p" ] || continue
    # plist 缺 key 时 plutil 返非零；下面按"没读到"处理，不吞成假版本号
    v="$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$p" 2>/dev/null || true)"
    [ -n "$v" ] && { echo "$v"; return 0; }
  done
  return 1
}

if [ -z "$APP" ] && [ -n "${BUILT_PRODUCTS_DIR:-}" ]; then
  APP="$BUILT_PRODUCTS_DIR/${FRAMEWORKS_FOLDER_PATH:-}"
  APP="${APP%/}"
fi
SHIPPED=""
if [ -n "$APP" ]; then
  FW_DIR="$APP"
  case "$APP" in *.app) FW_DIR="$APP/Contents/Frameworks" ;; esac
  SHIPPED="$(fw_version "$FW_DIR/Chromium Embedded Framework.framework" || true)"
fi

# 24h 缓存；网络失败优先用旧缓存（A 档绝不能因为网挂掉构建）
refresh_index() {
  [ -f "$INDEX_CACHE" ] && [ -n "$(find "$INDEX_CACHE" -mmin -1440 2>/dev/null)" ] && return 0
  mkdir -p "$(dirname "$INDEX_CACHE")"
  if curl -fsSL --retry 1 -m 8 -o "$INDEX_CACHE.part" "$INDEX_URL"; then
    mv -f "$INDEX_CACHE.part" "$INDEX_CACHE"
    return 0
  fi
  rm -f "$INDEX_CACHE.part"
  [ -f "$INDEX_CACHE" ]
}

latest_stable() {
  python3 - "$INDEX_CACHE" <<'PY'
import json, sys
try:
    d = json.load(open(sys.argv[1]))
    vs = [v for v in d["macosarm64"]["versions"] if v.get("channel") == "stable"]
    v = max(vs, key=lambda x: tuple(int(n) for n in x["chromium_version"].split(".")))
    print(v["cef_version"], v["chromium_version"])
except Exception as e:
    print("ERROR %s" % e, file=sys.stderr)
    sys.exit(1)
PY
}

problems=()   # 每条都会打一行 warning
hard=0        # strict 下必须失败的程度

if refresh_index; then
  if read -r LATEST LATEST_CHROMIUM <<<"$(latest_stable 2>/dev/null)"; then
    LATEST_MAJOR="${LATEST_CHROMIUM%%.*}"
    GAP=$((LATEST_MAJOR - PINNED_MAJOR))
    [ "$GAP" -lt 0 ] && GAP=0   # pin 跑到 stable 前面（用 beta 试）不算落后
    DAYS=""
    if [ -n "$PINNED_AT" ]; then
      PINNED_EPOCH="$(date -j -f "%Y-%m-%d" "$PINNED_AT" +%s 2>/dev/null || true)"
      [ -n "$PINNED_EPOCH" ] && DAYS=$(( ($(date +%s) - PINNED_EPOCH) / 86400 ))
    fi
    if [ "$GAP" -ge 2 ] || [ -n "$DAYS" ] && [ "$DAYS" -ge 90 ]; then
      detail=""
      [ "$GAP" -ge 2 ] && detail="落后 ${GAP} 个大版本（${PINNED_MAJOR} → ${LATEST_MAJOR}）"
      if [ -n "$DAYS" ] && [ "$DAYS" -ge 90 ]; then
        [ -n "$detail" ] && detail="${detail}，"
        detail="${detail}已停更 ${DAYS} 天（pin 于 ${PINNED_AT}）"
      fi
      problems+=("${detail}；最新 stable ${LATEST}，升级：scripts/update-cef.sh")
      if [ "$GAP" -ge 4 ] || [ -n "$DAYS" ] && [ "$DAYS" -ge 180 ]; then
        hard=1
        problems+=("已到必须升级的程度（≥4 个大版本或 ≥180 天），建议本次就升")
      fi
    fi
  fi
else
  problems+=("无法检查更新（离线且无缓存）：只核对了 app 里的版本，跳过落后程度判断")
fi

# framework 与 pin 不一致 = 升级了版本文件但没重装（症状：以为换了内核，其实没有）
if [ -n "$SHIPPED" ]; then
  PINNED_SHORT="$(cut -d+ -f1 <<<"$PINNED")"
  SHIPPED_SHORT="$(cut -d. -f1-3 <<<"$SHIPPED")"
  PINNED_SHORT3="$(cut -d. -f1-3 <<<"$PINNED_SHORT")"
  if [ "$SHIPPED_SHORT" != "$PINNED_SHORT3" ]; then
    problems+=("装进 app 的是 ${SHIPPED}，仓库 pin 的是 ${PINNED_SHORT3}：Bundle CEF 没重装（重启 Xcode 构建或删 .app 后重来）")
    hard=1
  fi
  # 升级后没跑过伪装回归就发版，等于"服务端看到什么"没人验过
  if [ ! -s "$STAMP" ]; then
    problems+=("还没跑过 scripts/cef-regression.sh（升级或改伪装层后必跑）")
    hard=1
  else
    STAMPED="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("version",""))' "$STAMP" 2>/dev/null || true)"
    if [ "$STAMPED" != "$SHIPPED" ]; then
      problems+=("${SHIPPED} 是升级后首次构建，先跑 scripts/cef-regression.sh（上次通过的是 ${STAMPED:-未知}）")
    fi
  fi
fi

if [ "${#problems[@]}" -gt 0 ]; then
  for p in "${problems[@]}"; do warn "$p"; done
  if [ "$STRICT" = "1" ] && [ "$hard" = "1" ]; then
    fail "发版前检查未过（--strict）"
    exit 1
  fi
fi
exit 0
