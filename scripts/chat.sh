#!/bin/bash
# 流式对话 — SSE Chat
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
source "$SCRIPT_DIR/env.sh"

if [ -z "$TOKEN" ]; then
    echo "❌ 未找到 token，请先运行 ./scripts/register-device.sh"
    exit 1
fi

MESSAGE="${1:-你好，介绍一下你自己}"
CONVERSATION_ID="${2:-}"

echo ">>> 发送消息: $MESSAGE"
if [ -n "$CONVERSATION_ID" ]; then
    echo ">>> 会话: $CONVERSATION_ID"
fi
echo "========================================="

# 构建 JSON body
if [ -n "$CONVERSATION_ID" ]; then
    BODY="{\"message\":\"$MESSAGE\",\"conversation_id\":\"$CONVERSATION_ID\"}"
else
    BODY="{\"message\":\"$MESSAGE\"}"
fi

# -N 禁用 curl 缓冲，确保逐行接收 SSE
curl -N -s -X POST "$BASE_URL/api/chat" \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d "$BODY" | while IFS= read -r line; do
    case "$line" in
        "event: message")
            read -r data
            # 提取 delta 字段
            delta=$(echo "$data" | sed 's/^data: //' | python3 -c "import sys,json; print(json.load(sys.stdin).get('delta',''))" 2>/dev/null)
            printf "%s" "$delta"
            ;;
        "event: tool_call")
            read -r data
            echo -e "\n\n🔧 $data"
            ;;
        "event: tool_result")
            read -r data
            echo -e "\n📋 $data"
            ;;
        "event: done")
            read -r data
            conv_id=$(echo "$data" | sed 's/^data: //' | python3 -c "import sys,json; print(json.load(sys.stdin).get('conversation_id',''))" 2>/dev/null)
            echo -e "\n\n✅ [conversation_id: $conv_id]"
            ;;
        "event: error")
            read -r data
            echo -e "\n\n❌ $data"
            ;;
    esac
done

echo ""
