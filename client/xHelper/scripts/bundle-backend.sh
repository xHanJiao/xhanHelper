#!/bin/bash
# 把 Go 后端二进制（内嵌前端 dist）+ conf 打包进 macOS app 的 Contents/Resources/。
#
# Xcode Run Script 调用：环境变量（SRCROOT / TARGET_BUILD_DIR / ...）由 Xcode 提供。
# 手动调用（无签名）：scripts/bundle-backend.sh /path/to/xhanHelper.app
#
# 增量：上次成功打包后源码没变就直接跳过（stamp 记在 DERIVED_FILE_DIR）。
# 逃生口（User-Defined 构建设置，不是 scheme 的环境变量）：
#   SKIP_BUNDLE_BACKEND=1  跳过打包    FORCE_BUNDLE_BACKEND=1  忽略增量强制重编
set -euo pipefail

# Xcode Run Script 的 PATH 很干净，补上 brew / go 的常见位置
export PATH="/opt/homebrew/bin:/usr/local/bin:$HOME/go/bin:$PATH"

if [ "${SKIP_BUNDLE_BACKEND:-0}" = "1" ]; then
  echo "note: [bundle-backend] SKIP_BUNDLE_BACKEND=1，跳过"
  exit 0
fi

# 目标 Resources 目录：优先 Xcode 构建环境，其次命令行传入的 app 路径
if [ -n "${UNLOCALIZED_RESOURCES_FOLDER_PATH:-}" ]; then
  RESOURCES_DIR="${TARGET_BUILD_DIR}/${UNLOCALIZED_RESOURCES_FOLDER_PATH}"
elif [ "${1:-}" != "" ]; then
  case "$1" in
    *.app) RESOURCES_DIR="$1/Contents/Resources" ;;
    *)     RESOURCES_DIR="$1" ;;
  esac
else
  echo "error: [bundle-backend] 非 Xcode 环境，需要传入 app 路径" >&2
  exit 1
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRCROOT="${SRCROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"
REPO_ROOT="$(cd "$SRCROOT/../.." && pwd)"
SERVER_DIR="$REPO_ROOT/server"
FRONTEND_DIR="$REPO_ROOT/front-end"

OUT="$RESOURCES_DIR/xhan-helper"
STAMP="${DERIVED_FILE_DIR:-$SERVER_DIR/dist}/.bundle-backend.stamp"

# 增量判断：产物齐全 + 有 stamp + 源码没有比 stamp 更新的文件 → 跳过重编。
# 必须排除 node_modules 与产物目录（dist/logs/data），否则上次的输出文件自身
# 会比 stamp 新，永远误判「有变化」。
if [ "${FORCE_BUNDLE_BACKEND:-0}" != "1" ] \
   && [ -f "$OUT" ] && [ -f "$RESOURCES_DIR/conf/config.yaml" ] && [ -f "$STAMP" ]; then
  CHANGED="$(find "$SERVER_DIR" "$FRONTEND_DIR" \
      \( -type d \( -name node_modules -o -name dist -o -name logs -o -name data \) -prune \) -o \
      -type f -newer "$STAMP" -print -quit 2>/dev/null || true)"
  if [ -z "$CHANGED" ]; then
    echo "==> [bundle-backend] 源码未变，跳过重编"
    exit 0
  fi
fi

# 全新克隆没装依赖时给个明确提示，别让 npm 抛一脸错
if [ ! -d "$FRONTEND_DIR/node_modules" ]; then
  echo "error: [bundle-backend] 缺 $FRONTEND_DIR/node_modules" >&2
  echo "       先跑：cd \"$FRONTEND_DIR\" && npm ci" >&2
  exit 1
fi

echo "==> [bundle-backend] 构建前端 + 本地版后端（内嵌前端）"
make -C "$SERVER_DIR" build-frontend
make -C "$SERVER_DIR" local-build

# 按构建配置选本地 conf（第 4 期）：Debug → dev（18924 + xhanHelper-dev），其余 → prod
case "${CONFIGURATION:-Release}" in
  Debug*) LOCAL_CONF="config.local.dev.yaml"; ENV_NAME="dev" ;;
  *)      LOCAL_CONF="config.local.yaml";     ENV_NAME="prod" ;;
esac
echo "==> [bundle-backend] 构建配置 ${CONFIGURATION:-（命令行/未设）} → 本地环境 $ENV_NAME（$LOCAL_CONF）"

echo "==> [bundle-backend] 拷入 $RESOURCES_DIR"
mkdir -p "$RESOURCES_DIR/conf"
cp -f "$SERVER_DIR/dist/xhan-helper" "$OUT"
cp -f "$SERVER_DIR/conf/config.yaml" "$SERVER_DIR/conf/$LOCAL_CONF" "$RESOURCES_DIR/conf/"
chmod +x "$OUT"

# 嵌套二进制必须单独签名——Xcode 不会自动签脚本拷进去的可执行文件。
# 第 5 期公证前再补 --options runtime --timestamp。
if [ -n "${EXPANDED_CODE_SIGN_IDENTITY:-}" ] && [ "${CODE_SIGNING_ALLOWED:-YES}" != "NO" ]; then
  echo "==> [bundle-backend] 签名嵌套二进制（${EXPANDED_CODE_SIGN_IDENTITY}）"
  codesign --force --sign "$EXPANDED_CODE_SIGN_IDENTITY" "$OUT"
else
  echo "warn: [bundle-backend] 无签名身份（非 Xcode 构建或已禁签），跳过嵌套签名" >&2
fi

# 成功后才写 stamp：失败时下次会重试
mkdir -p "$(dirname "$STAMP")"
touch "$STAMP"

echo "==> [bundle-backend] 完成：$OUT"
