#!/bin/bash
# 「全部关闭」按钮候选离屏渲染：真 SwiftUI 渲成 PNG（不做 HTML 预览）。
#
# 用法：client/xHelper/scripts/snapshot-closeall.sh [输出目录]
#       默认 $TMPDIR/closeall-snapshot
#
# 只信布局 / 图标语义 / token：背景是近似色块，最终观感以 Xcode 真机为准。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="${1:-${TMPDIR:-/tmp}/closeall-snapshot}"
TMP="${TMPDIR:-/tmp}/closeall-build"
APP="$TMP/CloseAllSnap.app"
BIN="$APP/Contents/MacOS/CloseAllSnap"

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$OUT_DIR"

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>CloseAllSnap</string>
<key>CFBundleIdentifier</key><string>local.closeallsnap</string>
<key>CFBundleName</key><string>CloseAllSnap</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
PLIST

echo "编译 snapshot-closeall.swift ..."
cp "$HERE/snapshot-closeall.swift" "$TMP/main.swift"
xcrun swiftc -O -o "$BIN" "$TMP/main.swift" -framework SwiftUI -framework AppKit

"$BIN" "$OUT_DIR"
echo "截图目录: ${OUT_DIR}"
