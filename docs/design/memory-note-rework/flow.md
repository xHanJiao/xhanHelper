# 记录循环 — 总流程图

> 版本：v2｜日期：2026-09-23｜状态：设计定稿，待开发
>
> 上位：《记忆 / 笔记改造 · 设计文档》（`design.md`）§4.1–4.4（后续主体）
> 覆盖：入口 A 记忆（会话结束）/ 入口 B 笔记（`record_note` 内）、记录循环、写入工具内的 topic 复用
>
> v1 是「提取 → 决策闸门 → 执行」三段式；v2 按 2026-09-23 的决定改成**统一 ReAct 循环**（复用 `MiniReAct`）。

## 图

```mermaid
flowchart TD
  subgraph TRIG["两条入口"]
    direction TB
    CHAT["入口 A：会话结束（后台 PostChatWorker）<br/>输入 = session 窗口"]
    TOOL["入口 B：主 LLM 调 record_note<br/>输入 = 单条候选（主 LLM 不参与判断）"]
  end

  CHAT --> PRE
  TOOL --> PRE

  PRE["循环前：预检索一次<br/>跨 note + mem 两域取近邻<br/>相似度 ≥ memory_neighbor_threshold 0.5，top_k<br/>→ 格式化为初始 &lt;neighbors&gt; 上下文"]

  PRE --> LOOP

  subgraph LOOP["记录循环 agent/record_react.go（复用 MiniReAct）"]
    direction TB
    SYS["system prompt（稳定，缓存友好）<br/>· 一条 = 一个 topic + 一句 summary + facts<br/>· 本次最多记 memory_max_per_session = 5 条（明示）<br/>· 台账/逐日流水不记，走 document"]
    ROUND["ChatSync + 原生 function calling<br/>工具结果以 role=tool 回传"]
    TOOLS{"模型调了什么工具？"}
    READ["检索工具（只读，可多轮）<br/>search_memory / search_note"]
    WRITE["写入工具<br/>save_memory / update_memory / delete_memory<br/>save_note / update_note / delete_note"]
    NONE["一个写入工具都没调 = NONE<br/>对预检索 top-1 近邻 UpdateConfirm<br/>（confirm_count + 1；笔记侧无操作）"]
    CAP{"save_memory 累计达 5 次（记忆入口）<br/>或笔记入口已写入？"}
    STOP["无工具调用 / 达上限 / 达 maxIterations → 结束"]

    SYS --> ROUND --> TOOLS
    TOOLS -->|只调检索| READ --> ROUND
    TOOLS -->|调了写入| WRITE
    TOOLS -->|什么都没调| NONE
    WRITE --> CAP
    CAP -->|否| ROUND
    CAP -->|是| STOP
    NONE --> STOP
  end

  WRITE --> SAVE

  subgraph SAVE["写入工具内部（确定性，不交给 LLM）"]
    direction TB
    TOPIC["topic 复用 FindSimilarTopics<br/>候选集 = 该用户在用的 topic<br/>（notes ∪ memories distinct）<br/>≥ memory_topic_reuse_threshold 0.80<br/>→ 用最相似的那个；否则用新的"]
    NAMECHK["命名硬校验：禁 / 、逗号、多主题，限长"]
    DB["落库<br/>user_memories / notes<br/>+ embeddings 向量 + 内存 vectorStore"]
    TOPIC --> NAMECHK --> DB
  end

  subgraph MUTEX["互斥原则：记忆让位笔记（谁后写谁负责）"]
    direction TB
    M1["记忆侧循环跨查两域<br/>命中笔记 → 不写入 / 删掉自己那条"]
    M2["笔记侧循环主要查笔记域<br/>可选删掉被顶替的旧记忆"]
  end
  LOOP -.-> MUTEX

  classDef todo fill:#fff3cd,stroke:#d9a441,stroke-width:2px;
  class CHAT,TOOL,PRE,SYS,ROUND,TOOLS,READ,WRITE,NONE,CAP,STOP,TOPIC,NAMECHK,DB,M1,M2 todo;
  style LOOP fill:#d6ecff,stroke:#3b7fd4,stroke-width:2px;
  style SAVE fill:#d6ecff,stroke:#3b7fd4,stroke-width:2px;
  style MUTEX fill:#d6ecff,stroke:#3b7fd4,stroke-width:2px;
```

图例：黄=本批待做、蓝=新建模块。

## 结构要点

1. **一个循环，两个入口**：记忆（多条、后台）与笔记（单条、工具内）在循环前汇流，之后走同一套工具语义。
2. **提取 = 调 `save_*`，决策 = 选哪个写入工具**：不再有单独的提取 LLM 与 `{op: …}` JSON 输出；**一个写入工具都没调 = NONE**。
3. **走原生 function calling**：给工具定义、读 `msg.ToolCalls`（照 `FactDecider`，`fact_worker.go:483`）。`target_id` 必须落在近邻集合内（`idSet` 校验，`:484`），参数非法丢弃并记日志，不拿零值继续写库。
4. **循环前预检索一次（≥ 0.5）**：省掉模型第一轮搜索；模型仍可调 `search_*` 追加。阈值避免把无关记录塞进上下文。
5. **上限 5 条必须在 prompt 里明示**：`memory_max_per_session`，工具侧同时计数封顶（达到即返回错误提示逼收敛）。
6. **topic 复用是确定性逻辑，留在 `save_*` 内部**：向量相似度不该让 LLM 复现。候选集限定「在用 topic」（notes ∪ memories 的 distinct），不用概念表全量——否则新 topic 可能被复用到已死的孤儿词（如 `睡眠管理 追踪睡眠记录与优化实验` 这类复合垃圾名）上。**不拒绝**：≥ 0.80 就用最相似的，否则用新的。
7. **delete 不清 topic 概念行**：概念行跨笔记/记忆共享（`concept_embeddings` 唯一键 `(type,value,user_id)`），孤儿由 topic-merge 的孤儿 GC 兜底。见 `docs/bugs/017-vector-cleanup-on-delete.md` §边界。孤儿本身无害（不进检索、不进 topic-merge 报告），隔几周手动跑一次 `make topic-merge-apply`（末尾带 GC，幂等）即可。
8. **互斥是虚线旁路**，不是主流程节点——它是两条入口间的最终一致性约束。
9. **不做自搜索**：曾设想给决策器开 MiniReAct 自搜索，但它底层还是向量检索，搜出来的东西与已送入的近邻重复。**现在检索已作为循环内的显式工具**（`search_*`），不再需要额外一层。

## 与 design.md 的对应

| 图中模块 | design.md |
|---|---|
| 入口 A / 入口 B、记录循环、工具集、上限、NONE | §4.2 |
| 笔记接入（同循环，同步跑） | §4.3 |
| 写入工具内的 topic 复用（仅 `save_*`，阈值 0.80） | §4.4 |
| 互斥：记忆让位笔记 | §4.2 末段 |
| 配置项 | §6 |
