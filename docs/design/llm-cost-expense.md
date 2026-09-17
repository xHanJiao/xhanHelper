# LLM 每日花销统计（成本中心 Phase 2）

> 2026-08-19

## 概述

Phase 1（llm-cost-center）已建立 token 消耗追踪：`llm_cost_details` 表记录了每次 LLM 调用的日期、分类、模型、缓存命中/未命中/生成 token。本阶段在此基础上增加**花销（¥）**计算与展示：

- 新增模型定价表 `llm_model_prices`，以「模型 + 生效日期」维度记录单价，应对供应商调价
- 支持**峰谷定价**：每行配置多个峰时段（可多段、可配置），峰段内用峰价、其余用谷价，按**北京时间**判定
- 按日聚合 token → 按当日生效价格折算花销 → 前端新增「每日花销」堆叠柱状图
- 复用现有分类/用户/时间筛选，实现"看某个分类的开销"
- 价格由管理员在成本中心页面手工录入维护，不写 config.yaml

核心原则：
- 定价与峰时段是随时间变化的维度数据，存库不存配置；计算时取「当日生效」的记录
- 只做花销展示，不反向修改 `llm_cost_details`（历史数据以查询时价格动态折算）
- 峰时段必须按北京时间判定，且**不能依赖 SQL 字符串函数**（SQLite strftime 会转 UTC、MySQL 依赖 DSN 时区），统一在 Go 内存用 `config.BJ`（Asia/Shanghai）换算

## 为什么不用 config.yaml

- 价格随供应商调整而浮动，同一模型不同月份单价不同
- 峰谷时段窗口同样会随供应商调整（DeepSeek 历史上调整过错峰窗口），与价格一样需要按生效日期追溯
- 若写死在 config，历史花销会按新窗口/新价格重算，永远算不出真实的每日曲线
- 定价记录是"数据"而非"配置"：需要可追溯（何时改的、改成多少），DB 表天然支持

## 数据模型

### 新表 `llm_model_prices`

```go
// server/internal/model/llm_price.go

package model

import "time"

// LlmModelPrice 模型单价记录（元 / 1M tokens），按生效日期分段。
// 某天某模型的有效价格 = effective_date <= 当天 的最新一条记录。
// 峰谷：时间落在 PeakSegments 任一段内（开始含、结束不含）用峰价，其余用谷价；峰段为空 = 全天谷价。
// 所有时段按北京时间（Asia/Shanghai）判定。
type LlmModelPrice struct {
    ID             uint      `gorm:"primaryKey" json:"id"`
    Model          string    `gorm:"type:varchar(50);not null;index:idx_model_date,priority:1" json:"model"`
    EffectiveDate  string    `gorm:"type:date;not null;index:idx_model_date,priority:2" json:"effective_date"`
    PriceHit       float64   `gorm:"not null" json:"price_hit"`        // 峰：缓存命中，元/1M tokens
    PriceMiss      float64   `gorm:"not null" json:"price_miss"`       // 峰：缓存未命中
    PriceOutput    float64   `gorm:"not null" json:"price_output"`     // 峰：生成
    PriceHitOff    float64   `gorm:"not null" json:"price_hit_off"`    // 谷：缓存命中
    PriceMissOff   float64   `gorm:"not null" json:"price_miss_off"`   // 谷：缓存未命中
    PriceOutputOff float64   `gorm:"not null" json:"price_output_off"` // 谷：生成
    PeakSegments   string    `gorm:"type:text" json:"peak_segments"`   // 峰时段 JSON：[["08:30","12:00"],["14:00","17:00"]]；空串 = 全天谷
    WeekendAllOff  bool      `gorm:"not null;default:false" json:"weekend_all_off"` // true = 周六/周日全天谷价
    CreatedAt      time.Time `gorm:"autoCreateTime" json:"created_at"`
}
```

### 字段说明

| 字段 | 含义 | 说明 |
|------|------|------|
| Model | 模型名 | 与 `llm_cost_details.model` 一致，如 `deepseek-v4-flash` |
| EffectiveDate | 生效日期 YYYY-MM-DD | 从这天（含）起生效，直到该模型的下一条记录生效前一天 |
| PriceHit/Miss/Output | 峰价 | 元 / 1M tokens |
| PriceHitOff/MissOff/OutputOff | 谷价 | 元 / 1M tokens |
| PeakSegments | 峰时段 | 存储 compact JSON `[["HH:MM","HH:MM"],...]`，由前端文本 `08:30-12:00,14:00-17:00` 规范化而来；空串 = 全天谷价 |
| WeekendAllOff | 周末全天谷 | true 时周六/周日恒按谷价，PeakSegments 仅作用于周一~周五；默认 false（周末沿用同一峰段），存量记录行为不变 |

