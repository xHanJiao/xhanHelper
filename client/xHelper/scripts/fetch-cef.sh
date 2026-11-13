#!/bin/bash
# CEF 二进制落地脚本（见 docs/design/mac-apps/browser-chromium-migration.md 6.1）
#
# 两件事：
#   1. prepare（默认）：按 CEF_VERSION 把发行包下到版本化共享缓存里，编 libcef_dll_wrapper，
#      编 Helper 可执行文件并造 5 个 Helper.app（主 / Alerts / GPU / Plugin / Renderer）。
#      多 worktree 共用一份。
#   2. install：把 framework + 5 个 Helper 拷进 .app 的 Frameworks 目录并 ad-hoc 签名。
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

# CEF 的五个 Helper（名字后缀 / bundle id 后缀），与 cef_variables.cmake 的 CEF_HELPER_APP_SUFFIXES 同构。
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
  # 不清 quarantine 的话签名必挂（第一次构建必踩）。
  # 本机自己打的包往往没有 quarantine 属性，xattr 返非零属正常，故忽略。
  xattr -dr com.apple.quarantine "$DIST" || true
  # 解完就删：一份 300MB 的 tar 留着没用（解出来的 ~2GB 才是要用的）
  rm -f "$ARCHIVE"
}

fetch_dist() {
  # 完整性判据同时看头文件和 framework 的 Info.plist：解包中断留下的半截目录不能当成"已就绪"
  if [ -f "$DIST/include/cef_app.h" ] && [ -f "$FRAMEWORK/Resources/Info.plist" ]; then return 0; fi
  [ -f "$ARCHIVE" ] || download
  extract
  if [ ! -f "$DIST/include/cef_app.h" ] || [ ! -f "$FRAMEWORK/Resources/Info.plist" ]; then
    echo "error: [cef] 解包不完整（缺 include/ 或 framework），删掉 $CACHE 重跑本脚本" >&2
    exit 1
  fi
}

