# xhanHelper 路线图 v2

> 日期: 2026-05-02 | P0-P3 已完成

---

## 已完成

| 阶段 | 内容 | 关键产出 |
|------|------|----------|
| P0 | 项目骨架 + 基础对话 | Go SSE 聊天, 设备认证, 会话管理 |
| P1 | 工具系统 + 记账 | ReAct 循环(初版), Tool Registry, LLM 抽象层 |
| P2 | SwiftUI 客户端 | iPhone App, 流式对话, 工具卡片, 会话列表 |
| P3 | 数据导入导出 + 会话管理 | CSV 导入/导出, 重命名/删除, DeepSeek 接入 |

---

## 下一步

### P4.1 — Memory（记忆系统）

**目标**: Agent 从对话中自动提取用户偏好和习惯，下次对话时自动注入上下文。

**实现**:
```
对话结束 → LLM 提取关键信息 → 存 user_memories 表
下次对话 → 语义检索相关记忆 → 注入 system prompt
```

**效果示例**:
```
用户: "午饭花了35"
Agent: "已记录午餐35元（你一般午饭花30-50，今天在范围内）"
```

**关键技术点**:
- 记忆提取 prompt: "从这段对话中提取关于用户的偏好、习惯、事实。每条一句话。没有就返回空。"
- 去重: 新记忆与已有记忆比较，相似度过高则更新而非新增
- 检索: 用 LLM 判断哪条记忆和当前对话相关（MVP 阶段全量注入，数据量小）

**改动范围**: 后端新增 `agent/memory.go`，orchestrator 首尾加两个节点

---

### P4.2 — 真正的 ReAct Agent

**目标**: LLM 能自主思考、规划、调整策略，而非机械的"检测 tool_call → 执行 → 回传"。

**当前 vs 真正的 ReAct**:

| | 当前 | 真正的 ReAct |
|------|------|------------|
| LLM 能做什么 | 调一次工具 | 多步推理 + 交叉验证 |
| 思考过程 | 不可见 | 显示 Thought → Action → Observation 链 |
| 出错处理 | 原样返回错误 | 换个方式重试 |
| 策略调整 | 无 | 根据中间结果调整下一步 |

**实现方案**: 扩展 orchestrator，增加以下能力:
- `Thought`: LLM 在调用工具前输出思考过程
- `Plan`: 多步任务先拆解再执行
- `Reflect`: 工具结果不理想时，自主调整参数重试
- `Chain`: 多个工具调用有先后依赖关系

**改动范围**: orchestrator 重构，前端加思考过程展示

---

### P5 — 爬虫工具集成

**目标**: 将现有的 Selenium 爬虫迁移为 xhanHelper 的一个 Tool，通过对话触发数据采集。

**实现**: `tool/crawler/` 实现 Tool 接口，注册到 Registry

---

### P6 — 社区生态集成（MCP / OpenClaw 兼容）

**目标**: 能接入社区丰富的 Agent 工具和插件。

**方向一: MCP (Model Context Protocol) 兼容**
- MCP 是 Anthropic 提出的 Agent 工具协议，已成为社区事实标准
- 实现 MCP Client: 自动发现和调用任何 MCP Server 提供的工具
- 效果: 社区已有的 MCP Server（文件系统、数据库、浏览器等）直接可用

```
MCP Server (社区)          xhanHelper (我们的)
  file-system-server    →   Tool Registry
  puppeteer-server      →   自动注册
  postgres-server       →   一行配置接入
```

**方向二: OpenClaw 插件兼容**
- 如果 OpenClaw 有插件规范，实现适配层
- 优先支持 MCP，它是更通用的协议

**方向三: 自定义工具 DSL**
- YAML 配置描述一个工具，无需写 Go 代码
- 适合简单的 HTTP API 类工具

```yaml
# tools/custom/weather.yaml
name: weather
description: 查询天气
functions:
  - name: get_weather
    endpoint: https://api.weather.com/v1
    method: GET
    params:
      city: { type: string, required: true }
```

**改动范围**: `tool/mcp/` 新增 MCP 客户端, `tool/dsl/` DSL 解析器

---

### P7 — 定时任务编排

**目标**: 定时触发工具执行，结果主动推送给用户。

**场景**: 每天早8点自动爬取数据 / 每周日晚生成账单周报 / 餐饮超预算时提醒

**实现**: 轻量 cron 调度器 + scheduled_tasks 表

---

### P8 — Web 管理端

**目标**: Mac/PC 上的 Web 页面查看账单和统计，手机只做上报。

**技术**: Go template / Vue 轻量 SPA / HTMX

---

### P9 — 多模态

**目标**: 支持图片识别账单、语音输入。

---

## 优先级建议

```
现在 → P4.1 Memory     (独立模块，改动小，效果立竿见影)
     → P4.2 ReAct 重构 (需要 Memory 先落地，为思考链提供上下文)
     → P6 MCP 生态     (接入社区，快速获得大量工具)
     → P5 爬虫         (已有代码，迁移成本低)
     → P7 定时任务     (等工具丰富后再做更有价值)
     → P8 Web 端
     → P9 多模态
```
