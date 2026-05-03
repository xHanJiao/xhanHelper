# P6-2 — 变量追踪工具（record_note）技术方案

> 日期: 2026-05-03 | 状态: 方案评审

---

## 一、目标

自由文本记录 + 可选标签 + 时间戳。零 schema 负担，Pro 模型通读分析。

参考交互设计: `design/p6-note-tool-cases.md`

---

## 二、数据模型

```go
// model/note.go

type Note struct {
    ID        string    `gorm:"primaryKey;size:36" json:"id"`
    DeviceID  string    `gorm:"size:36;not null;index" json:"device_id"`
    Topic     string    `gorm:"size:100;not null;index" json:"topic"`   // 房地产/睡眠/俄乌战争
    Content   string    `gorm:"type:text;not null" json:"content"`       // 自由文本
    Variables *string   `gorm:"type:json" json:"variables,omitempty"`   // {"入睡时间":"00:30","咖啡":true,"质量":3}
    Tags      string    `gorm:"size:500" json:"tags"`                   // 逗号分隔: "政策,土地供应"
    CreatedAt time.Time `gorm:"autoCreateTime" json:"created_at"`
}
```

**与 bill 的区别：** bill 有严格 schema（金额/分类/日期），note 自由定义——content 放叙述，variables 放可对比的结构化键值对，tags 放标签。

---

## 三、工具函数定义

LLM 通过以下函数定义理解工具用法。描述需要足够详细，让 LLM 知道什么时候该记录、怎么提取信息。

### record_note

```
Name: record_note
Description: 保存一条值得追踪的笔记。当用户提供了值得长期追踪的信息时调用——包括事实、数据、观点、事件、个人状态等。日常闲聊和简单记账不需要记录。调用前先判断：这条信息一个月后还有回顾价值吗？

Parameters:
  topic     (必填) 主题。从用户消息中推断，用一个词概括：房地产/睡眠/俄乌战争/宏观经济/健康/...
  content   (必填) 笔记正文。忠实保留用户原话中的关键信息，不要过度总结、不要丢失细节。数据要精确，观点要标注是"用户观点"还是"事实"。
  tags      (可选) 逗号分隔的标签，辅助分类和检索。
  variables (可选) JSON对象，从用户消息中提取的结构化变量。用于后续定量对比分析。

Variables 提取规则：
- 用户提到了可量化的信息时才填，否则留空
- 二值变量用 true/false: {"咖啡": true, "运动": false}
- 数值变量直接填数字: {"质量": 3, "入睡时间": "00:30", "屏幕小时": 4.5}
- 键名用中文简写，值类型根据语义自动判断（bool/number/string）
- 不要硬编——用户说"睡得不好"不能推出 {"质量": 2}，只能说"睡得不好"存在 content 里

示例:
  "昨晚12点半睡，喝了咖啡，质量3分"
  → topic="睡眠", content="昨晚12点半入睡，喝了咖啡，睡眠质量自评3分",
    tags="失眠,咖啡", variables={"入睡时间":"00:30","咖啡":true,"质量":3}

  "住建部强调稳房价，避免市场大起大落"
  → topic="房地产", content="住建部表态：稳房价，避免市场大起大落。属于政策信号。",
    tags="政策,住建部", variables={}

  "上海5月二手房成交1.7万套，环比涨12%"
  → topic="房地产", content="上海5月二手房成交1.7万套，环比上涨12%。",
    tags="上海,二手房,交易数据", variables={"成交量":17000,"环比":12}
```

### query_notes

```
Name: query_notes
Description: 查询已保存的笔记。用于检索历史记录，然后基于结果做分析。

Parameters:
  topic      (可选) 按主题筛选
  keyword    (可选) 在 content 和 tags 中模糊搜索
  start_date (可选) 开始日期 YYYY-MM-DD
  end_date   (可选) 结束日期 YYYY-MM-DD

使用模式:
  1. 用户要求回顾 → 调 query_notes → 直接总结结果
  2. 用户要求深度分析 → 调 query_notes → 把结果交给 Pro 模型做因果推断
  3. 跨主题分析 → 多次调 query_notes(不同topic) → 汇总对比

示例:
  "我最近睡眠怎么样"
  → query_notes(topic="睡眠", start_date="最近一个月")
  → 拿到 15 条记录 → 分析: "本月记录15次，咖啡因摄入的夜晚平均质量2.1，无咖啡因的夜晚4.3..."
```

