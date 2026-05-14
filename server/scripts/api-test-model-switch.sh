#!/bin/bash
# 模型切换测试 — 验证同会话内 Pro↔Flash 无缝切换
# Phase 1 统一工具调用后，两种模式共用 prompt engineering，消息格式一致
# 依赖父脚本的: BASE_URL, TOKEN, PASS, FAIL, assert 函数

# —— MS1: 先 Flash 后 Pro，同会话 ——
echo ""
echo "【MS1】模型切换 — Flash→Pro 同会话"
# 新会话，Flash 模式记账
SSE_OUT=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我记一笔，早餐豆浆油条8元，餐饮-早餐"}' 2>&1)
CONV_ID_MS=$(echo "$SSE_OUT" | sed -n 's/.*"conversation_id":"\([^"]*\)".*/\1/p')
assert "Flash: 包含 tool_call"      "event: tool_call"       "$SSE_OUT"
assert "Flash: create_bill 成功"    '"success":true'         "$SSE_OUT"
assert "Flash: 返回 conversation_id" '[0-9a-f\-]{36}'        "$CONV_ID_MS"

# 同一会话，切换到 Pro 模式
echo ""
echo "【MS2】模型切换 — Pro 模式同会话"
SSE_OUT2=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"帮我查一下今天早餐花了多少钱\",\"deep_mode\":true,\"conversation_id\":\"$CONV_ID_MS\"}" 2>&1)
assert "Pro: 包含 thought_delta"    "event: thought_delta"   "$SSE_OUT2"
assert "Pro: 包含 tool_call"        "event: tool_call"       "$SSE_OUT2"
assert "Pro: 包含 done"             "event: done"            "$SSE_OUT2"

# 切回 Flash 模式
echo ""
echo "【MS3】模型切换 — Pro→Flash 同会话"
SSE_OUT3=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"再帮我记一笔，午餐盖浇饭25元\",\"conversation_id\":\"$CONV_ID_MS\"}" 2>&1)
assert "切回 Flash: 包含 tool_call"  "event: tool_call"       "$SSE_OUT3"
assert "切回 Flash: 包含 done"       "event: done"            "$SSE_OUT3"
