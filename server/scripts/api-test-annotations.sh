#!/bin/bash
# 文档划线批注 API 冒烟测试（P1 验收）
#
# 独立起一个 SQLite 实例（conf/config.smoke.yaml + data/xhan_helper_smoke.db），
# 不碰 dev 的 MySQL。文档没有 REST 创建接口（只由 LLM 工具建），所以直接插库。
#
# 抓的是【接线】问题：表/路由/JSON 形状/级联/越权/配额。
# 锚点算法本身由 service 包的单测（含不变量测试）负责，不在这里重复。
#
# 用法: ./scripts/api-test-annotations.sh
set -u

cd "$(dirname "$0")/.." || exit 1

PORT=8099
BASE_URL="http://localhost:$PORT"
SMOKE_CFG="conf/config.smoke.yaml"
SMOKE_DB="data/xhan_helper_smoke.db"
SMOKE_BIN="/tmp/xh-annotation-smoke-bin"
LOG=/tmp/xh-annotation-smoke.log
PASS=0
FAIL=0

green() { echo -e "\033[32m$*\033[0m"; }
red()   { echo -e "\033[31m$*\033[0m"; }

assert() {
    local desc="$1" expected="$2" actual="$3"
    if echo "$actual" | grep -Eq -- "$expected"; then
        green "  ✅ $desc"
        PASS=$((PASS + 1))
    else
        red "  ❌ $desc"
        red "     期望匹配: $expected"
        red "     实际响应: $(echo "$actual" | head -c 400)"
        FAIL=$((FAIL + 1))
    fi
}

cleanup() {
    if [ -n "${SERVER_PID:-}" ]; then
        kill "$SERVER_PID" 2>/dev/null
        wait "$SERVER_PID" 2>/dev/null   # 回收子进程，避免 shell 打印 "Terminated"
    fi
    rm -f "$SMOKE_CFG" "$SMOKE_BIN"
    echo ""
    echo "清理：临时配置与二进制已删除（$SMOKE_DB 保留，便于事后翻查）"
}
trap cleanup EXIT

# 直接跑编译好的二进制，不用 go run —— go run 只杀掉父进程，
# 编译出的子进程会留在端口上，下一次跑就会静默测到旧实例。
echo "【0】编译并启动独立实例（SQLite，端口 ${PORT}）"
if lsof -nP -iTCP:"$PORT" -sTCP:LISTEN >/dev/null 2>&1; then
    red "端口 ${PORT} 已被占用（疑似上次残留），先清掉再跑："
    lsof -nP -iTCP:"$PORT" -sTCP:LISTEN
    exit 1
fi
go build -o "$SMOKE_BIN" ./cmd/xhan-helper/ || { red "编译失败"; exit 1; }

rm -f "$SMOKE_DB"
cat > "$SMOKE_CFG" <<EOF
server:
  port: $PORT
  registration_enabled: true
db:
  driver: sqlite
  database: $SMOKE_DB
EOF

if [ -f .env ]; then set -a; . ./.env; set +a; fi
APP_ENV=smoke "$SMOKE_BIN" > "$LOG" 2>&1 &
SERVER_PID=$!

for _ in $(seq 1 40); do
    sleep 1
    curl -s -o /dev/null "$BASE_URL/api/auth/register" && break
done
if ! kill -0 "$SERVER_PID" 2>/dev/null; then
    red "服务启动失败，日志尾部："
    tail -20 "$LOG"
    exit 1
fi
assert "服务已就绪" "1" "1"
assert "document_annotations 表已建" "document_annotations" "$(sqlite3 "$SMOKE_DB" '.tables')"

echo ""
echo "【1】注册两个用户"
TOKEN=$(curl -s -X POST "$BASE_URL/api/auth/register" -H 'Content-Type: application/json' \
    -d '{"username":"annosmoke1","password":"test123"}' | jq -r '.data.token // empty')
TOKEN2=$(curl -s -X POST "$BASE_URL/api/auth/register" -H 'Content-Type: application/json' \
    -d '{"username":"annosmoke2","password":"test123"}' | jq -r '.data.token // empty')
assert "用户 1 拿到 token" '[0-9a-f\-]{36}' "$TOKEN"
assert "用户 2 拿到 token" '[0-9a-f\-]{36}' "$TOKEN2"
[ -z "$TOKEN" ] && { red "无法获取 token，终止"; exit 1; }
USER_ID=$(sqlite3 "$SMOKE_DB" "select id from users where username='annosmoke1';")

echo ""
echo "【2】直插一篇文档（重复短句 + 跨粗体）"
python3 - "$SMOKE_DB" "$USER_ID" <<'PY'
import sqlite3, sys
db, uid = sys.argv[1], sys.argv[2]
content = ("# 调研\n\n结论是明确的。\n\n中间垫话把两处候选推开。\n\n"
           "结论是明确的，但语境不同。\n\n而是**散落在 persona 渲染**三处。\n")
con = sqlite3.connect(db)
con.execute(
    "insert into documents (id,user_id,title,content,tags,created_at,updated_at)"
    " values (?,?,?,?,?,datetime('now'),datetime('now'))",
    ("doc-smoke", uid, "冒烟", content, ""))
con.commit()
PY
assert "文档已入库" "^1$" "$(sqlite3 "$SMOKE_DB" "select count(*) from documents where id='doc-smoke';")"

