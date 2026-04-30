#!/bin/bash
# API 自动化测试 — P1: 注册 + 聊天 + 会话 + 记账全流程
# 用法: make test
set -e

BASE_URL="${BASE_URL:-http://localhost:8080}"
PASS=0
FAIL=0

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
echo "【1/12】注册设备"
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
echo "【2/12】认证拦截"
RESP=$(curl -s -X POST "$BASE_URL/api/chat" \
    -H "Content-Type: application/json" \
    -d '{"message":"test"}' 2>&1)
assert "无 token 返回 401" '"error".*"missing token"' "$RESP"

# ── 3. 流式聊天（新会话） ──
echo ""
echo "【3/12】流式对话"
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
echo "【4/12】会话列表"
CONV_LIST=$(curl -s "$BASE_URL/api/conversations" \
    -H "Authorization: Bearer $TOKEN")
assert "列表包含新会话" "$CONV_ID" "$CONV_LIST"

# ── 5. 消息历史 ──
echo ""
echo "【5/12】消息历史"
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

# ═══════════════════════════════════════════
# P1: 记账工具全 function 测试
# ═══════════════════════════════════════════

# ── 6. create_bill — 通过对话创建账单 ──
echo ""
echo "【6/12】create_bill — 对话创建账单"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我记一笔账，午饭花了35块钱","conversation_id":"'$CONV_ID'"}' 2>&1)
assert "包含 tool_call 事件"     "event: tool_call"   "$SSE_OUT"
assert "function 为 create_bill" '"function":"create_bill"' "$SSE_OUT"
assert "包含 tool_result 事件"   "event: tool_result" "$SSE_OUT"
assert "tool_result success"     '"success":true'     "$SSE_OUT"

# ── 7. REST API 确认 create + 提取 bill id ──
echo ""
echo "【7/12】REST API — 确认账单入库"
BILLS=$(curl -s "$BASE_URL/api/bills" \
    -H "Authorization: Bearer $TOKEN")
assert "返回包含 item"   '"item"'   "$BILLS"
assert "返回包含 amount" '"amount"' "$BILLS"

BILL_ID=$(echo "$BILLS" | python3 -c "
import sys,json
bills = json.load(sys.stdin)
for b in bills:
    if b.get('item','') == '午饭' and b.get('amount') == 35:
        print(b['id'])
        break
" 2>/dev/null)

if [ -z "$BILL_ID" ]; then
    # 如果 LLM 没用"午饭"作为 item，用第一条账单的 id
    BILL_ID=$(echo "$BILLS" | python3 -c "import sys,json; print(json.load(sys.stdin)[0]['id'])" 2>/dev/null)
fi
assert "能提取到 bill id" '[0-9a-f\-]{36}' "$BILL_ID"

# ── 8. query_bills — 通过对话查询账单 ──
echo ""
echo "【8/12】query_bills — 对话查询账单"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我查一下最近的账单","conversation_id":"'$CONV_ID'"}' 2>&1)
assert "包含 tool_call 事件"    "event: tool_call"    "$SSE_OUT"
assert "function 为 query_bills" '"function":"query_bills"' "$SSE_OUT"
assert "tool_result success"    '"success":true'      "$SSE_OUT"

# ── 9. update_bill — 通过对话修改账单 ──
echo ""
echo "【9/12】update_bill — 对话修改账单"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我把 '$BILL_ID' 这笔账单改成40块钱","conversation_id":"'$CONV_ID'"}' 2>&1)
assert "包含 tool_call 事件"     "event: tool_call"    "$SSE_OUT"
assert "function 为 update_bill" '"function":"update_bill"' "$SSE_OUT"
assert "tool_result success"     '"success":true'      "$SSE_OUT"

# ── 10. REST API 确认修改 ──
echo ""
echo "【10/12】REST API — 确认金额已修改"
BILLS=$(curl -s "$BASE_URL/api/bills" \
    -H "Authorization: Bearer $TOKEN")
UPDATED_AMOUNT=$(echo "$BILLS" | python3 -c "
import sys,json
for b in json.load(sys.stdin):
    if b['id'] == '$BILL_ID':
        print(b['amount'])
        break
" 2>/dev/null)
assert "金额已改为 40" "40" "$UPDATED_AMOUNT"

# ── 11. delete_bill — 通过对话删除账单 ──
echo ""
echo "【11/12】delete_bill — 对话删除账单"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我把 '$BILL_ID' 这笔账单删掉","conversation_id":"'$CONV_ID'"}' 2>&1)
assert "包含 tool_call 事件"     "event: tool_call"    "$SSE_OUT"
assert "function 为 delete_bill" '"function":"delete_bill"' "$SSE_OUT"
assert "tool_result success"     '"success":true'      "$SSE_OUT"

# ── 12. REST API 确认删除 ──
echo ""
echo "【12/12】REST API — 确认账单已删除"
BILLS=$(curl -s "$BASE_URL/api/bills" \
    -H "Authorization: Bearer $TOKEN")
DELETED=$(echo "$BILLS" | python3 -c "
import sys,json
found = False
for b in json.load(sys.stdin):
    if b['id'] == '$BILL_ID':
        found = True
        break
print('found' if found else 'not_found')
" 2>/dev/null)
assert "账单已不在列表中" "not_found" "$DELETED"

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
