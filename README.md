# xhanHelper

聊天驱动的个人 AI Agent。Go + Vue 3 + SwiftUI，本地双击就能跑，也能部署在云上。

A chat-driven personal AI agent with tools, memory, and native macOS/iOS clients.

你说的话它会当真：记账、写文档、建提醒、翻历史对话；
会话结束后自动整理摘要、更新对你的画像。

## 能做什么

记账 · 笔记与 Markdown 文档 · 待办提醒 · 跨记忆/笔记/摘要/历史的语义检索 ·
联网搜索与抓取 · 子 agent 探索 · 语音对话 · 会话执行计划

原生 SwiftUI 客户端（macOS / iOS），支持客户端工具、MCP、全局快捷键。

## 跑起来

```bash
cp server/.env.example server/.env    # 填 LLM_API_KEY / EMBEDDING_API_KEY / XHAN_HELPER_DB_PASSWORD
cd server && make dev                 # 后端 :8080
cd front-end && npm i && npm run dev  # 前端 :5173
```

`APP_ENV` 决定形态：`dev`/`prod`（MySQL）、`test`（SQLite）、`local`（SQLite，Mac app 用）。

## 现状

个人项目，单用户、无多租户，接口和数据结构仍在变。实现细节见 `docs/design/`，
`AGENTS.md` 是给 AI 编码助手的代码库导览。

## License

MIT
