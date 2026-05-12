# 006 - 工具上下文管理与检索系统

## 概述

解决两个核心问题：
1. **FlashClient 工具结果不持久化** - 导致大模型忘记之前执行过的工具
2. **工具列表占用上下文** - 未来工具多时会浪费 token，需要动态检索

## 方案

---

## 一、FlashClient 消息持久化

### 问题
- FlashClient 使用文本 JSON 解析方式调用工具
- 工具执行结果没有落库，下次对话时大模型不知道结果
- 导致重复调用工具

### 方案

#### 1. 数据库保存标准格式
- `assistant` 消息：保存原始 JSON，`ToolCallsJSON` 字段填
- `tool` 消息：`role="tool"`，`ToolCallID` 填 snowflake ID，`Content` 填纯结果

#### 2. 重建上下文时根据 client 类型转换
- **ProClient**：原样返回标准格式
- **FlashClient**：
  - `assistant` 消息：原样返回 JSON
  - `tool` 消息：转换成 `role="user"`，`Content` 包装成 `"[工具执行结果] " + content`

#### 3. 测试结论
✅ FlashClient 完全能理解连续 user 消息的格式

---

## 二、工具检索系统

### 核心思路
- 所有工具统一存入 `tool_registries` 表（内置工具启动时自动 seed）
- 只暴露 `tool_retriever` 这一个工具给 LLM
- LLM 用自然语言查询相关工具
- 对话过程中用到的工具记录到 `conversation.extra`，后续轮次直接用

### 数据模型变更

#### 1. `tool_registries` 表加字段
```go
type ToolRegistry struct {
    // ... 现有字段
    Category      string `gorm:"size:100;not null;default:''"`
    EmbeddingID   string `gorm:"size:36;not null;default:''"` // 指向 embeddings 表
}
```

#### 2. `messages` 表加字段
```go
type Message struct {
    // ... 现有字段
    IsInternal bool `gorm:"default:false"` // 标记内部消息（如 tool_retriever 调用）
}
```

#### 3. `conversations` 表加字段
```go
type Conversation struct {
    // ... 现有字段
    Extra string `gorm:"type:text;not null;default:'{}'"` // JSON: {"used_tool_ids": ["..."]}
}
```

#### 4. `embeddings` 表加 EntityType
```go
const (
    // ... 现有类型
    EntityTypeTool = "tool"
)
```

#### 5. 配置文件加 Snowflake
```yaml
snowflake:
  node_id: 1
```

---

## 三、tool_retriever 策略模式设计

### 概述
tool_retriever 使用策略模式，支持灵活切换不同的检索算法。

### 结构设计

```
┌─────────────────────────────────────────┐
│        ToolRetriever (Context)          │
│  ┌───────────────────────────────────┐ │
│  │ - strategy ToolRetrieveStrategy   │ │
│  │ + SetStrategy()                   │ │
│  │ + Execute()                       │ │
│  └───────────────────────────────────┘ │
└─────────────────────────────────────────┘
               │ uses
               ▼
┌─────────────────────────────────────────┐
│    ToolRetrieveStrategy (Interface)     │
│  ┌───────────────────────────────────┐ │
│  │ + Name() string                   │ │
│  │ + Search(ctx, query, limit)       │ │
│  └───────────────────────────────────┘ │
└─────────────────────────────────────────┘
               ▲
               │ implements
    ┌──────────┼──────────┐
    │          │          │
┌──────┐    ┌──────┐   ┌─────────┐
│Keyword│    │Vector│   │ Hybrid  │
│      │    │      │   │         │
└──────┘    └──────┘   └─────────┘
```

### 核心组件

#### 1. `ToolRetrieveStrategy` 接口
```go
type ToolRetrieveStrategy interface {
    Name() string
    Search(ctx context.Context, query string, limit int) ([]ToolInfo, error)
}
```

#### 2. `ToolRetriever` (上下文)
- 持有当前策略
- 支持运行时切换 `SetStrategy(strategy)`
- 支持自定义初始化 `NewToolRetrieverWithStrategy(strategy)`
- 当前策略会在 Description 中体现，方便调试

#### 3. 内置策略实现

| 策略 | 说明 | 适用场景 |
|------|------|---------|
| `SimpleKeywordStrategy` | 关键词匹配 | 工具少时，精确匹配 |
| `VectorRetrievalStrategy` | 向量相似度检索 | 工具多时，语义匹配 |
| `HybridRetrievalStrategy` | 混合检索（关键词+向量） | 兼顾精度和召回 |

### 数据结构

#### `ToolInfo`
```go
type ToolInfo struct {
    ID          string                 `json:"id"`
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Category    string                 `json:"category"`
    Functions   []ToolFunctionInfo     `json:"functions"`
    Score       float64                `json:"score,omitempty"`
}
```

