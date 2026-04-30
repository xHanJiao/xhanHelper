#!/bin/bash
# 共享环境变量 — 被其他脚本 source
# 用法: source scripts/env.sh

# 服务地址（本地开发默认 8080）
BASE_URL="${BASE_URL:-http://localhost:8080}"

# token 缓存文件
TOKEN_FILE="/tmp/xhan-helper-token"

# 读取已缓存的 token
if [ -f "$TOKEN_FILE" ]; then
    TOKEN=$(cat "$TOKEN_FILE")
else
    TOKEN=""
fi

echo "BASE_URL = $BASE_URL"
echo "TOKEN    = ${TOKEN:-<未注册>}"
