#!/bin/bash
# 卡片条（ArtifactCardStrip / DocumentRefCard）离屏渲染自检：把真视图渲成一堆 PNG。
#
# 用法：client/xHelper/scripts/snapshot-cardstrip.sh [输出目录] [light|dark|both]
#       默认 $TMPDIR/cardstrip-snapshot + both
#
# 为什么要一个 .app 壳：视图用 Color("surfaceInput") 这类 token，靠 asset catalog 解析；
# 裸可执行文件的 Bundle.main 找不到 Assets.car，颜色会解析成透明（踩过）。所以 actool 编一份
# Assets.car、塞进最小 .app 里再跑。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
SHARED="$HERE/../xHelper/Shared"
ASSETS="$HERE/../xHelperMac/Assets.xcassets"
OUT_DIR="${1:-${TMPDIR:-/tmp}/cardstrip-snapshot}"
MODE="${2:-both}"
TMP="${TMPDIR:-/tmp}/cardstrip-build"
APP="$TMP/CardStripSnap.app"
BIN="$APP/Contents/MacOS/CardStripSnap"

[ -d "$ASSETS" ] || { echo "找不到 $ASSETS"; exit 1; }

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$OUT_DIR"

echo "编译 Assets.car ..."
xcrun actool "$ASSETS" --compile "$APP/Contents/Resources" --platform macosx \
  --minimum-deployment-target 13.0 --output-format human-readable-text >/dev/null

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>CardStripSnap</string>
<key>CFBundleIdentifier</key><string>local.cardstripsnap</string>
<key>CFBundleName</key><string>CardStripSnap</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
PLIST

echo "编译 snapshot-cardstrip.swift ..."
# 拷成 main.swift：swiftc 只认这个文件名里的顶层代码当入口（否则报 "expressions are not allowed at top level"）
cp "$HERE/snapshot-cardstrip.swift" "$TMP/main.swift"
xcrun swiftc -O -o "$BIN" "$TMP/main.swift" \
  "$SHARED/Views/ArtifactCardStrip.swift" \
  "$SHARED/Views/DocumentRefCard.swift" \
  "$SHARED/Views/ThumbRowLayout.swift" \
  "$SHARED/LocalArtifacts/DocumentRef.swift" \
  -framework SwiftUI -framework AppKit

"$BIN" "$OUT_DIR" "$MODE"
echo "截图目录: ${OUT_DIR} (mode: ${MODE})"
