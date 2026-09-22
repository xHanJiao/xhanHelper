#!/bin/bash
# 在远端服务器上执行 topic-merge。
#
# 默认只读（Generate）；当参数含 --dry-run=false（真正落库）时，
# 执行前停服、执行后（无论成败）起服，避免内存向量库与库不一致。
#
# 用法: bash scripts/run-topic-merge.sh [topic-merge 参数...]
#
# 注意：不要开 `set -u`——source ~/.zshrc 等（zsh 语法）时引用到未定义变量会让
# bash 直接静默退出（且错误被 2>/dev/null 吞掉），表现为「脚本什么都不做就 RC=1」。
# ssh 远程执行不会自动 source 用户环境变量
for f in ~/.zshrc ~/.zprofile ~/.bashrc ~/.bash_profile; do
    source "$f" 2>/dev/null || true
done

APP_PATH="/srv/xhan-helper"
cd "$APP_PATH" || { echo "无法进入 $APP_PATH"; exit 1; }
export APP_ENV=prod

WRITE=0
for a in "$@"; do
    case "$a" in --dry-run=false|-dry-run=false) WRITE=1 ;; esac
done

stop_service() {
    local pid
    pid=$(ps -ef | grep -v grep | grep -E "[.]/xhan-helper$" | awk '{print $2}')
    if [ -z "$pid" ]; then
        echo "服务未运行"
        return 0
    fi
    echo "停止服务 PID=$pid"
    kill "$pid" 2>/dev/null || true
    for _ in 1 2 3 4 5; do
        ps -p "$pid" >/dev/null 2>&1 || break
        sleep 1
    done
    if ps -p "$pid" >/dev/null 2>&1; then
        kill -9 "$pid" 2>/dev/null || true
    fi
}

if [ "$WRITE" = 1 ]; then
    stop_service
fi

./topic-merge "$@"
RC=$?

if [ "$WRITE" = 1 ]; then
    echo "重启服务..."
    bash scripts/control-xhan-helper.sh >/dev/null 2>&1 || true
fi

exit $RC
