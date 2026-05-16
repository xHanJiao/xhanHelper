# 主题生命周期 + 站内信解耦

> 日期: 2026-05-16 | 状态: 设计评审通过，待实施

---

## 一、动机

1. 主题和候选人数据混在 `mailbox_items` 里，不利于扩展——未来加新的通知类型（如账单提醒、系统公告）需要重构
2. 站内信应该是纯触达层，只存通知文案 + 实体引用；主题数据应该归于 `theme_states`
3. 采纳候选人目前是「删 mailbox 消息 + POST 新建主题」，改了 status 就行
4. 跨轮次去重没有利用主题表已有的索引

## 二、设计原则

```
theme_states  —  主题全生命周期：candidate → active → archived/closed
mailbox_items —  纯通知触达：title/content + entity_type/entity_id 引用
```

- 站内信不参与 LLM 上下文
- LLM 需要的东西全部走 theme_states
- candidate 转正就是 status 变更，不建新记录

## 三、Schema 变更

### 3.1 theme_states

| 字段 | 操作 | 类型 | 说明 |
|------|------|------|------|
| status | 扩枚举 | varchar(20) | 加 `candidate` |
| metadata | 新增 | text/json | LLM 原始输出，含 candidate_info |

metadata 内容：
```json
{
  "candidate_info": {
    "rationale": "用户于2026-05-07决定裸辞...",
    "signal_strength": "high",
    "source_refs": ["note:abc...", "mem:def..."]
  }
}
```

### 3.2 mailbox_items

| 字段 | 操作 | 类型 | 说明 |
|------|------|------|------|
| entity_type | 新增 | varchar(20) | 引用实体类型，如 "theme" |
| entity_id | 新增 | varchar(36) | 引用实体 ID |
| metadata | 删除 | — | 不再需要，数据归于 theme_states |

## 四、状态流转

```
        ThemeDiscoverer 发现
              ↓
        candidate ───→ active ──→ archived
              ↓            ↓
           closed ←────── closed
                        (过期/忽略)
```

- candidate → active：用户采纳
- candidate → closed：用户忽略 / 7 天过期
- active → closed：用户关闭
- active → archived：长期不用归档
- closed → active：用户重新激活

## 五、流程变更

### 5.1 候选发现

```
旧: ThemeDiscoverer → Go 拼字符串 → mailbox.deliver
新: ThemeDiscoverer → 写 theme_states (status=candidate, metadata=candidate_info)
                    → Secretary 人话化
                    → mailbox.deliver (entity_type=theme, entity_id=该ID, title/content=提醒文案)
```

### 5.2 采纳

```
旧: 读 mailbox title/content → POST /api/themes 创建新主题 → 归档信箱消息
新: PUT /api/themes/:id/status  {status: "active"}
```

前端「采纳」按钮改为调 theme 的 status API。

### 5.3 去重

```
旧: 查询 mailbox_items WHERE type='candidate_theme' → 取 title
新: 查询 theme_states WHERE status IN ('active','candidate') → 取 name + description
    拼成「职业探索：裸辞后新赛道探索」传给 LLM
```

### 5.4 ThemeAnalyst 冷启动增强

ThemeAnalyst 输入消息中新增：

```
## 候选提名理由
{metadata.candidate_info.rationale}
{metadata.candidate_info.source_refs}
```

帮助 LLM 在首次分析时聚焦方向，不再纯靠向量检索从零拼凑。

## 六、信箱消息示例

一条 `candidate_theme` 通知：

```
title: "职业探索"
content: "最近发现你多次聊到裸辞后的新方向，要不要把「职业探索」作为一个长期追踪的主题？"
type: "candidate_theme"
source: "system"
entity_type: "theme"
entity_id: "abc123..."              ← 指向 theme_states
```

## 七、实施清单

| 文件 | 改动 |
|------|------|
| `model/theme_state.go` | status 枚举加 candidate；加 Metadata 列 |
| `model/mailbox.go` | 加 entity_type/entity_id；删 metadata |
| db 迁移 | AutoMigrate 处理新列 |
| `agent/reflection.go` runDiscoveryOnly | 写 theme_states + mailbox 引用 |
| `agent/reflection.go` 去重逻辑 | 从 theme_states 查候选列表 |
| `agent/theme_analyst.go` | 输入消息注入 candidate_info |
| `agent/theme_discover.go` | 输入消息加「近期已提名」段 |
| `front-end/MailboxView.vue` | 采纳改为 PATCH /api/themes/:id |
| `front-end/ThemeView.vue` | 支持展示 candidate 状态的主题 |
