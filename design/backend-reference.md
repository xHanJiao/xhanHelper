# xhanHelper 后端参考 — 数据模型与 API 接口

> 日期: 2026-05-01 | 用途: Python 重构参考

---

## 数据库模型

### devices — 设备

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(36) PK | UUID |
| token | VARCHAR(64) UNIQUE | 设备认证令牌 |
| name | VARCHAR(100) | 设备名称 |
| platform | VARCHAR(20) | ios / macos |
| created_at | TIMESTAMP | |
| last_seen_at | TIMESTAMP | |

### conversations — 会话

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(36) PK | UUID |
| device_id | VARCHAR(36) FK | 关联 devices |
| title | VARCHAR(200) | 默认"新对话" |
| created_at | TIMESTAMP | |
| updated_at | TIMESTAMP | |

### messages — 消息

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(36) PK | UUID |
| conversation_id | VARCHAR(36) FK | 关联 conversations |
| role | VARCHAR(20) | user / assistant |
| content | TEXT | 消息文本 |
| tool_calls_json | JSON | 工具调用详情（备用） |
| tool_call_id | VARCHAR(36) | 工具调用关联 ID（备用） |
| created_at | TIMESTAMP | |

### bills — 账单

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(36) PK | UUID |
| device_id | VARCHAR(36) FK | 关联 devices |
| item | VARCHAR(100) | 项目名称 |
| amount | DECIMAL(10,2) | 金额 |
| category1 | VARCHAR(50) | 一级分类 |
| category2 | VARCHAR(50) | 二级分类 |
| consume_type | VARCHAR(20) | 长期 / 一次性 / 周期 |
| date | VARCHAR(20) | YYYY-MM-DD |
| pay_type | VARCHAR(20) | 现金 / 公积金 |
| owner | VARCHAR(50) | 归属人 |
| remark | VARCHAR(200) | 备注 |
| status | INT | 1=正常 2=已作废 |
| created_at | TIMESTAMP | |
| updated_at | TIMESTAMP | |

**分类枚举：**
- Category1: 餐饮、服装、交通、居住、旅游、美容、人情、日用、数码、学习、医疗、娱乐
- Category2: 保险、餐饮、厨房用具、床上用品、打车、地铁、电影、订阅、房租、服务费、服装、机票、纪念品、驾照培训、交通、聚会、咖啡奶茶、老家、零食、买菜、买药、女性日用、朋友、啤酒、漂染头发、日用、日用耗材、日用小家电、社会、生活用品、市内短途、数码配件、数码设备、水电费、堂食、堂食（伴侣）、堂食（请客）、体检、外卖、玩具、箱包、休闲、医疗、医疗器械、饮料、饮用水、长途（火车）、住宿
- ConsumeType: 长期、一次性、周期
- PayType: 现金、公积金

### user_memories — 长期记忆（表已建，功能未启用）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(36) PK | |
| device_id | VARCHAR(36) FK | |
| memory_key | VARCHAR(200) | |
| memory_value | TEXT | |
| created_at / updated_at | TIMESTAMP | |

### scheduled_tasks — 定时任务（表已建，功能未启用）

| 字段 | 类型 | 说明 |
|------|------|------|
| id | VARCHAR(36) PK | |
| device_id | VARCHAR(36) FK | |
| tool_name | VARCHAR(50) | |
| func_name | VARCHAR(50) | |
| params_json | JSON | |
| cron_expr | VARCHAR(50) | |
| enabled | TINYINT | |

---

## API 接口

### POST /api/devices/register — 设备注册

- 无需认证
- Body: `{"device_name":"...", "platform":"ios|macos", "secret":"<REGISTRATION_SECRET>"}`
- 返回: `{"device_token":"...", "device_id":"..."}`
- 校验: `secret` 必须匹配环境变量 `REGISTRATION_SECRET`，不匹配返回 403

### POST /api/chat — 流式对话 (SSE)

- Bearer Token 认证
- Body: `{"message":"...", "conversation_id":"..."(可选)}`
- 返回: `text/event-stream` SSE 事件流

**SSE 事件：**

| event | data | 说明 |
|------|------|------|
| `message` | `{"delta":"好"}` | 逐字文本 |
| `tool_call` | `{"tool":"bookkeeping","function":"create_bill","args":{...}}` | 工具调用 |
| `tool_result` | `{"success":true,"data":{...}}` | 工具结果 |
| `done` | `{"conversation_id":"...","message_id":"..."}` | 本轮完成 |
| `error` | `"错误信息"` | 异常 |

### GET /api/conversations — 会话列表

- Bearer Token 认证
- 返回: `[{id, device_id, title, created_at, updated_at}]`（最近50条）

### GET /api/conversations/:id/messages — 消息历史

- Bearer Token 认证
- 校验会话归属当前设备
- 返回: `[{id, conversation_id, role, content, created_at}]`

### GET /api/bills — 账单列表

- Bearer Token 认证
- 返回: `[{id, item, amount, category1, category2, consume_type, date, pay_type, owner, remark, status}]`

### POST /api/bills/import — CSV 导入

- Bearer Token 认证
- Content-Type: text/plain
- Body: 逗号分隔 CSV（首行标题），列序: 项目,实际消费,一级分类,二级分类,消费类别,日期,消费类型,人员,备注
- 返回: `{"imported":N, "skipped":N, "errors":[...]}`

---

## Agent 机制

### ReAct 循环

```
用户消息 → Orchestrator.Run()
  → 构建 SystemPrompt (含工具定义 + 分类枚举 + 约束规则)
  → LLM ChatSync (豆包 /responses API)
  → 解析 JSON: {"reply":"...", "tool_calls":[{tool, function, args}]}
  → tool_calls 非空 → 执行工具 → 结果追加到上下文 → 重新调用 LLM (最多5轮)
  → tool_calls 为空 → 流式输出 reply → done
```

### Tool 注册机制

```python
# Python 重构时的接口参考
class Tool(ABC):
    name: str
    description: str
    functions: list[FuncSchema]  # {name, description, parameters (JSON Schema)}
    def execute(self, ctx: ExecuteContext, func_name: str, args: dict) -> Result
```

### 豆包 API 调用方式

- 端点: `https://ark.cn-beijing.volces.com/api/v3/responses`
- 请求体: `{"model":"...", "input":[{role, content}], "store":true, "max_output_tokens":2048}`
- 响应: `{"status":"completed", "output":[{type:"message", role:"assistant", content:[{type:"output_text", text:"..."}]}]}`
- 不支持原生 function calling，依赖 prompt engineering 实现工具调用

### System Prompt 关键要素

1. 基础行为规则（简洁/诚实/主动）
2. 工具定义 + 函数 signature + 枚举值列表
3. JSON 输出格式约定（`{"reply":"...","tool_calls":[...]}`）
4. 强约束：分类必须从枚举选，不确定时反问
