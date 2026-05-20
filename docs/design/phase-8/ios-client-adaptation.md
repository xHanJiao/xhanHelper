# Phase 8 — iOS 客户端适配：追上后端接口迭代

> 日期: 2026-05-20 | 状态: 方案已确认，待实施

---

## 一、问题回顾

后端接口响应格式整体迭代过一轮，iOS 客户端代码未同步更新，导致多个接口实际上处于**不可用**状态。同时 iOS 定位不清晰，存在大量不需要的功能代码。

主要问题：

1. **响应格式不匹配** — 后端所有 REST 返回 `{"status":"ok","errorCode":0,"errMsg":"","data":T}`，iOS 只解析 `{"data": T}`，无法检测 `errorCode != 0` 的业务错误
2. **SSE 事件类型缺失** — 后端发送 `text_delta`，iOS 只映射了 `message`（不存在的事件），导致**流式文本无法渲染**
3. **List 端点响应结构变化** — 所有 list 接口从返回裸数组改为 `{items: [...], total: N}`，iOS 预期裸数组，JSON 解码直接失败
4. **模型字段不同步** — `Message` 缺失 `reasoning_content`、`memory_ids` 等字段，`Bill` 缺失 `consume_type`、`pay_type` 等

## 二、iOS 客户端定位

**核心功能客户端**，不做后台管理。保留功能：聊天、账单、笔记、记忆、智能体、系统 Prompt。不做：Admin 面板、动态工具管理、主题管理。Mailbox 及推送作为后续独立迭代。

## 三、改动清单（按优先级）

### 3.1 修复 `APIResponse<T>` 响应解析

**文件**: `Shared/Services/APIClient.swift`

```swift
// 现状 — 只解析 data 字段，不检查业务错误码
private struct APIResponse<T: Decodable>: Decodable {
    let data: T
}

// 重构后 — 完整解析 + 错误检查
private struct APIResponse<T: Decodable>: Decodable {
    let status: String
    let errorCode: Int
    let errMsg: String
    let data: T?
}
```

`doRequest` 中增加 `errorCode != 0` 的判断，不再只依赖 HTTP 状态码。

### 3.2 修复 SSE 事件类型映射

**文件**: `Shared/Services/SSEClient.swift`

当前 `dispatch` 函数的事件映射与后端实际发送不匹配：

| 后端发送 | iOS 当前映射 | 状态 | 影响 |
|----------|-------------|------|------|
| `text_delta` | 无 | ❌ 缺失 | 流式文本无法渲染 |
| `thought_delta` | `.thoughtDelta` | ✓ | |
| `thought_done` | `.thoughtDone` | ✓ | |
| `tool_call` | `.toolCall` | ✓ | |
| `tool_result` | `.toolResult` | ✓ | |
| `plan` | `.plan` | ✓ | |
| `plan_node_start` | `.planNodeStart` | ✓ | |
| `plan_node_done` | `.planNodeDone` | ✓ | |
| `status` | `.status` | ✓ | |
| `done` | `.done` | ✓ | |
| `error` | `.error` | ✓ | |
| （无） | `.thought`、`.planDone`、`.message` | ⚠️ 僵尸 | 无用映射，可清理 |

**改动**：补 `case "text_delta": type = .message`（保留 `.message` 枚举值不变，`ChatViewModel` 中 `case .message` 的处理逻辑正确）。移除 `plan_done`、`thought` 两个不存在的事件映射。

### 3.3 List 端点的 Response 模型

**文件**: `Shared/Services/APIClient.swift`

每个 list 端点独立定义响应模型，更新对应的方法：

| 端点 | 后端 `data` 内容 | iOS 新 Response 模型 | iOS 方法变更 |
|------|-----------------|---------------------|-------------|
| `GET /api/conversations` | `{conversations:[...], total:N}` | `ConversationListResponse` | `listConversations()` 返回类型改为 `ConversationListResponse` |
| `GET /api/bills` | `{bills:[...], total:N}` | `BillListResponse` | `getBills()` 返回类型改为 `BillListResponse` |
| `GET /api/notes` | `{notes:[...], total:N}` | `NoteListResponse` | `listNotes()` 返回类型改为 `NoteListResponse` |
| `GET /api/memories` | `{memories:[...], total:N}` | `MemoryListResponse` | `listMemories()` 返回类型改为 `MemoryListResponse` |
| `GET /api/agents` | `{agents:[...], total:N}` | `AgentListResponse` | `listAgents()` 返回类型改为 `AgentListResponse` |

对应的 ViewModel（`ConversationListViewModel`、`MemoryViewModel`、`AgentViewModel` 等）需同步更新以适配新的返回类型。

### 3.4 模型字段对齐

**Message** (文件: `Shared/Models/Message.swift`)：
```
新增: reasoningContent: String?    // DeepSeek 思考模式思维链
新增: memoryIds: [String]?          // 关联的记忆 ID
新增: toolCallId: String?           // tool 消息关联的 tool_call ID
```

**Conversation** (文件: `Shared/Models/Conversation.swift`)：
```
新增: lastExtractedMsgId: String?   // 上次提取的记忆截止消息 ID
新增: lastExtractedAt: String?      // 上次提取记忆时间
```

**Bill** (文件: `Shared/Services/APIClient.swift` 内嵌)：
```
新增: consumeType: String?          // 消费类型
新增: payType: String?              // 支付方式
新增: owner: String?                // 归属人
新增: remark: String?               // 备注
```

**UserMemory** (文件: `Shared/Services/APIClient.swift` 内嵌)：
```
新增: topic: String?                // 主题
新增: tags: [String]?               // 标签
新增: facts: [String]?              // 事实列表
新增: confirmCount: Int?            // 确认次数
移除: memoryKey                      // 后端已废弃
```

**Agent** (文件: `Shared/Services/APIClient.swift` 内嵌)：
```
新增: summary: String?              // 智能体简介
新增: paramsJson: String?           // LLM 参数 JSON
```

所有模型保持 `CodingKeys` 进行 snake_case ↔ camelCase 映射。新增字段统一用 optional（`?`），确保向后兼容。

## 四、不做的事

- 不新增 Mailbox 功能（后续独立迭代）
- 不新增推送通知（需要后端 APNs 改造，后续独立迭代）
- 不新增动态工具管理、Admin 面板、主题管理界面
- 不修改后端代码
- 不改变 SSE 请求体结构

## 五、测试策略

纯客户端改动，测试方式：

1. 启动后端 `make dev`
2. Xcode 编译运行 `xHelper` target
3. 验证核心流程：注册/登录 → 发送消息 → 流式响应显示 → 账单/笔记/记忆列表可用
4. 验证 deep mode 下 `thought_delta` 思维链显示正常
5. 验证 tool_call 卡片渲染正常

## 六、风险

- **`DeviceManager` 的 `APIResponse` 解析** — 登录/注册使用同一套解析，需确认 `token`/`userId` 仍能从 `data` 字段正确提取
- **模型字段顺序** — Codable 解码不依赖字段顺序，但 `CodingKeys` 枚举必须覆盖所有映射字段，否则解码失败
- **List 返回类型变更** — ViewModel 中所有引用 `.count`、`for-in` 遍历列表的地方都需要适配，从 `response.conversations` 而非直接 `response` 取值