### 峰谷语义

- 同一模型可有多条价格记录（按 effective_date 分段），某天取「effective_date ≤ 当天」的最新一条
- 该行自己的 PeakSegments 决定当天几点是峰：时间落在任一峰段内（开始含、结束不含）用峰价，否则用谷价
- 一天可配置多段峰；峰以外全部是谷
- `WeekendAllOff` 勾选后，周六/周日的峰段判定被跳过，全天按谷价（对应供应商「周末全天谷」规则）；周末类型由北京时间日期串 `date` 推出，不依赖 `created_at` 换算。节假日不在支持范围内
- **无兼容逻辑**：这是全新功能，价格表无历史数据，不做任何回退/默认规则（谷价必须显式填写，可为 0）

### 时段与时区

- 所有时段一律北京时间（`config.BJ` = Asia/Shanghai），页面与提示均标注
- 峰谷判定基于 `llm_cost_details.created_at`（真实时刻 instant），在 Go 内 `.In(config.BJ).Format("15:04")` 换算成北京时分
- 同一时刻语义：`db_recorder.go` 写入 `Date` 列也统一改用 `.In(config.BJ)` 格式化，避免服务器时区非北京时日期归属偏差

## 花销计算

### 公式

```
某条明细 cost = hit/1e6 * 该时刻价格 + miss/1e6 * 该时刻价格 + output/1e6 * 该时刻价格
其中「该时刻价格」= 当日生效价格行的峰价或谷价（由峰段判定）
```

### 计算流程（ExpenseQuery）

```
1. 加载 llm_model_prices 全表，按 model 分组、effective_date 升序排序

2. SQL 拉取范围内明细原始行（date, model, category, created_at, 三个 token）
   —— 不做 SQL 聚合：峰谷需北京时分，SQL 字符串提取跨方言/时区不可靠

3. Go 内存聚合：key = (date, model, category, 北京HH:MM)，累加 token

4. 对每个聚合键：
   - 找该模型当日生效价格行（effective_date <= date 最新一条）
   - 解析该行 PeakSegments JSON → 判峰谷 → 取峰价或谷价
   - 按公式折算 cost，累计到：
     days[date].cost / cost_hit / cost_miss / cost_output / cost_peak / cost_off
     categories[category].cost、models[model].cost
     total / total_peak / total_off
   - 无有效价格 → 计入 unpriced_models（不静默计 0）

5. days 按 date 升序、categories/models 按 cost 降序返回
```

> ponytail: 个人量级（30 天数万行）内存扫描毫秒级。若数据量级到百万行/月，升级为写时落 `time_of_day` 列（db_recorder 计算存 "HH:MM" 北京时分字符串）+ SQL GROUP BY。

## Service 层

新文件 `server/internal/service/llm_price_repo.go`：

```go
type LlmPriceRepo struct { db *gorm.DB }

// List 全量价格记录，按 model, effective_date 升序
func (r *LlmPriceRepo) List(ctx context.Context) ([]model.LlmModelPrice, error)

// Save Upsert：同 (model, effective_date) 存在则更新，否则插入
func (r *LlmPriceRepo) Save(ctx context.Context, p *model.LlmModelPrice) error

// Delete 删除单条记录
func (r *LlmPriceRepo) Delete(ctx context.Context, id uint) error
```

峰段解析与判定（service 层纯函数，controller 校验与查询共用）：

```go
// NormalizePeakSegments 文本 "08:30-12:00,14:00-17:00" → compact JSON 字符串（空串=全天谷）
func NormalizePeakSegments(text string) (string, error)

// parsePeakSegmentsJSON 存储 JSON → [][]string（空串 = 无峰段）
func parsePeakSegmentsJSON(s string) ([][]string, error)

// isPeak 北京时间 HH:MM 是否落在任一峰段内（开始含、结束不含）
func isPeak(hm string, segs [][]string) bool
```

花销聚合复用现有 `LlmCostRepo`，新增方法：

