#!/bin/bash
# Agent 自动路由测试 — 验证 pipeline autoRoute 匹配智能体
# 依赖父脚本的: BASE_URL, TOKEN, PASS, FAIL, assert 函数

# —— AR1: 创建智能体供路由使用 ——
echo ""
echo "【AR1】autoRoute — 创建路由目标智能体"
AGENT_RESP=$(curl -s -X POST "$BASE_URL/api/agents" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"翻译助手","system_prompt":"你是一个专业翻译，把用户输入翻译成英文，只输出英文译文"}')
ROUTE_AGENT_ID=$(echo "$AGENT_RESP" | unwrap | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
assert "创建翻译助手成功" '[0-9a-f\-]{36}' "$ROUTE_AGENT_ID"

# —— AR2: 新会话首条消息触发 autoRoute ——
# autoRoute 在 msg_count+1 ∈ [1,3,6] 时触发，新会话首条消息 count+1==1 会触发
echo ""
echo "【AR2】autoRoute — 新会话触发异步路由"
SSE_OUT=$(curl -N -s --max-time 30 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"message":"帮我翻译：今天天气真好"}' 2>&1)
CONV_ID_AR=$(echo "$SSE_OUT" | sed -n 's/.*"conversation_id":"\([^"]*\)".*/\1/p')
assert "包含 done 事件" "event: done" "$SSE_OUT"
assert "返回 conversation_id" '[0-9a-f\-]{36}' "$CONV_ID_AR"
# 等待异步 autoRoute 完成
sleep 5

# —— AR3: 同一会话第二条消息，验证 agent 绑定后不影响对话 ——
echo ""
echo "【AR3】autoRoute — 绑定后第二消息正常"
SSE_OUT2=$(curl -N -s --max-time 30 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"再翻译：晚安\",\"conversation_id\":\"$CONV_ID_AR\"}" 2>&1)
assert "第二条消息 done" "event: done" "$SSE_OUT2"

# —— AR4: 直接指定 agent_id 参数 ——
echo ""
echo "【AR4】agent_id — 直接指定智能体"
SSE_OUT3=$(curl -N -s --max-time 30 -X POST "$BASE_URL/api/chat" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d "{\"message\":\"hello\",\"agent_id\":\"$ROUTE_AGENT_ID\"}" 2>&1)
assert "指定 agent 后 done" "event: done" "$SSE_OUT3"

# —— AR5: 清理 ——
echo ""
echo "【AR5】autoRoute — 清理测试智能体"
DEL_RESP=$(curl -s -X DELETE "$BASE_URL/api/agents/$ROUTE_AGENT_ID" \
    -H "Authorization: Bearer $TOKEN")
assert "删除翻译助手成功" "true" "$DEL_RESP"
