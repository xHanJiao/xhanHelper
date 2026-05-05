# ── 动态工具 API 测试 ──
echo ""
echo "【T1】注册工具"
TOOL_RESP=$(curl -s -X POST "$BASE_URL/api/tools" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"name":"test_calc","description":"测试计算器","schema_json":"[{\"name\":\"add\",\"description\":\"加法\",\"parameters\":{\"type\":\"object\",\"properties\":{\"a\":{\"type\":\"number\"},\"b\":{\"type\":\"number\"}},\"required\":[\"a\",\"b\"]}}]"}')
TOOL_ID=$(echo "$TOOL_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
assert "注册成功返回 id" '[0-9a-f\-]{36}' "$TOOL_ID"
assert "初始状态为 draft" '"draft"' "$TOOL_RESP"

echo ""
echo "【T2】工具列表"
TOOL_LIST=$(curl -s "$BASE_URL/api/tools" -H "Authorization: Bearer $TOKEN")
assert "列表包含新工具" "$TOOL_ID" "$TOOL_LIST"

echo ""
echo "【T3】更新工具状态为 active"
curl -s -X PATCH "$BASE_URL/api/tools/$TOOL_ID" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"status":"active"}' > /dev/null
# 重新查一次确认状态
TOOL_GET=$(curl -s "$BASE_URL/api/tools" -H "Authorization: Bearer $TOKEN")
STATUS=$(echo "$TOOL_GET" | python3 -c "import sys,json; tools=json.load(sys.stdin); print(next((t['status'] for t in tools if t['id']=='$TOOL_ID'),''))" 2>/dev/null)
assert "状态改为 active" "active" "$STATUS"

echo ""
echo "【T4】删除工具"
DEL_RESP=$(curl -s -X DELETE "$BASE_URL/api/tools/$TOOL_ID" \
    -H "Authorization: Bearer $TOKEN")
assert "删除成功" "deleted" "$DEL_RESP"