```go
func (r *LlmCostRepo) ExpenseQuery(ctx context.Context, params QueryParams, prices []model.LlmModelPrice) (*ExpenseResult, error)
```

（`LlmCostRepo` 需要访问价格表；价格 repo 与 token repo 同属 service 层，由 controller 分别注入后组合调用。最终以「controller 拉价格 → repo 按价格算钱」的方式串联。）

### ExpenseResult 结构

```go
type ExpenseDay struct {
    Date       string  `json:"date"`
    Cost       float64 `json:"cost"`
    CostHit    float64 `json:"cost_hit"`
    CostMiss   float64 `json:"cost_miss"`
    CostOutput float64 `json:"cost_output"`
    CostPeak   float64 `json:"cost_peak"`
    CostOff    float64 `json:"cost_off"`
}

type ExpenseResult struct {
    Total           float64         `json:"total"`
    TotalHit        float64         `json:"total_hit"`
    TotalMiss       float64         `json:"total_miss"`
    TotalOutput     float64         `json:"total_output"`
    TotalPeak       float64         `json:"total_peak"`
    TotalOff        float64         `json:"total_off"`
    Days            []ExpenseDay    `json:"days"`            // 按日期升序
    Categories      []ExpenseGroup  `json:"categories"`      // 按 cost 降序
    Models          []ExpenseGroup  `json:"models"`          // 按 cost 降序
    UnpricedModels  []string        `json:"unpriced_models"` // 有消耗但无有效价格
}
```

## API 设计

全部挂在现有 `/api/admin/cost` Admin 路由组下（Auth + Admin 中间件沿用）。

### 1. `GET /api/admin/cost/expense` — 花销聚合（新增）

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| start | string | 否 | 开始日期 YYYY-MM-DD（含） |
| end | string | 否 | 结束日期 YYYY-MM-DD（含） |
| user_id | string[] | 否 | 多选用户，同 stats 端点 |
| category | string | 否 | 大类筛选（分类花销视图靠它实现） |

响应即 `ExpenseResult`（见上）。示例：

```json
{
  "total": 12.34,
  "total_hit": 1.2, "total_miss": 5.6, "total_output": 5.54,
  "total_peak": 8.9, "total_off": 3.44,
  "days": [
    {"date": "2026-08-18", "cost": 1.23, "cost_hit": 0.1, "cost_miss": 0.5, "cost_output": 0.63,
     "cost_peak": 0.8, "cost_off": 0.43}
  ],
  "categories": [{"key": "main_chat", "cost": 9.8}, {"key": "reflection", "cost": 2.54}],
  "models": [{"key": "deepseek-v4-flash", "cost": 10.1}],
  "unpriced_models": []
}
```

### 2. `GET /api/admin/cost/prices` — 价格列表（新增）

```json
[
  {"id": 1, "model": "deepseek-v4-flash", "effective_date": "2026-08-01",
   "price_hit": 0.5, "price_miss": 2, "price_output": 8,
   "price_hit_off": 0.25, "price_miss_off": 1, "price_output_off": 4,
   "peak_segments": "[['08:30','12:00']]", "created_at": "..."}
]
```

按 model + effective_date 升序返回，前端直接渲染表格。

### 3. `POST /api/admin/cost/prices` — 新增/更新价格（新增）

请求体（`peak_segments` 为用户输入文本，后端校验并规范化为 JSON 存储）：

```json
{"model": "deepseek-v4-flash", "effective_date": "2026-08-20",
 "price_hit": 0.5, "price_miss": 2, "price_output": 4,
 "price_hit_off": 0.25, "price_miss_off": 1, "price_output_off": 2,
 "peak_segments": "08:30-12:00,14:00-17:00"}
```

- 校验：model 非空、effective_date 格式 YYYY-MM-DD、6 个价格 >= 0
- `peak_segments` 校验：逗号分隔多段 `HH:MM-HH:MM`，每段格式合法且 start < end；空串/空白 = 全天谷；非法返回 400 并附格式说明
- 规范化：解析后存 compact JSON `[["08:30","12:00"],["14:00","17:00"]]`
- Upsert：同 (model, effective_date) 已存在 → 更新并返回原记录
- 响应：保存后的完整记录

### 4. `DELETE /api/admin/cost/prices/:id` — 删除价格（新增）

