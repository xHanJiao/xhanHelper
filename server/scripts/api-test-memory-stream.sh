#!/bin/bash
# 记忆提取流式测试 — 验证 extract-memories/stream SSE 端点
# 依赖父脚本的: BASE_URL, TOKEN, PASS, FAIL, assert 函数, CONV_ID

# —— MX1: 先通过对话创建一些可提取的记忆内容 ——
echo ""
echo "【MX1】记忆流式提取 — 准备测试数据"
SSE_OUT=$(curl -N -s --max-time 30 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"我叫小红，今年28岁，在北京工作，喜欢跑步和游泳\",\"conversation_id\":\"$CONV_ID\"}" 2>&1)
assert "测试数据: 包含 done" "event: done" "$SSE_OUT"

# —— MX2: 流式提取记忆 SSE ——
echo ""
echo "【MX2】记忆流式提取 — SSE 事件"
STREAM_OUT=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/conversations/$CONV_ID/extract-memories/stream" \
    -H "Authorization: Bearer $TOKEN" 2>&1)
# 验证 SSE 事件类型
if echo "$STREAM_OUT" | grep -q "event: status"; then
    green "  ✅ 包含 status 事件"
    PASS=$((PASS + 1))
else
    red "  ❌ 包含 status 事件"
    FAIL=$((FAIL + 1))
fi
if echo "$STREAM_OUT" | grep -q "event: done"; then
    green "  ✅ 包含 done 事件"
    PASS=$((PASS + 1))
else
    red "  ❌ 包含 done 事件"
    FAIL=$((FAIL + 1))
fi

# —— MX3: 流式提取应产生记忆记录 ——
echo ""
echo "【MX3】记忆流式提取 — 验证结果入库"
# 提取是异步的，稍等一下
sleep 3
MEM_LIST=$(curl -s "$BASE_URL/api/memories?limit=5" -H "Authorization: Bearer $TOKEN")
assert "流式提取后存在记忆" "memory_value" "$MEM_LIST"
