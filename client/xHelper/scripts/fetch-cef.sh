#!/bin/bash
# CEF 二进制落地脚本（见 docs/design/mac-apps/browser-chromium-migration.md 6.1）
#
# 两件事：
#   1. prepare（默认）：按 CEF_VERSION 把发行包下到版本化共享缓存里，编 libcef_dll_wrapper，
#      编 Helper 可执行文件并造 4 个 Helper.app。多 worktree 共用一份。
#   2. install：把 framework + 4 个 Helper 拷进 .app 的 Frameworks 目录并 ad-hoc 签名。
#      Xcode 的两个 Run Script（"Prepare CEF" / "Bundle CEF"）就是这两件事。
#
# 手动用：scripts/fetch-cef.sh                 # 只准备缓存
#         scripts/fetch-cef.sh --install "$APP/Contents/Frameworks"
# 逃生口：CEF_CACHE_DIR=/somewhere scripts/fetch-cef.sh   （换缓存根，调试用）
set -euo pipefail

export PATH="/opt/homebrew/bin:/usr/local/bin:$PATH"

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SRCROOT="${SRCROOT:-$(cd "$SCRIPT_DIR/.." && pwd)}"

VERSION="$(tr -d '[:space:]' < "$SRCROOT/CEF_VERSION")"
[ -n "$VERSION" ] || { echo "error: [cef] CEF_VERSION 是空的" >&2; exit 1; }

CACHE_ROOT="${CEF_CACHE_DIR:-$HOME/Library/Caches/xhanHelper-cef}"
CACHE="$CACHE_ROOT/$VERSION"
DIST_NAME="cef_binary_${VERSION}_macosarm64"
DIST="$CACHE/$DIST_NAME"
ARCHIVE="$CACHE_ROOT/$DIST_NAME.tar.bz2"
URL="https://cef-builds.spotifycdn.com/$DIST_NAME.tar.bz2"
FRAMEWORK="$DIST/Release/Chromium Embedded Framework.framework"
WRAPPER_LIB="$CACHE/lib/libcef_dll_wrapper.a"
STUB="$CACHE/bin/cef-helper-stub"
HELPER_SRC="$SRCROOT/CEF/helper_main.mm"
HELPERS_DIR="$CACHE/helpers"

# CEF 的四个 Helper（名字后缀 / bundle id 后缀），与 cef_variables.cmake 的 CEF_HELPER_APP_SUFFIXES 同构。
HELPER_NAMES=("xhanHelper Helper" "xhanHelper Helper (Alerts)" "xhanHelper Helper (GPU)" "xhanHelper Helper (Plugin)" "xhanHelper Helper (Renderer)")
HELPER_ID_SUFFIX=("" ".alerts" ".gpu" ".plugin" ".renderer")

log() { echo "[cef] $*"; }

download() {
  mkdir -p "$CACHE_ROOT"
  log "下载 ${DIST_NAME}（~300MB，只发生一次）"
  # -C - 断点续传；.part 先落临时文件，下完再改名，避免半截包被当成完整包
  curl -fL --retry 3 --retry-delay 2 -C - -o "$ARCHIVE.part" "$URL"
  mv -f "$ARCHIVE.part" "$ARCHIVE"
}

extract() {
  log "解包到 $CACHE"
  mkdir -p "$CACHE"
  tar -xjf "$ARCHIVE" -C "$CACHE"
  # 不清 quarantine 的话签名必挂（第一次构建必踩）
  xattr -dr com.apple.quarantine "$DIST" || true
  # 解完就删：一份 300MB 的 tar 留着没用（解出来的 ~2GB 才是要用的）
  rm -f "$ARCHIVE"
}

fetch_dist() {
  [ -f "$DIST/include/cef_app.h" ] && return 0
  [ -f "$ARCHIVE" ] || download
  extract
  [ -f "$DIST/include/cef_app.h" ] || { echo "error: [cef] 解包后仍找不到 include/cef_app.h" >&2; exit 1; }
}

build_wrapper() {
  [ -f "$WRAPPER_LIB" ] && return 0
  log "编 libcef_dll_wrapper（CEF 升级后必须重编，否则 ABI 不匹配直接崩）"
  local build="$CACHE/wrapper-build"
  cmake -S "$DIST" -B "$build" -G Ninja \
    -DCMAKE_BUILD_TYPE=Release \
    -DPROJECT_ARCH=arm64 \
    -DUSE_SANDBOX=OFF >/dev/null
  cmake --build "$build" --target libcef_dll_wrapper
  mkdir -p "$CACHE/lib"
  cp "$build/libcef_dll_wrapper/libcef_dll_wrapper.a" "$WRAPPER_LIB"
}

build_stub() {
  if [ -f "$STUB" ] && [ "$STUB" -nt "$HELPER_SRC" ]; then return 0; fi
  log "编 Helper 可执行文件（${HELPER_SRC}）"
  mkdir -p "$CACHE/bin"
  # 只链 wrapper，不链 framework：framework 由 CefScopedLibraryLoader 在运行时 dlopen
  # （链上它会被装成 @executable_path/../Frameworks/... 的相对依赖，Helper 嵌在
  #  Contents/Frameworks/<Helper>.app 里时必然找错路径）
  xcrun clang++ -std=c++20 -fobjc-arc -O2 -arch arm64 \
    -I"$DIST" -I"$DIST/include" \
    -x objective-c++ "$HELPER_SRC" \
    -L"$CACHE/lib" -lcef_dll_wrapper \
    -o "$STUB"
}