#### `ToolFunctionInfo`
```go
type ToolFunctionInfo struct {
    Name        string                 `json:"name"`
    Description string                 `json:"description"`
    Parameters  map[string]interface{} `json:"parameters"`
}
```

### 使用示例

```go
// 方式 1: 使用默认策略（关键词）
retriever := tool.NewToolRetriever()

// 方式 2: 指定策略
vectorStrategy := tool.NewVectorRetrievalStrategy(embedder, vectorStore, toolRepo)
retriever := tool.NewToolRetrieverWithStrategy(vectorStrategy)

// 方式 3: 运行时切换策略
hybridStrategy := tool.NewHybridRetrievalStrategy(
    keywordStrategy,
    vectorStrategy,
    0.7,  // 向量权重
    0.3,  // 关键词权重
)
retriever.SetStrategy(hybridStrategy)
```

### 策略特点

- ✅ **运行时可切换**：无需重启即可改变检索行为
- ✅ **易于扩展**：只需实现接口即可添加新策略
- ✅ **依赖注入友好**：策略可接收任意依赖（repo、embedder、vector store 等）
- ✅ **可组合**：Hybrid 策略示例展示了如何组合多个策略

---

## 四、工作流

### 完整对话流程
```
1. 用户新消息
   ↓
2. 从 Conversation.Extra 读取 used_tool_ids
   ↓
3. 加载这些工具，加入 ChatRequest.Tools（同时永远包含 tool_retriever）
   ↓
4. LLM 判断是否需要新工具
   - 不需要：直接用已有工具或回复
   - 需要：调用 tool_retriever("查询描述")
   ↓
5. 执行 tool_retriever（使用当前策略检索）
   ↓
6. 新工具追加到 used_tool_ids，更新 Conversation.Extra
   ↓
7. 后续轮次重复，ChatRequest.Tools 包含所有历史用过的工具
```

### tool_retriever 返回值
```json
{
  "tools": [
    {
      "id": "xxx",
      "name": "bookkeeping",
      "description": "记账工具",
      "category": "finance",
      "functions": [...],
      "score": 0.95
    }
  ],
  "strategy": "hybrid",
  "query": "记一笔饭钱"
}
```

---

## 五、实现步骤

### Phase 1: 基础设施 (已完成)
1. 引入 Snowflake ID 库 (`github.com/bwmarrin/snowflake`)
2. 配置文件加 Snowflake 配置
3. 数据库迁移（加字段）
4. 实现 ID 生成器单例 (`idgen` 包)

### Phase 2: FlashClient 消息持久化 (已完成)
1. 修改 `handleFlashResponse`：生成 snowflake ID（作为 tool_call_id），保存 tool 消息
2. 修改从数据库重建消息的逻辑：`BuildMessagesForFlash` / `BuildMessagesForPro`
3. `saveToolMsg` 继续用 uuid 作为消息主键，`tool_call_id` 用 snowflake

### Phase 3: 工具统一管理
1. 定义内置工具的 seed 数据（包括 tool_retriever 本身）
2. 启动时检查并 seed 内置工具到数据库
3. 重构 `Registry`：从数据库加载工具而非硬编码注册
4. 工具存入时生成 embedding

### Phase 4: tool_retriever 实现
1. 实现策略接口和基础框架
2. 实现 SimpleKeywordStrategy（关键词匹配）
3. 实现 VectorRetrievalStrategy（向量检索）
4. 实现 HybridRetrievalStrategy（混合检索）
5. 实现向量存储的工具检索方法

### Phase 5: 对话上下文管理
1. 实现 `Conversation.Extra` 的读写
2. 从 `used_tool_ids` 加载工具到 `ChatRequest.Tools`
3. 新工具自动追加到 `used_tool_ids`

### Phase 6: System Prompt 更新
1. 更新 system prompt：
   - 50-100 字摘要说明可用能力
   - 只包含 tool_retriever 这一个工具定义

---

## 六、注意事项

1. **向后兼容**：现有对话历史中的消息格式不变，重建时做兼容处理
2. **内置工具 seed**：幂等操作，启动时检查不存在才写入
3. **向量存储**：工具 embedding 生成后存入 `embeddings` 表，`tool_registries.EmbeddingID` 指向它
4. **IsInternal 使用**：tool_retriever 调用消息标记 `IsInternal=true`，提取记忆时跳过
5. **ID 生成策略**：现有表主键继续用 uuid，新增字段 `tool_call_id` 用 snowflake

---

## 七、未来扩展

- 工具市场：用户可以分享/发现工具
- 工具评分/推荐
- 多语言工具描述
- 更多检索策略（BM25、语义重排等）
- A/B 测试框架：在线对比不同策略效果
