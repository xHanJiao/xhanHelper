#!/usr/bin/env bash
# export-local-data.sh — 在 Mac 上打一致性的「本地→云端」迁移包。
#
# 用法: bash scripts/export-local-data.sh [data_dir] [output_dir]
#   data_dir   默认: ~/Library/Application Support/xhanHelper（本地 prod 数据根）
#   output_dir 默认: ./local-export-<timestamp>
#
# 前提: 已退出 Mac app（避免边写边备份）。产物分三样，各自独立上传：
#   xhan_helper.snapshot   主库一致性快照（.backup，含 WAL 内容）
#   events.db              活动埋点库（wal_checkpoint 后 .backup）
#   images.tar.gz          图片资产
set -euo pipefail

DATA_DIR="${1:-$HOME/Library/Application Support/xhanHelper}"
OUT_DIR="${2:-$(pwd)/local-export-$(date +%Y%m%d-%H%M%S)}"

MAIN_DB="$DATA_DIR/xhan_helper.db"
EVENTS_DB="$DATA_DIR/events.db"
IMAGES_DIR="$DATA_DIR/images"

if [ ! -f "$MAIN_DB" ]; then
    echo "❌ 找不到主库: $MAIN_DB"
    echo "   若数据根不同，把路径作为第一个参数传入。"
    exit 1
fi
if ! command -v sqlite3 >/dev/null 2>&1; then
    echo "❌ 需要 sqlite3（macOS 自带；检查 PATH）"
    exit 1
fi

mkdir -p "$OUT_DIR"

echo "=== 1/3 主库一致性快照（.backup）==="
# .backup 会把 WAL 里的已提交内容一并写进快照，服务运行中也一致；仍需先退出 app 更稳妥。
sqlite3 "$MAIN_DB" ".backup '$OUT_DIR/xhan_helper.snapshot'"
ls -lh "$OUT_DIR/xhan_helper.snapshot"

if [ -f "$EVENTS_DB" ]; then
    echo "=== 2/3 events.db（WAL checkpoint + .backup）==="
    sqlite3 "$EVENTS_DB" "PRAGMA wal_checkpoint(TRUNCATE);" >/dev/null
    sqlite3 "$EVENTS_DB" ".backup '$OUT_DIR/events.db'"
    ls -lh "$OUT_DIR/events.db"
else
    echo "=== 2/3 无 events.db（activity 未启用），跳过 ==="
fi

if [ -d "$IMAGES_DIR" ]; then
    echo "=== 3/3 图片打包 ==="
    tar -C "$DATA_DIR" -czf "$OUT_DIR/images.tar.gz" images
    ls -lh "$OUT_DIR/images.tar.gz"
else
    echo "=== 3/3 无 images 目录，跳过 ==="
fi

echo ""
echo "✅ 导出完成: $OUT_DIR"
echo "下一步（把 \$DEPLOY_USER/\$DEPLOY_HOST 换成 .env 里的值）："
echo "  1) 上传： scp $OUT_DIR/xhan_helper.snapshot $OUT_DIR/events.db $OUT_DIR/images.tar.gz \\"
echo "               \$DEPLOY_USER@\$DEPLOY_HOST:/srv/xhan-helper/upload/"
echo "  2) 迁移： 见 docs/design/return-to-cloud/design.md 的 Phase 4/5（停服窗口执行）"