### content 存储原则：存原文，不总结

无论是短句还是长文，content 忠实保留用户原始输入。LLM 的职责是提取元数据（topic/tags/variables），不是改写内容。后续 Pro 分析需要看到原文，而不是 LLM 的二手转述。

```
短句:
  用户: "昨晚12点半睡，喝了咖啡，质量3分"
  → content = "昨晚12点半睡，喝了咖啡，质量3分"  （原样）

长文:
  用户: "（粘贴一篇3000字的房地产观察文章）"
  → content = （全文原样保存，不删减）
  → LLM 只需提取 topic="房地产"、tags、variables
  → 回复可以加一句话摘要，但 content 必须是原文
```

### 不记录的情况

以下情况**不需要**调 record_note：
- 日常闲聊（"你好"、"今天天气不错"）
- 简单的记账操作（已经有 bookkeeping 工具）
- 一次性查询（"现在几点"、"1+1等于几"）
- 用户明确说"不要记录"

---

## 四、API

```
GET    /api/notes?topic=房地产&keyword=政策
  → [{id, topic, content, tags, variables, created_at}, ...]

DELETE /api/notes/:id
  → 删除单条笔记

GET    /api/notes/export
  → CSV 导出
```

跟 bills 的 API 风格一致。

---

## 五、改动清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `model/note.go` | 新 | Note 模型 |
| `service/note_repo.go` | 新 | CRUD（含 Delete）+ 查询 |
| `tool/note.go` | 新 | 实现 Tool 接口 |
| `controller/note.go` | 新 | GET /api/notes + export |
| `controller/router.go` | 改 | 注册路由 |
| `cmd/main.go` | 改 | 注册 note tool |

**改动量：** 约 300 行，结构完全参考 bookkeeping。

---

## 六、与向量数据库联动（后续迭代）

当前 query_notes 靠 topic + keyword 精确匹配，数据量小的时候够用。引入向量数据库后，检索能力质变：

### 自动注入相关记忆

```
当前: 用户说"我最近睡得不好"
  → 系统只能靠记忆系统注入已提取的偏好（"用户通常12点后入睡"）
  → 但不会自动检索相关笔记

向量化后: 同一句话
  → embedding 检索所有笔记 → 命中 "上周记录: 咖啡+熬夜→质量1分"
  → 命中 "两周前: 换了遮光窗帘→质量提升"
  → 命中 "一个月前观点: 怀疑是咖啡因导致的"
  → 三条笔记自动注入 system prompt
  → LLM: "你最近三次失眠记录都提到咖啡因，而且两周前换窗帘后有改善但没持续。
       要验证咖啡因假说，建议连续一周不喝咖啡记录对比。"
```

### 跨主题关联发现

```
向量相似度聚类 → 发现"睡眠"和"咖啡消费"的笔记高度相关
  → 主动提醒: "你的睡眠质量和咖啡摄入在记录中出现了17次交叉，可能存在因果关系"
```

### 长期趋势检测

```
向量空间追踪 → "房地产"笔记从"看跌"语义区逐步漂移到"观望"语义区
  → 提示: "你对房地产的判断在半年内发生了显著变化，从悲观转向观望。
       要回顾一下是什么信息导致了这次转变吗？"
```

### 实现路径

| 阶段 | 内容 |
|------|------|
| 当前 | topic + keyword 文本匹配，数据少够用 |
| P6-3 记忆增强 | 引入 embedding API，笔记存入时自动向量化 |
| P6-3 检索升级 | query_notes 支持语义搜索，"最近失眠"能匹配到"睡得不好" |
| P7 主动分析 | 定时任务扫描向量空间，发现趋势和异常主动推送 |
