#!/bin/bash
# 导出最近 N 天的账单（默认 3 天）
# 用法: ./scripts/export-bills.sh [天数]

set -e

DAYS="${1:-3}"
BASE_URL="${BASE_URL:-https://helper.xhanx.cn}"
TOKEN="${myToken:?请先在 ~/.zshrc 中设置 export myToken=\"your-token\"}"

START=$(date -v-${DAYS}d +%Y-%m-%d)
END=$(date +%Y-%m-%d)
FILE="/Users/you/Documents/统计/bills-${START}-${END}.csv"

echo "导出 ${DAYS} 天内账单 (${START} ~ ${END})..."
curl -s -H "Authorization: Bearer $TOKEN" \
  "$BASE_URL/api/bills/export?start_date=$START&end_date=$END" \
  -o "$FILE"

LINES=$(wc -l < "$FILE" | tr -d ' ')
echo "✅ 已导出到 $FILE ($LINES 行)"
