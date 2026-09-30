# 023 文档「问 AI」功能整体下线

- 日期：2026-09-30
- 分支：`feat/document-annotation-reply`
- 设计决策：`docs/design/document-annotation-reply/design.md` **AR-12**

「问 AI」（文档选区 → 聊天引用胶囊 → 回答存为批注）作为**整体功能已下线**，替换为「批注下的 LLM 回复线程」。

> 旧设计与历史实现记录在 `docs/design/document-highlight-annotation/design.md`（顶部已标注为历史记录，**勿照此重新实现**）；该文档里的 D5 / D9 / D14、`annotationAskAI` 消息、§12、P5 各段均已失效。

## 删除范围

- **客户端**：阅读器工具条 / 点击菜单的 `ask` 入口、`revealQuote` / `beginNoteFromAI`、`DocumentAIBridge`、`DocQuote.swift`、`ChatView` 引用胶囊 + 「存为批注」窄条、`ChatViewModel.docQuote` / `attachDocQuote` / `quoteAnswerText`、SSE 请求体 `doc_quote`。
- **服务端**：`/api/chat` 请求字段 `doc_quote`、`RunRequest.DocQuote`、`retrieval.DocQuoteMeta`、`ctxflow` 的 `TypeDocumentQuote` / `document_quote` 块 / `DocumentQuotePacket` / `buildDocumentQuoteBlock` / `normalizeDocQuote`。
- `client/xHelper/scripts/check-reader-html.sh`：移除 P5 断言（工具条动作数 4→3）。

## 提交

`286a7079`（客户端）· `30052052`（服务端）· `962f568d`（文档与注释订正）
