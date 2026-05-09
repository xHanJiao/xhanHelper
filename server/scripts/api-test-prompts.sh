#!/bin/bash
# 提示词管理 API 测试
# 依赖父脚本的: BASE_URL, TOKEN, PASS, FAIL, assert 函数

# ── 提示词列表（空）──
echo ""
echo "【35/41】提示词列表（空）"
PROMPT_LIST=$(curl -s "$BASE_URL/api/prompts" \
    -H "Authorization: Bearer $TOKEN")
assert "提示词列表为空数组"  '^\[\]$'  "$(echo "$PROMPT_LIST" | unwrap)"

# ── 创建 Flash 提示词 ──
echo ""
echo "【36/41】创建 Flash 提示词"
FLASH_P1=$(curl -s -X POST "$BASE_URL/api/prompts" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"prompt_type":"flash","system_prompt":"自定义Flash提示词：保持极简风格回复"}')
FLASH_P1_ID=$(echo "$FLASH_P1" | unwrap | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
assert "Flash 提示词 status=active"  '"active"'  "$(echo "$FLASH_P1" | unwrap | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin).get('status','')))")"

# ── 创建第二个 Flash 提示词（旧变 inactive）──
echo ""
echo "【37/41】创建第二个 Flash 提示词（旧变 inactive）"
FLASH_P2=$(curl -s -X POST "$BASE_URL/api/prompts" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"prompt_type":"flash","system_prompt":"第二版Flash提示词：加入 emoji 风格"}')
FLASH_P2_ID=$(echo "$FLASH_P2" | unwrap | python3 -c "import sys,json; print(json.load(sys.stdin).get('id',''))" 2>/dev/null)
FIRST_STATUS=$(curl -s "$BASE_URL/api/prompts?type=flash" \
    -H "Authorization: Bearer $TOKEN" | unwrap | python3 -c "import sys,json; items=json.load(sys.stdin); print([i['status'] for i in items if i['id']=='$FLASH_P1_ID'][0])")
assert "旧提示词变为 inactive"  "inactive"  "$FIRST_STATUS"

# ── 回滚到旧提示词 ──
echo ""
echo "【38/41】回滚到旧提示词"
ROLLBACK_RESP=$(curl -s -X POST "$BASE_URL/api/prompts/$FLASH_P1_ID/rollback" \
    -H "Authorization: Bearer $TOKEN")
FIRST_ACTIVE=$(curl -s "$BASE_URL/api/prompts?type=flash" \
    -H "Authorization: Bearer $TOKEN" | unwrap | python3 -c "import sys,json; items=json.load(sys.stdin); print([i['status'] for i in items if i['id']=='$FLASH_P1_ID'][0])")
assert "回滚后旧提示词变为 active"  "active"  "$FIRST_ACTIVE"

# ── 创建 Pro 提示词 ──
echo ""
echo "【39/41】创建 Pro 提示词"
PRO_P1=$(curl -s -X POST "$BASE_URL/api/prompts" \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    -d '{"prompt_type":"pro","system_prompt":"Pro模式自定义：深度思考时使用"}')
assert "Pro 提示词创建成功"  '"active"'  "$(echo "$PRO_P1" | unwrap | python3 -c "import sys,json; print(json.dumps(json.load(sys.stdin).get('status','')))")"

# ── 提示词列表含2个类型 ──
echo ""
echo "【40/41】提示词列表含2个类型"
PROMPT_LIST2=$(curl -s "$BASE_URL/api/prompts" \
    -H "Authorization: Bearer $TOKEN")
assert "包含 flash 类型"  '"prompt_type":"flash"'  "$PROMPT_LIST2"
assert "包含 pro 类型"    '"prompt_type":"pro"'    "$PROMPT_LIST2"

# ── 删除提示词 ──
echo ""
echo "【41/41】删除提示词"
DEL_PROMPT=$(curl -s -X DELETE "$BASE_URL/api/prompts/$FLASH_P2_ID" \
    -H "Authorization: Bearer $TOKEN")
assert "删除返回 deleted"  "deleted"  "$DEL_PROMPT"
