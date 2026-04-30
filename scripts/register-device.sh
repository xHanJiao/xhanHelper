#!/bin/bash
# 注册设备 — 获取 device_token
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/env.sh"

DEVICE_NAME="${1:-test-device}"
PLATFORM="${2:-macos}"

echo ">>> 注册设备: name=$DEVICE_NAME, platform=$PLATFORM"
RESP=$(curl -s -X POST "$BASE_URL/api/devices/register" \
  -H "Content-Type: application/json" \
  -d "{\"device_name\":\"$DEVICE_NAME\",\"platform\":\"$PLATFORM\"}")

echo "$RESP" | python3 -m json.tool 2>/dev/null || echo "$RESP"

# 提取 token 并缓存
TOKEN=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['device_token'])" 2>/dev/null)
if [ -n "$TOKEN" ]; then
    echo "$TOKEN" > "$TOKEN_FILE"
    echo ""
    echo "✅ token 已保存到 $TOKEN_FILE"
    echo "   TOKEN = $TOKEN"
else
    echo ""
    echo "❌ 未能提取 token"
    exit 1
fi
