#!/bin/bash

# 加载用户环境变量（ssh 远程执行时不会自动 source）
source ~/.bashrc 2>/dev/null || true
source ~/.bash_profile 2>/dev/null || true

APP_NAME="xhan-helper"
APP_PATH="/srv/xhan-helper"
LOG_DIR="$APP_PATH/logs"
LOG_FILE="$LOG_DIR/xhan-helper-$(date +%Y-%m-%d).log"
ENV="prod"

mkdir -p "$LOG_DIR"

# 清理 30 天前的旧日志
find "$LOG_DIR" -name "xhan-helper-*.log" -mtime +30 -delete 2>/dev/null

# 精准查找进程ID
PID=$(ps -ef | grep -v grep | grep -E "[.]/$APP_NAME$" | awk '{print $2}')

# 停止旧进程
if [ -n "$PID" ]; then
    echo "发现正在运行的进程，PID: $PID，准备终止..."
    kill $PID
    for i in {1..5}; do
        if ! ps -p $PID > /dev/null; then
            break
        fi
        sleep 1
    done
    if ps -p $PID > /dev/null; then
        echo "进程未正常终止，强制终止进程 $PID..."
        kill -9 $PID
        sleep 1
    fi
    echo "进程已终止"
fi

# 启动新进程
echo "启动 $APP_NAME (环境: $ENV)..."
cd "$APP_PATH" || { echo "无法进入目录 $APP_PATH"; exit 1; }
export APP_ENV=$ENV
nohup ./$APP_NAME >> "$LOG_FILE" 2>&1 &

sleep 2
NEW_PID=$(ps -ef | grep -v grep | grep -E "[.]/$APP_NAME$" | awk '{print $2}')
if [ -n "$NEW_PID" ]; then
    echo "$APP_NAME 已成功启动，PID: $NEW_PID"
    echo "日志: $LOG_FILE"
else
    echo "启动失败，请检查日志文件 $LOG_FILE"
    exit 1
fi