按主键删除。历史花销立即按剩余记录重算（查询时动态折算，无需重建数据）。

### 5. `GET /api/admin/cost/models` — 模型下拉列表（新增）

从 `llm_cost_details` 去重取模型名（升序），供定价表单下拉选择。

### 6. `GET /api/admin/cost/hourly` — 单日分时（24 小时）（新增）

| 参数 | 类型 | 必填 | 说明 |
|------|------|------|------|
| date | string | 是 | 目标日期 YYYY-MM-DD |
| user_id | string[] | 否 | 多选用户 |
| category | string | 否 | 大类筛选 |

```json
{
  "date": "2026-08-19",
  "hours": [
    {"hour": "00", "hit": 0, "miss": 0, "output": 0, "cost": 0, "cost_hit": 0, "cost_miss": 0, "cost_output": 0},
    "... 固定 24 项，hour 为 00-23 ..."
  ],
  "totals": {"hit": 123, "miss": 456, "output": 789, "cost": 1.23,
             "cost_hit": 0.4, "cost_miss": 0.5, "cost_output": 0.33, "cost_peak": 1.0, "cost_off": 0.23},
  "unpriced_models": []
}
```

- 实现：拉 `date = ?` 明细行 → Go 内存按 `(model, category, 北京hour)` 聚合；**逐行按真实分钟判峰谷**（`priceAt` 用 `HH:MM`），再累加到小时桶，分钟精度不受小时桶影响
- token 数不依赖价格照常累计；无有效价格的模型只影响 cost 并记入 `unpriced_models`
- 用途：柱状图点击联动（每日 → 单日 24 小时分时）

## 前端改动

文件：`front-end/src/views/AdminView.vue`（成本中心 Tab，沿用现有内联风格）。

### 交互：柱状图点击钻取单日分时

- 点击「每日 Token 消耗」或「每日花销」任一柱子 → 两个柱状图同时切到该日 24 小时分时视图（x 轴 `00:00`–`23:00`）
- 饼图（分类/模型/子类）跟随：统计范围收窄到选中日（`effectiveRange()`）
- 退出：图表区顶部「← 返回按日」按钮；再次点击已选中的柱子也可退出
- 筛选（分类/用户/堆叠维度）在分时视图仍生效；**切换时间范围自动退出分时**
- 分时 token 图：堆叠 hit/miss/output（跟随勾选），不画「平均 token/次」折线
- 分时花销图：堆叠 cost 三色 + 柱顶 ¥ 总值（barTotalPlugin 复用）
- 花销卡片标题：分时模式显示该日总花销（峰/谷），unpriced 警告沿用

### 布局变化

```
筛选栏（现有，不动）
  时间范围 ▼  用户 ▼  分类 ▼        [堆叠勾选: 命中/未命中/生成] [刷新]

概览卡片（现有，不动，仍为 token 维度）

图表区
  ┌─ 每日 Token 消耗（现有柱状图）───────────────┐
  ├─ 每日花销 (¥)（新增柱状图）──────────────────┤
  │   标题旁: 区间总花销 ¥X（峰 ¥P / 谷 ¥O）      │
  │   若 unpriced_models 非空 → warning 提示      │
  └─────────────────────────────────────────────┘
  ┌─ 分类分布 / 模型分布 / 子类分布（现有饼图，不动）─┐
  ┌─ 模型定价（新增卡片）─────────────────────────┐
  │  表单: [模型▼] [生效日期]                     │
  │       [峰·命中][峰·未命中][峰·生成]            │
  │       [谷·命中][谷·未命中][谷·生成]            │
  │       [峰时段文本 08:30-12:00,14:00-17:00] [保存] │
  │  列表: 模型 | 生效日期 | 峰 x/y/z | 谷 x/y/z | 峰段 | [删除] │
  └─────────────────────────────────────────────┘
```

### 新增逻辑