build_wrapper() {
  [ -f "$WRAPPER_LIB" ] && return 0
  command -v cmake >/dev/null || { echo "error: [cef] 需要 cmake（brew install cmake ninja）" >&2; exit 1; }
  command -v ninja >/dev/null || { echo "error: [cef] 需要 ninja（brew install cmake ninja）" >&2; exit 1; }
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
  # 伪装注入（第 3/4 层）也在 helper 里：改了共享头必须重编，否则症状是"改了完全没生效"
  local disguise_hdr="$SRCROOT/xHelperMac/CEF/CefDisguise.h"
  if [ -f "$STUB" ] && [ "$STUB" -nt "$HELPER_SRC" ] && [ "$STUB" -nt "$disguise_hdr" ]; then return 0; fi
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
    # Helper 里的可执行文件是 stub 的拷贝，stub 重编过就得重造。
    # 用 `! -ot` 而不是 `-nt`：同一秒内编完 stub 再 cp，mtime 相等时 `-nt` 为假 → 每次构建都重造一遍
    [ "$HELPERS_DIR/$name.app/Contents/MacOS/$name" -ot "$STUB" ] && missing=1
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
  # 版本一致 **且** Helper 就是这次编出来的那份（改了 helper_main.mm 必须重装，否则 app 里一直是
  # 旧 Helper，症状是"改了完全没生效"）。装进去的二进制被 codesign 改过，不能直接 cmp → 记 sha。
  #
  # 戳文件**必须放在 .app 外面**：`Contents/Frameworks/` 下的一切都被 Xcode 当成"必须已签名的代码
  # 对象"，放个普通文件进去会让 CodeSign 直接失败（`code object is not signed at all`，B0 踩过）。
  # 位置：Xcode 构建用 TARGET_TEMP_DIR，手动跑回落到缓存目录；文件名按目标路径区分。
  local stamp_dir stamp helper_sha installed_sha layout_version
  stamp_dir="${CEF_INSTALL_STAMP_DIR:-${TARGET_TEMP_DIR:-$CACHE/stamps}}"
  mkdir -p "$stamp_dir"
  stamp="$stamp_dir/$(printf '%s' "$dest" | shasum -a 256 | cut -c1-16).sha"
  # 布局版本跟着"装进 app 的结构"走：改了 framework 布局（如转版本化）就 +1，老产物会重装一次
  layout_version="2"
  helper_sha="$layout_version:$(shasum -a 256 "$STUB" | cut -d' ' -f1)"
  # 首次安装没有戳文件，`cat` 返非零属预期
  installed_sha="$(cat "$stamp" 2>/dev/null || true)"
  if [ -n "$version" ] &&
     [ "$(read_framework_version "$dest/Chromium Embedded Framework.framework")" = "$version" ] &&
     [ "$installed_sha" = "$helper_sha" ]; then
    log "已是 ${version}，跳过安装"
    return 0
  fi

  mkdir -p "$dest"
  rm -f "$dest/.cef-helper-sha"   # 老版本误写进 bundle 的戳，留着会让 CodeSign 继续失败
  log "装进 $dest"
  rm -rf "$dest/Chromium Embedded Framework.framework"
  cp -R "$FRAMEWORK" "$dest/"

  # 转成**版本化布局**：CEF 发行包是扁平包（Resources/Info.plist），而 Xcode 在 macOS 上
  # 校验嵌入 framework 时只认 `Versions/Current/Resources/Info.plist`（非 shallow bundle，
  # 见 SWBTaskExecution 里的判定），不改就是：
  #   error: Framework ... did not contain an Info.plist
  local fw="$dest/Chromium Embedded Framework.framework"
  if [ ! -d "$fw/Versions/A" ]; then
    mkdir -p "$fw/Versions/A"
    mv "$fw/Chromium Embedded Framework" "$fw/Resources" "$fw/Libraries" "$fw/Versions/A/"
    ln -sfn A "$fw/Versions/Current"
    # 根上只留标准软链（CEF 运行时要按 Framework.framework/Libraries/... 找 dylib，所以它也留着）
    ln -sfn "Versions/Current/Chromium Embedded Framework" "$fw/Chromium Embedded Framework"
    ln -sfn "Versions/Current/Resources" "$fw/Resources"
    ln -sfn "Versions/Current/Libraries" "$fw/Libraries"
  fi
  for name in "${HELPER_NAMES[@]}"; do
    rm -rf "$dest/$name.app"
    cp -R "$HELPERS_DIR/$name.app" "$dest/"
  done
  # 同上：本机产物可能本来就没有 quarantine 属性，xattr 返非零属正常
  xattr -dr com.apple.quarantine "$dest/Chromium Embedded Framework.framework" "$dest"/*.app || true

  # 由里到外签名：框架内 dylib → 框架 → 各 Helper。
  # 用 Xcode 这次构建的签名身份（与 App 本体一致，否则后续到 Xcode 的 embedded binary
  # 校验里会出现"签名身份不一致"）；手动跑没有身份时退回 ad-hoc（只自用，D13 不公证）。
  local sign_id="${EXPANDED_CODE_SIGN_IDENTITY:--}"
  log "嵌套签名身份：$sign_id"
  local lib
  for lib in "$dest/Chromium Embedded Framework.framework/Libraries/"*.dylib; do
    codesign -f -s "$sign_id" --timestamp=none "$lib"
  done
  codesign -f -s "$sign_id" --timestamp=none "$dest/Chromium Embedded Framework.framework"
  for name in "${HELPER_NAMES[@]}"; do
    codesign -f -s "$sign_id" --timestamp=none "$dest/$name.app"
  done
  echo "$helper_sha" > "$stamp"
  log "签名完成"
}

case "${1:-}" in
  --install) install "${2:-}" ;;
  --print-dist) fetch_dist; echo "$DIST" ;;
  "") prepare ;;
  *) echo "usage: $0 [--install <Frameworks 目录> | --print-dist]" >&2; exit 2 ;;
esac
