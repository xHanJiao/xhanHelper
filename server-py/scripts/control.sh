#!/bin/bash

# 加载环境变量（ssh 远程执行时不自动 source）
for f in ~/.zshrc ~/.zprofile ~/.bashrc ~/.bash_profile; do
    source "$f" 2>/dev/null || true
done

APP_NAME="xhan-helper-py"
APP_PATH="/srv/xhan-helper-py"
LOG_DIR="$APP_PATH/logs"
LOG_FILE="$LOG_DIR/xhan-helper-$(date +%Y-%m-%d).log"
ENV="prod"
PORT=8088

mkdir -p "$LOG_DIR"
find "$LOG_DIR" -name "xhan-helper-*.log" -mtime +30 -delete 2>/dev/null

# 停止旧进程
PID=$(ps -ef | grep -v grep | grep "uvicorn app.main:app" | grep "$PORT" | awk '{print $2}')
if [ -n "$PID" ]; then
    echo "发现正在运行的进程，PID: $PID，准备终止..."
    kill $PID
    for i in {1..5}; do
        if ! ps -p $PID > /dev/null; then break; fi
        sleep 1
    done
    if ps -p $PID > /dev/null; then
        echo "进程未正常终止，强制终止进程 $PID..."
        kill -9 $PID
        sleep 1
    fi
    echo "进程已终止"
fi

cd "$APP_PATH" || { echo "无法进入目录 $APP_PATH"; exit 1; }
if [ ! -f "venv/bin/activate" ]; then
    echo "❌ venv 不存在，请先执行: cd $APP_PATH && python3 -m venv venv"
    exit 1
fi
# 快速检查核心依赖是否已装（不联网）
if ! . venv/bin/activate && python -c "import fastapi" 2>/dev/null; then
    echo "❌ 依赖未安装，需要在服务器上手动执行一次:"
    echo "   cd $APP_PATH && source venv/bin/activate"
    echo "   pip install -i https://pypi.tuna.tsinghua.edu.cn/simple -r requirements.txt"
    exit 1
fi

# 启动新进程
echo "启动 $APP_NAME (环境: $ENV, 端口: $PORT)..."
export APP_ENV=$ENV
. venv/bin/activate && nohup uvicorn app.main:app --host 0.0.0.0 --port $PORT --log-level info >> "$LOG_FILE" 2>&1 &

sleep 3
NEW_PID=$(ps -ef | grep -v grep | grep "uvicorn app.main:app" | grep "$PORT" | awk '{print $2}')
if [ -n "$NEW_PID" ]; then
    echo "$APP_NAME 已成功启动，PID: $NEW_PID"
    echo "端口: $PORT"
    echo "日志: $LOG_FILE"
else
    echo "启动失败，请检查日志文件 $LOG_FILE"
    exit 1
fi
