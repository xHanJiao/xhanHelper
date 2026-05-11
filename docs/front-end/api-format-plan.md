# 接口改造计划

> 日期: 2026-05-09 | 状态: 待实施

## 1. 统一响应格式

当前后端 `JSONOK(c, data)` 直接返回裸 data。需改为统一格式：

```json
{"status":"ok","errorCode":0,"errMsg":"","data":{}}
```

**改动范围**：
- `view/render.go` — `JSONOK` 改为包裹结构体
- `view/render.go` — `JSONError` 改为返回统一格式（含 `status: "error"`）
- 前端 `api/response.js` — `unwrap()` 已提前准备，后端切格式后自动生效

## 2. 列表接口分页改造

### 当前状态

| 接口 | 分页 | 缺少 |
|------|------|------|
| `GET /api/conversations` | 硬编码 limit=50 | offset、total |
| `GET /api/conversations/:id/messages-v2` | cursor 分页 ✅ | — |
| `GET /api/bills` | limit/offset ✅ | total |
| `GET /api/notes` | limit/offset ✅ | total |
| `GET /api/memories` | limit/offset ✅ | total |
| `GET /api/agents` | limit/offset ✅ | total |
| `GET /api/tools` | **无分页** | limit/offset、total |
| `GET /api/prompts` | **无分页** | limit/offset、total |

### 分页策略

| 策略 | 适用场景 | 接口 |
|------|---------|------|
| **page + pageSize** (offset) | 数据表格、卡片列表，需要跳页 | bills, notes, memories, agents, tools, prompts |
| **load more** (cursor) | 时间线、无限滚动，按时间倒序 | conversations, messages-v2 |

- 所有 offset 分页接口统一返回 `{ data: [...], total: N }`
- cursor 分页保持现有 `{ messages, has_more_before, has_more_after, oldest_cursor, newest_cursor }`

### 改动物理清单

| 文件 | 改动 |
|------|------|
| `view/render.go` | `JSONOK` 包裹、`JSONError` 统一格式 |
| `controller/conversations.go` | `listConversations` 加 limit/offset 参数 |
| `controller/tool_controller.go` | `ListTools` 加 limit/offset |
| `controller/prompt.go` | `ListPrompts` 加 limit/offset |
| `service/tool_repo.go` | `ListTools` 加 limit/offset |
| `service/user_prompt_repo.go` | `ListByUser` 加 limit/offset |
| `service/conversation_repo.go` | `ListByUser` 加 offset 和 total count |
| `service/bill_repo.go` | `ListFiltered` 加 total count 返回 |
| `service/note_repo.go` | `List` 加 total count 返回 |
| `service/user_memory_repo.go` | `ListByUser` 加 total count 返回 |
| `service/agent_repo.go` | `ListByUser` 加 total count 返回 |

### 前端影响

- 后端 `JSONOK` 包裹后，`api/response.js` 的 `unwrap()` 自动提取 `res.data`
- total 返回后，各列表页的 `n-pagination` 可以使用实际 total 计算页数
- cursor 接口不受影响

## 3. 受影响的其他模块

### iOS 客户端
- **影响**: 所有 `JSONDecoder().decode(T.self, from: data)` 失效（期望裸数组/对象，收到包裹结构体）
- **修复**: 新增 `struct APIResponse<T: Decodable>: Decodable` 包装模型，所有 API 调用改返回 `APIResponse<T>`，ViewModel 取 `.data`

### 测试脚本 (api-test.sh)
- **影响**: 所有 `json.load(sys.stdin)` 断言失效（期望顶层字段，实际嵌套在 data 下）
- **修复**: 每个 python 解析前加 `json.load(sys.stdin).get('data', json.load(sys.stdin))` 兼容新旧格式，或批量替换
