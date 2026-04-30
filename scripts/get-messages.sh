#!/bin/bash
# 获取会话消息列表
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/env.sh"

if [ -z "$TOKEN" ]; then
    echo "❌ 未找到 token，请先运行 ./scripts/register-device.sh"
    exit 1
fi

CONVERSATION_ID="$1"
if [ -z "$CONVERSATION_ID" ]; then
    echo "用法: ./scripts/get-messages.sh <conversation_id>"
    echo ""
    echo "提示: 先用 ./scripts/chat.sh 发起对话获取 conversation_id"
    echo "      或用 ./scripts/list-conversations.sh 查看已有会话"
    exit 1
fi

echo ">>> 获取消息: conversation_id=$CONVERSATION_ID"
echo "========================================="

curl -s -X GET "$BASE_URL/api/conversations/$CONVERSATION_ID/messages" \
  -H "Authorization: Bearer $TOKEN" | python3 -m json.tool 2>/dev/null
