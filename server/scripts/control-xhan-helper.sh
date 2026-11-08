#!/bin/bash

# 加载用户环境变量（ssh 远程执行时不会自动 source，覆盖 bash 和 zsh）
for f in ~/.zshrc ~/.zprofile ~/.bashrc ~/.bash_profile; do
    source "$f" 2>/dev/null || true
done

APP_NAME="xhan-helper"
APP_PATH="/srv/xhan-helper"
LOG_DIR="$APP_PATH/logs"
LOG_FILE="$LOG_DIR/xhan-helper-$(date +%Y-%m-%d).log"
ENV="prod"

cd "$APP_PATH" || { echo "无法进入目录 $APP_PATH"; exit 1; }
export APP_ENV=$ENV

# 启动前预检：用新二进制自己的配置加载器校验（Key 缺失、YAML 写错都能拦住），
# 别把还在服务的旧进程杀掉。base config 里永远有 DOUBAO_VOICE_API_KEY 占位符，
# 所以不能靠 grep 变量名判断——enabled:false 的环境不需要 Key。
if ! CHECK_OUT=$("./$APP_NAME" -check-config 2>&1); then
    if echo "$CHECK_OUT" | grep -q 'flag provided but not defined'; then
        # 旧二进制没有这个 flag（首次升级到带预检的版本）：跳过，别把人挡在门外
        echo "⚠️ 当前二进制不支持 -check-config，跳过配置预检"
    else
        echo "$CHECK_OUT"
        echo "❌ 配置校验失败，拒绝重启（旧进程保持运行）"
        exit 1
    fi
fi

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
