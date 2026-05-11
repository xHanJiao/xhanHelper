# 记忆系统改进方向

> 2026-05-11 梳理

---

## 一、已修复问题

### P1 - High
1. ✅ **向量持久化缺失同步机制** - 删除记忆时同步移除向量库条目
2. ✅ **排序算法效率低** - O(nk) 选择排序 → O(n logk) 堆排序

### P2 - Medium
1. ✅ **去重机制改进** - 增加 content_hash 精确去重 + 相似度阈值可配置
2. ✅ **函数命名明确化** - `ListAllPaginated` → `ListAllUsersMemoriesPaginated`

---

## 二、记忆系统功能改进

### 1. 记忆生命周期管理
- **过期/归档**：很久没用到的记忆可以归档，不参与检索
- **记忆合并**：发现相似/冲突的记忆时，支持人工或半自动合并
- **重要性评分**：根据 `confirm_count`、使用频率等计算重要性，检索时加权
- **记忆显式开关**：用户可以手动禁用某些记忆

### 2. 检索增强
- **时间衰减**：最近产生的记忆权重更高
- **混合检索**：关键词过滤 + 向量相似度结合
- **分类检索**：按 Source/Topic 等维度先过滤再向量检索
- **重排序**：先粗筛 top N，再用更复杂的逻辑重新排序

### 3. 提取质量改进
- **反馈循环**：用户可以对提取的记忆点赞/点踩，作为 fine-tuning 信号
- **多轮提取**：一次会话结束后可以分多轮提取不同维度的记忆
- **记忆结构化**：不仅存 summary，还可以提取结构化的 key-value（比如喜好、作息等）

---

## 三、向量存储层改进

### 1. 可插拔接口
```go
type VectorStore interface {
    Upsert(id string, vec []float64, metadata map[string]string)
    Delete(id string)
    Search(query []float64, topK int, filter FilterFunc) []SearchResult
    Size() int
}
```
- 当前：内存实现
- 未来：可接入 Qdrant、Milvus、Chroma 等专门的向量数据库

### 2. 增量加载
- 当前：启动时全量加载所有记忆/笔记/消息向量
- 改进：按用户按需加载，或记录增量 checkpoint

---

## 四、架构解耦专题（待规划）

### 痛点
当前 `Orchestrator` 是超级耦合大核心，职责过多：
- 构建 System Prompt
- 管理 ReAct 循环
- 调用 LLM
- 执行工具
- 保存消息/记忆关联
- 更新向量库
- ...

### 解耦方向

#### 1. 事件驱动架构
```
UserMessageSavedEvent → 触发向量化
ToolExecutedEvent → 触发关联记忆确认
MemoryExtractedEvent → 触发向量库更新
```

#### 2. 职责拆分
| 当前 | 拆分后 |
|------|--------|
| Orchestrator | ReActLoop、ToolExecutor、MessagePersister |
| Retriever | PromptBuilder、ContextComposer |

#### 3. 中间件/拦截器模式
- 把日志、鉴权、 metrics 等从业务逻辑里剥离出来

---

## 五、待处理小问题（P3 - Low）

1. **MD5 安全性** - `hashPrompt` 改用 SHA-256
2. **Retrieve 逻辑耦合** - ContextPacket 增加 Type 字段，或用结构化返回值
3. **"无" 判断脆弱** - LLM 输出模糊匹配或改用 JSON `{"should_extract": false}`
