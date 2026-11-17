# xhanHelper

> 聊天驱动的个人 AI Agent —— Go 单体后端 + Vue 3 前端 + 原生 SwiftUI 客户端（macOS / iOS）。
>
> A chat-driven personal AI agent: Go monolith, Vue 3 web, native SwiftUI clients.

用自然语言对话，Agent 自主判断意图、调用工具（记账 / 笔记 / 文档 / 检索 / 提醒 / 子任务），
并带向量记忆、后台记忆-摘要-画像流水线。可以纯本地跑（SQLite + 双击 app），也可以部署到云上（MySQL + nginx）。

## 特性

- **原生 function calling**：主对话与后台 worker 全部走 DeepSeek 原生工具调用，不解析文本 JSON
- **渐进式工具暴露**：常驻只有一个 `expose_tools` 元工具，LLM 按需把具体工具/函数拉进请求体
- **向量检索**：Ark Embedding + 内存向量库（余弦相似度 + 时间衰减），跨 memory / note / summary / event / history 五域
- **后台处理流水线**：会话结束后异步产出记忆、摘要、用户画像、事件线程、反思投递
- **SSE 流式**：断开连接不终止执行，支持断线续跑
- **三种形态**：云端（MySQL）、本地（SQLite，双击 app 启动）、dev 沙盒（与本地 prod 完全隔离）

## 架构

```
POST /api/chat
 → middleware        Bearer Token → user_id
 → pipeline          短输入检测 + 相对日期规范化
 → orchestrator      embed → 分层 prompt 构建 → 多源并发检索
 → llm.Chat(stream)  原生 function calling
 → 工具执行           服务端并发 / 客户端串行
 → 落库 + WriteSSE
```

关键约束：进入请求体的动态内容必须字节稳定（工具定义按名字排序），否则破坏前缀缓存；
动态上下文（检索结果 / 画像 / 摘要）一律注入当前 user 消息，不进 system prompt。

## 目录

```
server/     Go 后端（config / model / service / controller / agent / llm / tool / app …）
front-end/  Vue 3 + Naive UI
client/     SwiftUI 客户端（Shared/ 同时编进 macOS 与 iOS target）
docs/       设计文档、bug 记录、代码审查
```

## 快速开始

前置：Go 1.26、Node 20+、（可选）MySQL 8。

```bash
# 1. 环境变量（不提交，已在 .gitignore）
cp server/.env.example server/.env
$EDITOR server/.env        # LLM_API_KEY / EMBEDDING_API_KEY / XHAN_HELPER_DB_PASSWORD

# 2. 后端（dev → MySQL）
cd server && make dev

# 3. 前端（另一个终端）
cd front-end && npm install && npm run dev
```

配置加载顺序：`conf/config.yaml` → `conf/config.<APP_ENV>.yaml`（非零值覆盖）→ `${VAR}` 展开 → 校验。
环境切换全靠 `APP_ENV`：`dev`（MySQL）、`prod`（MySQL）、`test`（SQLite）、`local` / `local.dev`（SQLite，本机 app 用）。

### 主要环境变量

| 变量 | 用途 |
|---|---|
| `LLM_API_KEY` | 对话模型 Key（provider 见 `conf/config.yaml`） |
| `EMBEDDING_API_KEY` | 向量模型 Key |
| `DOUBAO_VOICE_API_KEY` | 语音合成/识别 Key（`voice.enabled=true` 时必需） |
| `XHAN_HELPER_DB_PASSWORD` | MySQL 密码 |
| `ALLOWED_USER` | 注册白名单 |
| `XHAN_HELPER_ADMIN` | 管理员用户 ID |

## 技术栈

| 层 | 技术 |
|---|---|
| 后端 | Go 1.26 · Gin · GORM · MySQL / SQLite |
| 前端 | Vue 3 · Naive UI · Pinia · Vite |
| 客户端 | Swift · SwiftUI（macOS 26 / iOS 26） |
| AI | DeepSeek 原生 function calling · Ark Embedding |

## 文档

设计与实现细节在 `docs/design/`（按特性分目录），踩坑记录在 `docs/bugs/`。
`AGENTS.md` 是给 AI 编码助手看的代码库导览，也适合人类快速理解项目结构。

## 现状

这是一个从个人需求长出来的项目，**不是通用产品**：

- 面向单用户 / 极小规模场景，没有多租户隔离设计
- 接口和数据结构仍在演进，不保证向后兼容
- 部分依赖特定模型供应商（DeepSeek / 火山引擎 Ark）

欢迎参考架构与实现，但不建议直接当作生产级服务部署。

## License

待定（见 `LICENSE`）。
