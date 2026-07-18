#!/bin/bash
# 客户端工具桥接手工联调 — 模拟 Mac 客户端行为：
#   1. 带 client_tools 发起 SSE 对话
#   2. 监听 client_tool_call 事件，提取 call_id
#   3. 回传假的工具执行结果（POST /api/chat/tool-result）
#   4. 验证 LLM 拿到结果后继续推理直到 done
#
# 前置：服务器已启动，LLM_API_KEY 已生效
# 用法: bash scripts/client-tool-e2e.sh
set -e

BASE_URL="${BASE_URL:-http://localhost:8080}"
TS=$(date +%s)
USERNAME="mcptest_$TS"
SSE_LOG=$(mktemp /tmp/client-tool-sse.XXXXXX)

green() { echo -e "\033[32m$*\033[0m"; }
red()   { echo -e "\033[31m$*\033[0m"; }

echo "【1/5】注册测试用户 $USERNAME"
TOKEN=$(curl -s -X POST "$BASE_URL/api/auth/register" \
    -H "Content-Type: application/json" \
    -d "{\"username\":\"$USERNAME\",\"password\":\"test123\"}" \
    | python3 -c "import sys,json; print(json.load(sys.stdin)['data']['token'])")
if [ -z "$TOKEN" ]; then red "注册失败"; exit 1; fi
green "  ✅ token 获取成功"

echo "【2/5】发起带 client_tools 的 SSE 对话"
cat > /tmp/client-tool-payload.json <<'EOF'
{
  "message": "请用浏览器打开百度首页 https://www.baidu.com ，并告诉我页面标题",
  "client_tools": [
    {
      "server_name": "browser",
      "name": "browser_navigate",
      "description": "在用户的 Mac 本地浏览器中打开指定 URL，返回页面标题等信息",
      "parameters": {
        "type": "object",
        "properties": {
          "url": {"type": "string", "description": "要打开的 URL"}
        },
        "required": ["url"]
      }
    }
  ]
}
EOF
curl -sN -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d @/tmp/client-tool-payload.json > "$SSE_LOG" &
SSE_PID=$!

echo "【3/5】等待 client_tool_call 事件（最多 60s）..."
CALL_ID=""
for i in $(seq 1 60); do
    if grep -q "^event: client_tool_call" "$SSE_LOG"; then
        CALL_ID=$(python3 - "$SSE_LOG" <<'PYEOF'
import sys, json
event = None
for line in open(sys.argv[1]):
    line = line.strip()
    if line.startswith("event: "):
        event = line[7:]
    elif line.startswith("data: ") and event == "client_tool_call":
        d = json.loads(line[6:])
        print(d["call_id"])
        print("TOOL:" + d["tool_name"], file=sys.stderr)
        print("ARGS:" + json.dumps(d["arguments"], ensure_ascii=False), file=sys.stderr)
        break
PYEOF
)
        break
    fi
    if ! kill -0 $SSE_PID 2>/dev/null && ! grep -q "^event: client_tool_call" "$SSE_LOG"; then
        red "  ❌ SSE 流提前结束，未收到 client_tool_call。完整输出："
        cat "$SSE_LOG"
        exit 1
    fi
    sleep 1
done
if [ -z "$CALL_ID" ]; then
    red "  ❌ 60s 内未收到 client_tool_call。当前 SSE 输出："
    cat "$SSE_LOG"
    kill $SSE_PID 2>/dev/null || true
    exit 1
fi
green "  ✅ 收到 client_tool_call, call_id=$CALL_ID"

echo "【4/5】模拟客户端回传工具结果"
RESP=$(curl -s -X POST "$BASE_URL/api/chat/tool-result" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"call_id\":\"$CALL_ID\",\"result\":\"{\\\"page_title\\\":\\\"百度一下，你就知道\\\",\\\"status\\\":\\\"loaded\\\"}\"}")
if echo "$RESP" | grep -q '"ok"'; then
    green "  ✅ 回传成功: $RESP"
else
    red "  ❌ 回传失败: $RESP"
    kill $SSE_PID 2>/dev/null || true
    exit 1
fi

echo "【5/5】等待对话完成（最多 60s）..."
for i in $(seq 1 60); do
    grep -q "^event: done" "$SSE_LOG" && break
    sleep 1
done
kill $SSE_PID 2>/dev/null || true

echo ""
echo "======== SSE 事件序列 ========"
grep "^event: " "$SSE_LOG" | uniq -c
echo ""
echo "======== 最终回复 ========"
python3 - "$SSE_LOG" <<'PYEOF'
import sys, json
event, reply = None, []
for line in open(sys.argv[1]):
    line = line.rstrip("\n")
    if line.startswith("event: "):
        event = line[7:]
    elif line.startswith("data: ") and event == "message":
        try:
            reply.append(json.loads(line[6:])["delta"])
        except Exception:
            pass
print("".join(reply) or "（无文本回复）")
PYEOF
echo ""

if grep -q "^event: done" "$SSE_LOG"; then
    green "✅ 端到端联调通过：client_tool_call → tool-result 回传 → LLM 继续推理 → done"
else
    red "❌ 未收到 done 事件，完整日志: $SSE_LOG"
    exit 1
fi

# 附加验证：重复回传应被拒绝（404）
echo ""
echo "【附加】重复回传同一 call_id（应 404）"
CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$BASE_URL/api/chat/tool-result" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"call_id\":\"$CALL_ID\",\"result\":\"{}\"}")
if [ "$CODE" = "404" ]; then
    green "  ✅ 重复回传被拒绝 (404)"
else
    red "  ❌ 期望 404，实际 $CODE"
fi

rm -f "$SSE_LOG" /tmp/client-tool-payload.json
