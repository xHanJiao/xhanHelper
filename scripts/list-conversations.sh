#!/bin/bash
# 获取会话列表
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/env.sh"

if [ -z "$TOKEN" ]; then
    echo "❌ 未找到 token，请先运行 ./scripts/register-device.sh"
    exit 1
fi

echo ">>> 获取会话列表"
echo "========================================="

curl -s -X GET "$BASE_URL/api/conversations" \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool 2>/dev/null
