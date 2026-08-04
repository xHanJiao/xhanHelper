# PostChatWorkerGroup — 对话后处理统一管线

## 背景

重构前，对话结束后有两套独立的定时任务驱动后处理：

| 路径 | 任务类型 | 延迟 | 轮询 | 处理内容 |
|------|---------|------|------|---------|
| post_chat | `post_chat` | 30min | 3min | 记忆提取（可选）+ 主题状态更新 |
| summary | `summary_generation` | 15min | 30s | 对话摘要生成 |

**问题**：
1. 两条路径从同一对话读消息，做了大量重复的 LLM 调用与 DB 查询。
2. `post_chat` 的 `BuildConversationText` 拉取**全部角色**消息（user/assistant/tool），
   而浏览器 agent 工具的 tool 结果（snapshot HTML、页面文本）动辄数千字符，
   会严重膨胀记忆提取与主题更新的 LLM 输入，既烧 token 又引入噪声。
3. `LastExtractedMsgID` 游标式增量处理与 summary 的时间窗口式处理并存，机制不一致。

## 目标

1. 统一为一个 `session_end` 延迟任务 + 一个 30s 轮询。
2. 消息输入统一为 **session 窗口内 user 消息**（天然排除 tool 消息，解决膨胀问题）。
3. 三个子任务（摘要/记忆/主题）各自独立失败、不回滚。

## 架构

```
onChatEnd
  └─ CronRunner.NotifySessionEnd(userID, convID, userMsgID)
        └─ DelayedTask("session_end", 延迟=sessionGapMinutes)  // 去重/延迟重置
              └─ 30s ticker → processSessionEndTasks
                    └─ PostChatWorkerGroup.Process(convID, userID, lastMsgID)
                          ├─ 1. GetSessionStart(lastMsg) → 无则跳过
                          ├─ 2. ListUserMessagesBetween(窗口内 user 消息)
                          ├─ 3. buildMsgBlock → ConvText
                          └─ 4. 逐个 worker.Process(ctx, SessionSnapshot)
                                ├─ SummaryWorker —— 拉旧摘要 → LLM → saveSummary + embedding
                                ├─ MemoryWorker  —— (memoryAutoExtract) LLM 提取 → 去重 → storeMemory
                                └─ StateWorker   —— 匹主题 → ReAct loop → SaveState
```

### 核心接口

```go
// SessionSnapshot 是一个已结束 session 的完整快照，所有 worker 共享同一份。
type SessionSnapshot struct {
    ConvID   string
    UserID   string
    Messages []model.Message // 仅 user 角色，时间升序
    ConvText string          // buildMsgBlock 格式（带时间戳）
}

// PostChatWorker 是对话后处理子任务。
type PostChatWorker interface {
    Name() string
    Process(ctx context.Context, sess *SessionSnapshot) error
}
```

### 失败语义

- **消息拉取阶段失败** → `Process` 返回 error → 任务级重试（`ScheduleRetry`，最多 3 次）。
- **单个 worker 失败** → 只记日志，不影响其他 worker 继续（不回滚）。

## 输入变化影响

| 维度 | 旧（post_chat） | 新（session_end） |
|------|-----------------|-------------------|
| 消息角色 | ALL roles（含 tool） | **仅 user** |
| 记忆提取输入 | user+assistant+tool | 仅 user |
| 主题更新输入 | user+assistant+tool | 仅 user |
| tool 膨胀风险 | 高 | 零 |

用户长期记忆本就来自 user 的发言（偏好/习惯/计划/观点），去掉 assistant/tool
不丢失信息，反而去噪。`matchThemesFromMessages` 本就在 `role != user` 时跳过，
改为 user-only 输入后语义一致。

## 删除与保留

**删除**：
- `cron_runner.go`：`NotifyChatEnd` / `NotifySummaryCheck` / `enqueueSummaryTask` /
  `debounceSummaryTask` / `createSummaryTask` / `processPostChatTasks` /
  `processSummaryTasks` / `dispatchPostChat` / `taskPollTicker` / `taskPollInterval` /
  `postChatDelay`
- `extractor.go`：`BuildConversationText` / `ExtractFromConversation` /
  `ExtractFromConversationWithCallback` / `ExtractFromPrompt` / `updateProgress`
- 手动 API：`POST /api/conversations/:id/extract-memories` 与 `/extract-memories/stream`
  （含前端 `MemoryView.vue` 提取 UI、Swift `APIClient.extractMemoriesFromConversation`）
- `bills.go`：CSV 导入后的 `ExtractFromPrompt` 消费习惯提取
- `MemoryExtractor`：`MsgRepo` / `ConvRepo` 依赖（不再需要）

**保留（加 Deprecated 标记，不删列）**：
- `Conversation.LastExtractedMsgID` / `LastExtractedAt` —— 旧游标，仅保留列兼容旧数据
- `config.Reflection.PostChatDelaySeconds` —— 不再读取，保留字段兼容旧配置

**保留不变**：
- `DelayedTask` / `DelayedTaskMeta` 模型（taskType 从 `summary_generation` 改为 `session_end`）
- `SummaryWorker.callFlash` / `saveSummary` / `buildEmbeddingAndChunks`
- `StateUpdater.UpdateThemesFromConversation`（ReAct 逻辑不变）
- deliver / discover / mission 定时任务

## 文件清单

| 文件 | 说明 |
|------|------|
| `agent/post_chat_group.go` | 新建：SessionSnapshot + PostChatWorker + PostChatWorkerGroup |
| `agent/summary_worker.go` | 改：实现 PostChatWorker，去掉消息拉取/ProcessSessionEnd |
| `agent/memory_worker.go` | 新建：包装 MemoryExtractor |
| `agent/state_worker.go` | 新建：包装 StateUpdater |
| `agent/extractor.go` | 改：删游标/手动API/ExtractFromPrompt |
| `agent/cron_runner.go` | 改：统一 session_end 任务 |
| `controller/conversations.go` / `router.go` | 删 extract-memories 路由 |
| `controller/bills.go` | 删 CSV 记忆提取 |
| `appinit/agents.go` / `tools.go` / `router.go` | 接线改动 |
| `model/conversation.go` / `config/config.go` | Deprecated 标记 |

## 测试

- `post_chat_group_test.go`：无 SessionStart 跳过、正确窗口拉取（含 tool 消息排除）、
  部分 worker 失败继续、全 worker 调用、lastMsg 不存在
- `cron_session_end_test.go`：enqueue 新建、debounce 推迟 process_at + 更新 LastMsgID、
  ListDue + MarkDone
- 回归：`go test ./...` 全绿