make_helpers() {
  local shipped version plist_template i name id_suffix app
  shipped="$FRAMEWORK/Resources/Info.plist"
  version="$(/usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$shipped")"
  plist_template="$DIST/tests/cefsimple/mac/helper-Info.plist.in"
  [ -f "$plist_template" ] || { echo "error: [cef] 找不到 helper plist 模板 $plist_template" >&2; exit 1; }

  local missing=0
  for name in "${HELPER_NAMES[@]}"; do
    [ -f "$HELPERS_DIR/$name.app/Contents/Info.plist" ] || missing=1
    # Helper 里的可执行文件是 stub 的拷贝，stub 重编过就得重造
    [ "$HELPERS_DIR/$name.app/Contents/MacOS/$name" -nt "$STUB" ] || missing=1
  done
  # CEF 升级换了版本号时 plist 也要跟着变 → 用版本文件当戳
  [ -f "$HELPERS_DIR/.version" ] && [ "$(cat "$HELPERS_DIR/.version")" != "$version" ] && missing=1
  [ "$missing" = "0" ] && return 0

  log "造 Helper.app ×${#HELPER_NAMES[@]}（版本 ${version}）"
  rm -rf "$HELPERS_DIR"
  mkdir -p "$HELPERS_DIR"
  for i in "${!HELPER_NAMES[@]}"; do
    name="${HELPER_NAMES[$i]}"
    id_suffix="${HELPER_ID_SUFFIX[$i]}"
    app="$HELPERS_DIR/$name.app"
    mkdir -p "$app/Contents/MacOS"
    cp "$STUB" "$app/Contents/MacOS/$name"
    sed -e "s|\${EXECUTABLE_NAME}|$name|g" \
        -e "s|\${PRODUCT_NAME}|$name|g" \
        -e "s|\${BUNDLE_ID_SUFFIX}|$id_suffix|g" \
        -e "s|\${VERSION_SHORT}|$version|g" \
        -e "s|org.cef.cefsimple.helper|xhanx.cn.xHelperMac.helper|g" \
        "$plist_template" > "$app/Contents/Info.plist"
    printf 'APPL????' > "$app/Contents/PkgInfo"
  done
  echo "$version" > "$HELPERS_DIR/.version"
}

prepare() {
  fetch_dist
  build_wrapper
  build_stub
  make_helpers
  # 稳定入口：pbxproj 里的路径不写版本号，升级只用改 CEF_VERSION。
  # current → 版本目录（人看）；dist → 发行包根（build setting 要的是它，里面才有 include/）
  ln -sfn "$CACHE" "$CACHE_ROOT/current"
  ln -sfn "$DIST" "$CACHE/dist"
  log "就绪：$CACHE"
}

# framework 的 Info.plist 版本是"装进去的到底是哪个版本"的权威来源（与 10.2 的 A 档检查同源）
read_framework_version() {
  local plist="$1/Resources/Info.plist"
  [ -f "$plist" ] || return 0
  # 取不到（plist 缺 key）就当没装过，重装一遍最坏也只是几秒
  /usr/bin/plutil -extract CFBundleShortVersionString raw -o - "$plist" 2>/dev/null || true
}

install() {
  local dest="$1"
  [ -n "$dest" ] || { echo "error: [cef] --install 需要目标目录（.app/Contents/Frameworks）" >&2; exit 1; }
  prepare

  # 增量：每次构建都重拷 300MB 太浪费，版本一致就跳过（Bundle CEF 是 alwaysOutOfDate）
  local version
  version="$(read_framework_version "$FRAMEWORK")"
  if [ -n "$version" ] &&
     [ "$(read_framework_version "$dest/Chromium Embedded Framework.framework")" = "$version" ] &&
     [ -d "$dest/${HELPER_NAMES[0]}.app" ]; then
    log "已是 ${version}，跳过安装"
    return 0
  fi

  mkdir -p "$dest"
  log "装进 $dest"
  rm -rf "$dest/Chromium Embedded Framework.framework"
  cp -R "$FRAMEWORK" "$dest/"
  for name in "${HELPER_NAMES[@]}"; do
    rm -rf "$dest/$name.app"
    cp -R "$HELPERS_DIR/$name.app" "$dest/"
  done
  xattr -dr com.apple.quarantine "$dest/Chromium Embedded Framework.framework" "$dest"/*.app || true

  # 由里到外 ad-hoc 签名：框架内的 dylib → 框架 → 各 Helper。
  # 只自用不分发（D13），不引 Developer ID、不公证，所以用 - 而不是证书名。
  local lib
  for lib in "$dest/Chromium Embedded Framework.framework/Libraries/"*.dylib; do
    codesign -f -s - --timestamp=none "$lib"
  done
  codesign -f -s - --timestamp=none "$dest/Chromium Embedded Framework.framework"
  for name in "${HELPER_NAMES[@]}"; do
    codesign -f -s - --timestamp=none "$dest/$name.app"
  done
  log "签名完成"
}

case "${1:-}" in
  --install) install "${2:-}" ;;
  --print-dist) fetch_dist; echo "$DIST" ;;
  "") prepare ;;
  *) echo "usage: $0 [--install <Frameworks 目录> | --print-dist]" >&2; exit 2 ;;
esac
