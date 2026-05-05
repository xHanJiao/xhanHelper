# ── AgentTool 测试：通过对话创建/查看/删除智能体 ──
echo ""
echo "【A1】create_agent — 对话创建智能体"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"帮我创建一个智能体，名叫「测试小助手」，它的 system_prompt 是「你是一个友善的助手，用简洁的语言回复」\"}" 2>&1)
assert "包含 tool_call 事件"     "event: tool_call"            "$SSE_OUT"
assert "function 为 create_agent" '"function":"create_agent"' "$SSE_OUT"
assert "包含 tool_result"         "event: tool_result"          "$SSE_OUT"

echo ""
echo "【A2】list_agents — 对话列出智能体"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"帮我查一下现在有哪些智能体\"}" 2>&1)
assert "包含 tool_call 事件"    "event: tool_call"           "$SSE_OUT"
assert "function 为 list_agents" '"function":"list_agents"' "$SSE_OUT"

echo ""
echo "【A3】create_agent — 带详细要求创建"
SSE_OUT=$(curl -N -s -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"创建一个叫「记账专家」的智能体，system_prompt 是「你是记账高手，帮用户分析消费习惯」\"}" 2>&1)
assert "包含 tool_call 事件"     "event: tool_call"            "$SSE_OUT"
assert "function 为 create_agent" '"function":"create_agent"' "$SSE_OUT"
