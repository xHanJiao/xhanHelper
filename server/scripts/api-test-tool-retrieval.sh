#!/bin/bash
# 工具检索测试 — 验证 tool_retriever.search 动态发现工具
# 依赖父脚本的: BASE_URL, TOKEN, PASS, FAIL, assert 函数

# —— TR1: tool_retriever 搜索并发现工具 ——
echo ""
echo "【TR1】tool_retriever — 搜索并发现记账工具"
SSE_OUT=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我记一笔，咖啡35元，餐饮-外卖"}' 2>&1)
assert "调用 tool_retriever.search" '"function":"search"' "$SSE_OUT"
assert "发现 bookkeeping 工具"       "bookkeeping"          "$SSE_OUT"
assert "调用 create_bill"            '"function":"create_bill"' "$SSE_OUT"
assert "记账成功"                     '"success":true'       "$SSE_OUT"

# —— TR2: tool_retriever 搜索笔记工具 ——
echo ""
echo "【TR2】tool_retriever — 搜索并发现笔记工具"
SSE_OUT=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我记一条笔记，下周要参加技术分享会"}' 2>&1)
assert "调用 tool_retriever.search" '"function":"search"' "$SSE_OUT"
assert "发现 note 工具"             "note"               "$SSE_OUT"
assert "调用 record_note"           '"function":"record_note"' "$SSE_OUT"
assert "笔记创建成功"                '"success":true'      "$SSE_OUT"

# —— TR3: tool_retriever 搜索智能体工具 ——
echo ""
echo "【TR3】tool_retriever — 搜索并发现智能体工具"
SSE_OUT=$(curl -N -s --max-time 60 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我查一下现在有哪些智能体"}' 2>&1)
assert "调用 tool_retriever.search"  '"function":"search"' "$SSE_OUT"
assert "发现 agent 工具"             "agent"               "$SSE_OUT"
assert "调用 list_agents"            '"function":"list_agents"' "$SSE_OUT"
