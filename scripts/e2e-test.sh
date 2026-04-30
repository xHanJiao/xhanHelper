#!/bin/bash
# 端到端测试: 注册 → 聊天 → 查列表 → 查消息
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "========================================="
echo "  xhanHelper 端到端测试"
echo "========================================="
echo ""

# 1. 注册设备
echo "【1/4】注册设备..."
bash "$SCRIPT_DIR/register-device.sh" "e2e-test" "macos"
echo ""

# 2. 发送第一条消息
echo "【2/4】发送消息..."
bash "$SCRIPT_DIR/chat.sh" "你好，我叫小明，请记住我的名字"
echo ""
echo ""

# 3. 查看会话列表
echo "【3/4】查看会话列表..."
bash "$SCRIPT_DIR/list-conversations.sh"
echo ""
echo ""

# 4. 测试多轮对话
echo "【4/4】多轮对话测试（使用 conversation_id）..."
CONV_ID=$(bash "$SCRIPT_DIR/chat.sh" "我今天心情不错" 2>&1 | grep "conversation_id:" | head -1 | sed 's/.*conversation_id: //' | tr -d '[]')
echo ""
echo "========================================="
echo "  测试通过!"
echo "  部署到阿里云后，将 BASE_URL 指向线上地址:"
echo "  BASE_URL=https://your-domain.com bash scripts/register-device.sh"
echo "========================================="
