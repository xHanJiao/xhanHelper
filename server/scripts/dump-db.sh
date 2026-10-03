#!/usr/bin/env bash
# dump-db.sh — 导出线上 MySQL 数据库
# 用法: bash scripts/dump-db.sh [output_dir] [keep_count]
#   output_dir 默认: /srv/xhan-helper/mysqldumps
#   keep_count 默认: 3（保留最近 N 份，自动清理旧备份）
# 需要环境变量: XHAN_HELPER_DB_PASSWORD（通常从 ~/.bashrc / ~/.zshrc 加载）

# SSH 远程执行时不会自动 source 用户环境——strict mode 之前先加载
for f in ~/.zshrc ~/.zprofile ~/.bashrc ~/.bash_profile; do
    source "$f" 2>/dev/null || true
done

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
OUTPUT_DIR="${1:-$PROJECT_DIR/mysqldumps}"
KEEP_COUNT="${2:-3}"

DB_HOST="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-3306}"
DB_USER="${DB_USER:-xhan_helper_user}"
DB_NAME="${DB_NAME:-xhan_helper}"
DB_PASSWORD="${XHAN_HELPER_DB_PASSWORD:-}"

if [ -z "$DB_PASSWORD" ]; then
    echo "❌ XHAN_HELPER_DB_PASSWORD 未设置"
    exit 1
fi

mkdir -p "$OUTPUT_DIR"

TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
OUTPUT_FILE="$OUTPUT_DIR/xhan_helper-$TIMESTAMP.sql.gz"

echo "=== 导出数据库 ==="
echo "  Host: $DB_HOST:$DB_PORT"
echo "  Database: $DB_NAME"
echo "  Output: $OUTPUT_FILE"

mysqldump \
    -h "$DB_HOST" -P "$DB_PORT" \
    -u "$DB_USER" -p"$DB_PASSWORD" \
    --single-transaction \
    --routines \
    --triggers \
    --no-tablespaces \
    --default-character-set=utf8mb4 \
    "$DB_NAME" | gzip > "$OUTPUT_FILE"

echo "✅ 导出完成: $OUTPUT_FILE ($(du -h "$OUTPUT_FILE" | cut -f1))"
echo "$OUTPUT_FILE"

# 顺带备份 events.db（活动埋点自包含 SQLite，与主库 MySQL 分家）。
# 优先 sqlite3 .backup（一致快照，含 WAL 内容）；无 sqlite3 时把 db+wal+shm 作为一组打包
# （单独拷主文件会丢掉未 checkpoint 的事件，故必须带上 -wal/-shm 或直接失败）。
EVENTS_DB="$PROJECT_DIR/data/events.db"
if [ -f "$EVENTS_DB" ]; then
    if command -v sqlite3 >/dev/null 2>&1; then
        sqlite3 "$EVENTS_DB" ".backup '$OUTPUT_DIR/events-$TIMESTAMP.db'"
        gzip -f "$OUTPUT_DIR/events-$TIMESTAMP.db"
        echo "✅ events.db 备份: $OUTPUT_DIR/events-$TIMESTAMP.db.gz ($(du -h "$OUTPUT_DIR/events-$TIMESTAMP.db.gz" | cut -f1))"
    elif command -v tar >/dev/null 2>&1; then
        EVENTS_DIR="$(dirname "$EVENTS_DB")"
        EVENTS_SET="$(basename "$EVENTS_DB")"
        [ -f "$EVENTS_DB-wal" ] && EVENTS_SET="$EVENTS_SET $(basename "$EVENTS_DB")-wal"
        [ -f "$EVENTS_DB-shm" ] && EVENTS_SET="$EVENTS_SET $(basename "$EVENTS_DB")-shm"
        tar -C "$EVENTS_DIR" -czf "$OUTPUT_DIR/events-$TIMESTAMP.db.tar.gz" $EVENTS_SET
        echo "⚠️  未找到 sqlite3，改为打包 db+wal+shm（须作为一组一并恢复）：$OUTPUT_DIR/events-$TIMESTAMP.db.tar.gz"
    else
        echo "❌ 需要 sqlite3 或 tar 才能安全备份 events.db（拒绝裸拷主文件丢 WAL）"
        exit 1
    fi
fi

# 自动清理：只保留最近 KEEP_COUNT 份备份，防止 mysqldumps 无限堆积
KEPT=0
for OLD in $(ls -t "$OUTPUT_DIR"/xhan_helper-*.sql.gz 2>/dev/null); do
    KEPT=$((KEPT + 1))
    if [ "$KEPT" -gt "$KEEP_COUNT" ]; then
        rm -f "$OLD"
        echo "🧹 已清理旧备份: $OLD"
    fi
done
KEPT=0
for OLD in $(ls -t "$OUTPUT_DIR"/events-*.db.gz "$OUTPUT_DIR"/events-*.db.tar.gz 2>/dev/null); do
    KEPT=$((KEPT + 1))
    if [ "$KEPT" -gt "$KEEP_COUNT" ]; then
        rm -f "$OLD"
        echo "🧹 已清理旧备份: $OLD"
    fi
done
