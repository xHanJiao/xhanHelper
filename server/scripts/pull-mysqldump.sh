#!/bin/bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
SERVER_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
ENV_FILE="$SERVER_DIR/.env"
REMOTE_DIR="/tmp/xhan-helper-dump"
LOCAL_DUMP_DIR="$SERVER_DIR/data/mysqldumps"
DB_NAME="xhan_helper_test"
LOCAL_ONLY="${LOCAL_ONLY:-0}"

if [ -f "$ENV_FILE" ]; then
  source "$ENV_FILE"
else
  echo "❌ 找不到 $ENV_FILE"
  exit 1
fi

mkdir -p "$LOCAL_DUMP_DIR"

# 0. 提前输入 MySQL 密码（终端不可见），后续复用
read -s -p "🔑 请输入本地 MySQL root 密码: " MYSQL_PWD
echo ""

if [ "$LOCAL_ONLY" = "1" ]; then
  # 仅导入本地已有 dump，不访问远端
  LOCAL_SQL=$(ls -t "$LOCAL_DUMP_DIR"/*.sql 2>/dev/null | head -1 || true)
  LOCAL_GZ=$(ls -t "$LOCAL_DUMP_DIR"/*.sql.gz 2>/dev/null | head -1 || true)
  if [ -z "$LOCAL_SQL" ] && [ -z "$LOCAL_GZ" ]; then
    echo "❌ $LOCAL_DUMP_DIR 下没有 .sql/.sql.gz 文件"
    exit 1
  fi
  if [ -n "$LOCAL_GZ" ] && { [ -z "$LOCAL_SQL" ] || [ "$LOCAL_GZ" -nt "$LOCAL_SQL" ]; }; then
    echo "📦 解压中: $(basename "$LOCAL_GZ")"
    gunzip -f "$LOCAL_GZ"
    LOCAL_SQL="${LOCAL_GZ%.gz}"
  fi
  echo "📄 使用本地文件: $(basename "$LOCAL_SQL")"
else
  # 1. 查找远程最新的 dump 文件
  echo "🔍 查找远程最新 mysqldump..."
  LATEST_FILE=$(ssh "${DEPLOY_USER}@${DEPLOY_HOST}" "ls -t ${REMOTE_DIR}/*.sql.gz 2>/dev/null | head -1")
  if [ -z "$LATEST_FILE" ]; then
    echo "❌ 远程 ${REMOTE_DIR} 下没有找到 .sql.gz 文件"
    exit 1
  fi
  echo "   最新文件: $(basename "$LATEST_FILE")"

  # 2. 下载到本地
  LOCAL_GZ="$LOCAL_DUMP_DIR/$(basename "$LATEST_FILE")"
  LOCAL_SQL="${LOCAL_GZ%.gz}"

  if [ -f "$LOCAL_GZ" ]; then
    echo "⏭️  已存在 $(basename "$LOCAL_GZ")，跳过下载"
  else
    echo "📥 下载到本地..."
    scp "${DEPLOY_USER}@${DEPLOY_HOST}:${LATEST_FILE}" "$LOCAL_GZ"
  fi

  # 2.5 删除远程临时备份文件
  echo "🗑️  删除远程临时备份..."
  ssh "${DEPLOY_USER}@${DEPLOY_HOST}" "rm -f ${LATEST_FILE}"

  # 3. 解压
  if [ -f "$LOCAL_SQL" ]; then
    echo "⏭️  已存在 $(basename "$LOCAL_SQL")，跳过解压"
  else
    echo "📦 解压中..."
    gunzip -f "$LOCAL_GZ"
  fi
fi

# 4. 重建数据库 + 导入
echo ""
echo "🗑️  删除并重建 ${DB_NAME} 数据库..."
MYSQL_PWD="$MYSQL_PWD" mysql -u root -e "DROP DATABASE IF EXISTS ${DB_NAME}; CREATE DATABASE IF NOT EXISTS ${DB_NAME} CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;"

echo "📥 导入数据到 ${DB_NAME}（文件较大，可能需要几十秒）..."
MYSQL_PWD="$MYSQL_PWD" mysql -u root "${DB_NAME}" < "$LOCAL_SQL"

echo "✅ 完成！$(basename "$LOCAL_SQL") → 本地 ${DB_NAME}"