- `fetchCostExpense()`：带与 stats 相同的筛选参数调 `/expense`，返回 days/categories/models/total/total_peak/total_off/unpriced_models
- 新增柱状图 `cost-expense-chart`：堆叠 `cost_hit` / `cost_miss` / `cost_output` 三色（复用现有 `makeBarChart` + 勾选开关 `costBarStack`，与 token 图联动）
- 定价表单：模型下拉（`/cost/models` 来源）+ 生效日期 + 峰/谷 6 个 `NInputNumber` + 峰时段文本框
- 峰段文本 placeholder 与提示明确：`08:30-12:00,14:00-17:00`、留空=全天谷、开始含/结束不含、北京时间
- 价格列表：峰/谷三价 + 峰段（`fmtPeakSegments` 把存储 JSON 转回 `08:30-12:00, 14:00-17:00` 文本，空=全天谷）
- 花销卡片标题：`总 ¥X（峰 ¥P / 谷 ¥O）`
- 保存/删除后重新拉 prices + expense
- 金额格式化 `fmtMoney(n)`：`¥` + `toFixed(2)`

### 分类开销的查看方式

不做新图。复用现有「分类筛选」下拉：选中某分类 → expense 请求带 `category` 参数 → 柱状图、总花销、分类饼图（categories 组只剩该分类）全部反映该分类。这是零新 UI 成本的实现。

## 接线改动

| 文件 | 改动 |
|------|------|
| `server/internal/model/llm_price.go` | 新增 LlmModelPrice 模型（峰/谷 6 价 + 峰段） |
| `server/internal/app/init_db.go` | AutoMigrate 列表追加 `&model.LlmModelPrice{}` |
| `server/internal/service/llm_price_repo.go` | LlmPriceRepo（List/Save/Delete）+ 峰段解析/判峰谷 |
| `server/internal/service/llm_cost_repo.go` | 新增 ExpenseQuery + 结构体 + ListModels |
| `server/internal/llmcost/db_recorder.go` | Date 列改 `.In(config.BJ)` 格式化 |
| `server/internal/controller/cost.go` | 新增 5 端点；CostRoutesConfig 增加 `PriceRepo` |
| `server/internal/app/init_repos.go` | 新增 `LlmPrice` repo 实例 |
| `server/internal/appinit/router.go` | RegisterCostRoutes 传入 PriceRepo |
| `front-end/src/views/AdminView.vue` | 花销柱状图 + 定价管理卡片 + 峰谷字段 |

## 实施步骤

1. 模型 + AutoMigrate + LlmPriceRepo（List/Save/Delete）✅
2. 峰段解析/判峰谷函数 + ExpenseQuery（Go 内存聚合 + 北京时区）+ 单测 ✅
3. db_recorder Date 改北京时间 ✅
4. controller 5 端点（含 models）+ 路由接线 ✅
5. 前端：expense 图表 + 定价卡片（峰谷 + 峰段）+ 金额格式化 ✅
6. HourlyQuery + `/hourly` 端点 + 单测 ✅
7. 前端柱状图点击钻取单日分时 + 饼图跟随 + 返回交互 ✅
8. 验收：`go build ./...` + `go test ./...` 全绿 + `vite build` ✅（真机点击联动待人工确认）

## 风险与边界

- **历史数据无价格**：价格表初始为空，历史花销全按 0 计并提示 unpriced。管理员需回填「首条价格」（生效日期设为最早数据日期之前），这是预期操作流程
- **价格/峰段修改影响历史**：价格记录是分段生效的，修改某段价格或峰段后，该段覆盖日期内的历史花销会变化。这是"动态折算"的固有特性，符合设计意图（价格更正应反映真实花销）
- **时区**：峰谷严格按北京时间。峰谷判定在 Go 内存用 `config.BJ` 换算 created_at，不依赖 SQL；`Date` 列写入也统一北京时间。若未来服务器部署到非北京时区，created_at 仍是真实时刻，判定不受影响
- **Embedding 不计价**：Phase 1 就不统计 Embedding，本阶段保持一致
- **币种**：按人民币 ¥，单价单位统一为「元 / 1M tokens」。若未来接其他币种供应商（如美元计价的 Claude），需在价格表加 currency 字段——当前 YAGNI，不加
- **价格表无审计日志**：只有 created_at，没有"谁改的"。个人自用系统够用；若未来多人维护可加 updated_at/操作者字段
- **同模型同日期重复保存**：Upsert 覆盖，不会产生重复分段
- **浮点精度**：金额用 float64，展示层 toFixed(2)。个人开销量级下精度足够，不做定点数
- **明细行全量扫描**：ExpenseQuery 拉取范围内全部明细行做内存聚合。个人量级（30 天数万行）毫秒级；百万行/月量级时升级为写时落 `time_of_day` 列 + SQL GROUP BY（见代码 ponytail 注释）
