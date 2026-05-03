# xhanHelper 路线图 v2

> 日期: 2026-05-02 | P6 进行中

---

## 已完成

| 阶段 | 内容 | 关键产出 |
|------|------|----------|
| P0 | 项目骨架 + 基础对话 | Go SSE 聊天, 设备认证, 会话管理 |
| P1 | 工具系统 + 记账 | ReAct 循环, Tool Registry, LLM 抽象层 |
| P2 | SwiftUI 客户端 | iPhone App, 流式对话, 工具卡片, 会话列表 |
| P3 | 数据导入导出 + 会话管理 | CSV 导入/导出, 重命名/删除, DeepSeek 接入 |
| P4.1 | 记忆系统 | LLM 记忆提取, user_memories 表, 记忆注入 prompt |
| P4.2a | 思考链 | thought 事件, 并发工具执行, buildSystemPrompt 升级 |
| P4.3a | Plan 流程图 | plan/plan_node_start/plan_node_done 事件, 客户端 DAG 展示 |
| — | DeepSeek 接入 + query_bills 增强 | 原生 function calling, 月度/分类统计 |

---

## 现在 — P6 严肃思维伙伴

**核心目标**: 把 xhanHelper 打造成冷静、中立、客观的思维伙伴。不谄媚、不迎合，基于事实和逻辑讨论问题。

详见 `design/p6-serious-partner.md`

| 优先级 | 模块 | 难度 | 状态 |
|--------|------|------|------|
| ⭐1 | DeepSeek 强模型 + 强弱切换 | 中 | ✅ 已完成 |
| ⭐2 | 变量追踪工具（Observations） | 小 | 严肃分析的数据基础 |
| ⭐3 | 记忆增强（向量检索） | 中小 | — |
| ⭐4 | P4.3b 动态图弹窗 | 中大 | — |
| 🫱5 | Todo 工具 | 小 | — |

---

## 以后

| 阶段 | 内容 | 说明 |
|------|------|------|
| P7 | 定时任务编排 | cron 调度器: 每天自动统计、超预算提醒 |
| P8 | Web 管理端 | 账单看板 + 统计分析 |
| P9 | 多模态 | 图片识别账单、语音输入 |
| — | 搜索能力 | 等国内可用方案成熟 |
| — | MCP 生态 | 框架已就绪(feature/mcp-search), 等合适的 server |
