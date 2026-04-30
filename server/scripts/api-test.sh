#!/bin/bash
# API 自动化测试 — 注册 → 聊天 → 会话列表 → 消息历史
# 用法: make test
set -e

BASE_URL="${BASE_URL:-http://localhost:8080}"
PASS=0
FAIL=0
CONV_ID=""

green() { echo -e "\033[32m$*\033[0m"; }
red()   { echo -e "\033[31m$*\033[0m"; }

assert() {
    local desc="$1"
    local expected="$2"
    local actual="$3"
    if echo "$actual" | grep -Eq "$expected"; then
        green "  ✅ $desc"
        PASS=$((PASS + 1))
    else
        red "  ❌ $desc"
        red "     期望包含: $expected"
        red "     实际响应: $(echo "$actual" | head -3)"
        FAIL=$((FAIL + 1))
    fi
}

# ── 1. 注册设备 ──
echo ""
echo "【1/5】注册设备"
RESP=$(curl -s -X POST "$BASE_URL/api/devices/register" \
    -H "Content-Type: application/json" \
    -d '{"device_name":"api-test","platform":"macos"}')
TOKEN=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('device_token',''))" 2>/dev/null)
DEVICE_ID=$(echo "$RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('device_id',''))" 2>/dev/null)

assert "返回 device_token" '[0-9a-f\-]{36}' "$TOKEN"
assert "返回 device_id"   '[0-9a-f\-]{36}' "$DEVICE_ID"

if [ -z "$TOKEN" ]; then
    red "无法获取 token，终止测试"
    exit 1
fi

# ── 2. 无 token 请求应被拒绝 ──
echo ""
echo "【2/5】认证拦截"
RESP=$(curl -s -X POST "$BASE_URL/api/chat" \
    -H "Content-Type: application/json" \
    -d '{"message":"test"}' 2>&1)
assert "无 token 返回 401" '"error".*"missing token"' "$RESP"

# ── 3. 流式聊天（新会话） ──
echo ""
echo "【3/5】流式对话"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"你好，我叫小明，请记住我的名字"}' 2>&1)
CONV_ID=$(echo "$SSE_OUT" | sed -n 's/.*"conversation_id":"\([^"]*\)".*/\1/p')

assert "包含 text_delta"   "delta"   "$SSE_OUT"
assert "包含 done 事件"   "done"     "$SSE_OUT"
assert "返回 conversation_id" '[0-9a-f\-]{36}' "$CONV_ID"

# ── 4. 会话列表 ──
echo ""
echo "【4/5】会话列表"
CONV_LIST=$(curl -s "$BASE_URL/api/conversations" \
    -H "Authorization: Bearer $TOKEN")

assert "列表包含新会话" "$CONV_ID" "$CONV_LIST"

# ── 5. 消息历史 ──
echo ""
echo "【5/5】消息历史"
MSGS=$(curl -s "$BASE_URL/api/conversations/$CONV_ID/messages" \
    -H "Authorization: Bearer $TOKEN")

USER_MSG=$(echo "$MSGS" | python3 -c "
import sys,json
for m in json.load(sys.stdin):
    if m['role']=='user': print(m['content'])
" 2>/dev/null)

ASST_MSG=$(echo "$MSGS" | python3 -c "
import sys,json
for m in json.load(sys.stdin):
    if m['role']=='assistant': print(m['content'])
" 2>/dev/null)

assert "用户消息已保存"         "小明"    "$USER_MSG"
assert "assistant 消息已保存"   "."       "$ASST_MSG"

# ── 结果汇总 ──
echo ""
echo "========================================="
if [ "$FAIL" -eq 0 ]; then
    green "  全部通过 ($PASS/$PASS)"
    echo "========================================="
    exit 0
else
    red "  $FAIL 项失败, $PASS 项通过"
    echo "========================================="
    exit 1
fi
