#!/bin/bash
# 右栏「统一 tab」方案原型离屏渲染：真 SwiftUI 渲成 PNG（不做 HTML 预览）。
#
# 用法：client/xHelper/scripts/snapshot-righttabs.sh [输出目录]
#       默认 $TMPDIR/righttabs-snapshot
#
# 只信**布局 / token**：这里是纯色近似材质，深色/浅色的最终观感以 Xcode 真机为准。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
OUT_DIR="${1:-${TMPDIR:-/tmp}/righttabs-snapshot}"
TMP="${TMPDIR:-/tmp}/righttabs-build"
APP="$TMP/RightTabsSnap.app"
BIN="$APP/Contents/MacOS/RightTabsSnap"

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources" "$OUT_DIR"

cat > "$APP/Contents/Info.plist" <<'PLIST'
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>CFBundleExecutable</key><string>RightTabsSnap</string>
<key>CFBundleIdentifier</key><string>local.righttabssnap</string>
<key>CFBundleName</key><string>RightTabsSnap</string>
<key>CFBundlePackageType</key><string>APPL</string>
</dict></plist>
PLIST

echo "编译 snapshot-righttabs.swift ..."
# 拷成 main.swift：swiftc 只认这个文件名里的顶层代码当入口
cp "$HERE/snapshot-righttabs.swift" "$TMP/main.swift"
xcrun swiftc -O -o "$BIN" "$TMP/main.swift" -framework SwiftUI -framework AppKit

"$BIN" "$OUT_DIR"
echo "截图目录: ${OUT_DIR}"