API="$BASE_URL/api/documents/doc-smoke/annotations"

echo ""
echo "【3】建划线：重复短句两次 + 跨粗体一次"
# 第一处「结论是明确的」：DOM 前后文指向它
R1=$(curl -s -X POST "$API" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d '{"quote":"结论是明确的","note":"第一条","prefix":"# 调研","suffix":"。\n\n中间垫话"}')
# 第二处同名短句：前后文不同 —— 必须落在不同 offset
R2=$(curl -s -X POST "$API" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d '{"quote":"结论是明确的","note":"第二条","prefix":"中间垫话把两处候选推开。","suffix":"，但语境不同。"}')
# 跨粗体标记：DOM 文本不含 **
R3=$(curl -s -X POST "$API" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
    -d '{"quote":"而是散落在 persona 渲染","note":"","prefix":"，但语境不同。","suffix":"三处。"}')
ID1=$(echo "$R1" | jq -r '.data.id // empty')
ID2=$(echo "$R2" | jq -r '.data.id // empty')
OFF1=$(echo "$R1" | jq -r '.data.start_offset // empty')
OFF2=$(echo "$R2" | jq -r '.data.start_offset // empty')
assert "第一条创建成功" '"orphaned":false' "$R1"
assert "第二条创建成功" '"orphaned":false' "$R2"
assert "跨粗体创建成功" '"orphaned":false' "$R3"
assert "重复短句两次落在【不同】offset" "^1$" "$([ -n "$OFF1" ] && [ -n "$OFF2" ] && [ "$OFF1" != "$OFF2" ] && echo 1 || echo 0)"
assert "第二条落在第一条之后" "^1$" "$([ "$OFF2" -gt "$OFF1" ] 2>/dev/null && echo 1 || echo 0)"
assert "跨粗体取到源文里的完整跨度（18 字含标记）" "^18$" "$(echo "$R3" | jq -r '.data.end_offset - .data.start_offset')"

echo ""
echo "【4】列表"
LIST=$(curl -s "$API" -H "Authorization: Bearer $TOKEN")
assert "三条都在" '"total":3' "$LIST"
assert "按 offset 升序" "^1$" "$(echo "$LIST" | jq -r '[.data.annotations[].start_offset] | . == (sort)' | grep -q true && echo 1 || echo 0)"

echo ""
echo "【5】改批注 / 清空退化"
assert "改成有文字的批注" '"note":"改过了"' "$(curl -s -X PATCH "$BASE_URL/api/annotations/$ID1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"note":"改过了"}')"
assert "清空 note 退化成纯划线" '"note":""' "$(curl -s -X PATCH "$BASE_URL/api/annotations/$ID1" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"note":""}')"

echo ""
echo "【6】越权与不存在的 id"
assert "他人删除 → 404" '404' "$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE_URL/api/annotations/$ID1" -H "Authorization: Bearer $TOKEN2")"
assert "他人列表看不到（空）" '"total":0' "$(curl -s "$API" -H "Authorization: Bearer $TOKEN2")"
assert "他人 PATCH → 404" '404' "$(curl -s -o /dev/null -w '%{http_code}' -X PATCH "$BASE_URL/api/annotations/$ID1" -H "Authorization: Bearer $TOKEN2" -H 'Content-Type: application/json' -d '{"note":"x"}')"
assert "不存在的 id → 404" '404' "$(curl -s -o /dev/null -w '%{http_code}' -X DELETE "$BASE_URL/api/annotations/00000000-0000-0000-0000-000000000001" -H "Authorization: Bearer $TOKEN")"

echo ""
echo "【7】入参边界"
assert "空 quote → 400" '400' "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"quote":"   "}')"
LONGQ=$(python3 -c "print('字'*501)")
assert "quote 超 500 字 → 400" '400' "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"quote\":\"$LONGQ\"}")"
LONGN=$(python3 -c "print('字'*2001)")
assert "note 超 2000 字 → 400" '400' "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$API" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d "{\"quote\":\"结论是明确的\",\"note\":\"$LONGN\"}")"
assert "文档不存在 → 404" '404' "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/api/documents/doc-nope/annotations" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"quote":"结论"}')"

echo ""
echo "【8】手动重挂锚点"
assert "重挂到另一段" '"orphaned":false' "$(curl -s -X POST "$BASE_URL/api/annotations/$ID2/relocate" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"quote":"而是散落在 persona 渲染"}')"
assert "重挂到不存在的文字 → 400" '400' "$(curl -s -o /dev/null -w '%{http_code}' -X POST "$BASE_URL/api/annotations/$ID2/relocate" -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' -d '{"quote":"这句话根本不在文档里"}')"

echo ""
echo "【9】删文档级联删批注"
curl -s -o /dev/null -X DELETE "$BASE_URL/api/documents/doc-smoke" -H "Authorization: Bearer $TOKEN"
assert "级联后批注表为空" "^0$" "$(sqlite3 "$SMOKE_DB" 'select count(*) from document_annotations;' 2>/dev/null)"

echo ""
echo "════════════════════════════"
if [ "$FAIL" -eq 0 ]; then
    green "全部通过：$PASS 项"
    exit 0
else
    red "失败 $FAIL 项 / 通过 $PASS 项"
    red "服务日志：$LOG"
    exit 1
fi
