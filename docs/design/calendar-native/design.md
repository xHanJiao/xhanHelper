# 本机日历（EventKit）：日历工具 + 提醒写入日历

> 2026-10-29 · 方案已定稿（用户拍板：备忘录不做；写操作进 v1；提醒「日历优先，写不进再回退通知」）

**估算（定稿）：4 面 / 4 缝（0 条需先建探针）/ 4~5 次真机往返 / 0 个待拍决定 → 11~17h（约 1.5~2.5 个工作日）**

| 面 | 内容 | 估时 |
|---|---|---|
| 日历只读工具 | `calendar__list_events` / `search_events` | 4~6h |
| 日历写工具 | `create/update/delete_event` + 可写日历挑选 + L4 确认 | 3~5h |
| 提醒镜像 | 映射存储 / 权限缓存 / diff 删除 / 回退通知 / 设置开关 | 4~6h |
| 纯逻辑 + 单测 | 日历挑选与字段映射（SwiftPM `swift test`） | 含在上面 |

## 背景

助手目前只能靠**本地系统通知**提醒用户（`NotificationManager` + `UNCalendarNotificationTrigger`），
通知只在这台 Mac 上响；而用户主要在 iPhone 上收提醒。日历事件走 iCloud 自动同步到手机，
所以把「提醒」落到日历上，是让提醒跨设备的唯一低成本路径。

同时助手本身也应该能看到日历：用户问「我明天有什么安排」「下周三下午空不空」。

## 非目标

- 备忘录（Notes）：无公开 API，只能走 AppleScript + 自动化授权，本期不做
- Apple 提醒事项（EventKit reminders）：已有自建提醒系统，重复
- 循环提醒的日历展开：服务端 `recurrence_rule` 目前只存不用（`agent.NextDeadline` 是死代码），镜像时忽略
- iOS 端：`client_tools` 由 macOS 端上报，iOS 端本来就不参与；代码用 `#if os(macOS)` 包住
- 邀请参会人：EventKit 不支持加 attendee / 发邀请，能力上限就是「在用户自己日历上建事件」

## 架构

新增的两个东西都活在 **Mac 客户端**，Go 后端一行不改：

```
LLM ──calendar__*──▶ client_tool_call ──▶ CalendarToolProvider (EventKit)
                                  └────▶ 结果回传

reminder 工具(Go) → Reminder(pending) → SSE reminders_changed
   → ContentView.syncReminders() → GET /api/reminders?status=pending
   → NotificationManager.syncWithServer(pending:total:)
        ├─ ReminderCalendarMirror：能写 → 建日历事件（带闹钟）
        └─ 写不了 → 原样排本地通知
```

服务端零改动：客户端工具按 `server_name` 分组，`calendar` 命名空间不与任何服务端工具冲突；
渐进式暴露只认 `group_description`，没有白名单。

## 日历工具（`Shared/Tools/CalendarToolProvider.swift`）

| 函数 | 参数 | 说明 |
|---|---|---|
| `list_events` | `start` `end` `calendar?` `limit(50)` | 窗口内事件，按开始时间升序，紧凑文本 |
| `search_events` | `query` `days_back(30)` `days_forward(30)` `limit(30)` | 标题/备注/地点包含匹配 |
| `create_event` | `title` `start` `end?` `calendar?` `notes?` `all_day?` `alarm_minutes?` | 返回 `event_id` |
| `update_event` | `event_id` `occurrence?` + 可选字段 | 可改 `all_day` / `alarm_minutes`；重复事件必须带 `occurrence` |
| `delete_event` | `event_id` `occurrence?` | 重复事件必须带 `occurrence` |

- 读取结果形如 `2026-10-30 14:00-15:00 周会 @工作 id=<id> t=<起始 unix 秒>`，单行一条，超预算截断并提示缩小范围
- **`t=` 是「哪一次」的锚点**：EventKit 没有单次 id，重复事件每次出现的 `eventIdentifier` 相同，`store.event(withIdentifier:)` 拿回的是系列首条；改/删靠 `id + t` 用 1 秒窗口定位那一次。重复事件不带 `t` 直接报错，绝不落到系列第一次
- 风险等级：读 = L2（免确认，显式登记）；`create/update/delete` = **L4（红色必弹）**，登记在 `ToolRiskLevels.byTool`
- 确认条展示名登记在 `LocalToolRegistry.shortNames`（「创建日程」…）

### 目标日历挑选（纯逻辑，`Shared/Tools/CalendarEventCore.swift`，SwiftPM 可测）

`CalendarPicker.pick(candidates:explicitName:)`，候选来自 `eventStore.calendars(for: .event)`：

1. 指定了名字 → 精确匹配，其次包含匹配；匹配到但不可同步 → 失败并列出可写日历
2. `defaultCalendarForNewEvents` 且可同步 → 用它
3. 可同步候选里 `accountTitle == "iCloud"` 的第一个
4. 其余可同步候选（Google/Exchange）的第一个
5. 全都不可同步（只有本地/订阅/生日日历）→ **不写**，返回「本机没有能同步到手机的日历 + 现有可写清单」

「可同步」= `allowsContentModifications && sourceType ∉ {local, subscribed, birthdays}`。

## 提醒镜像（`Shared/Services/ReminderCalendarMirror.swift`）

挂在 `NotificationManager.syncWithServer(pending:total:)` 里，规则三条：

| 情况 | 动作 |
|---|---|
| pending 且能写日历 | 建事件（标题=content 截 60 字，备注=`notification_body` + 归属标记，`deadline → +15min`，`EKAlarm(relativeOffset: 0)`），**不排本地通知** |
| pending 但写不了（无授权 / 无可同步日历 / save 报错） | 走原来的 `schedule(reminder:)`，行为不变 |
| 映射里有、这次 pending 里没了（ack 或 cancel） | 删事件 + 清映射 |

- 事件在提醒**仍是 pending** 期间一直保留（含已过点但还没 ack 的），过期后日历里仍看得到今天开过什么会
- **用户手删事件 = 尊重删除**：映射在、事件没了（且有读权限）→ 清映射、记墓碑（`reminder_calendar_dropped`）、不重建，该提醒回落系统通知
- **关掉开关只停新建**：已建事件保留（那是用户日历里的东西，删掉还会同步删到手机、连带抹掉他改过的内容），只是不再给它们排通知（避免闹钟 + 通知双响）；提醒 ack/取消的照旧删
- 想彻底清干净走设置页显式入口「已写入日历的提醒事件 N 条 → 清理…」（红色确认，说明会同步删到其他设备）；清理后这些提醒回落系统通知，**不会**再被下一轮同步写回日历
- **读不到日历（无权限）时不判「被删」**：一律假定映射里的事件还活着（不排通知、不新建），否则会在无权限时把「读不到」当成「用户删了」

- 映射 `reminder.id → eventIdentifier` 存 `UserDefaults`（key 走 `LocalPaths.scoped`，dev/prod 不串味）
- 事件备注末尾写 `[xhanhelper:<reminder_id>]`：映射丢失（换机/清偏好）时按标记在截止时间附近认领旧事件，不在日历里重建第二份；这也是将来上 iOS 客户端时做「双响去重」的依据
- 权限/日历不可用做**会话级负缓存**：denied 之后不再请求、不再逐条试，只打一条日志
- **截断保护**：`total != pending.count`（超过一页 50 条）时跳过「按页 diff 删除」这一轮，防误删日历事件
- 开关：`@AppStorage` key `reminder_sync_to_calendar`，默认开（**Debug 构建默认关**，见后续变更 2026-11-14）；设置页给状态展示 + 「请求权限」按钮，权限状态随 App 激活刷新

### 取舍：日历闹钟替代本地通知（方案 A，用户已拍板）

日历事件带 `EKAlarm(relativeOffset: 0)`，Mac 上不再排本地通知 —— 两边都排会在同一分钟弹两次。
代价：点击闹钟打开的是日历 App，**失去「点通知 → 助手在聊天里把提醒提起来」**（`handleReminderTap`）。
该流程保留，仅服务于回退路径（写不进日历的提醒）。用户明确同意。

## 权限

- Info.plist：`NSCalendarsFullAccessUsageDescription`（`project.pbxproj` 的 `INFOPLIST_KEY_*`，macOS Debug + Release 各一条）
- 读写共用一次「完整访问」授权；app 未开沙盒（`ENABLE_APP_SANDBOX = NO`），不需要 calendars entitlement
- **请求时机**：提醒镜像在「存在可镜像的 pending 提醒」的前台同步里请求一次（用户刚说完「提醒我…」，时机自然）；
  `calendar__*` 首次调用也会请求。拒绝后本会话不再请求，回退系统通知；设置页显示权限状态、给「请求权限」兜底，
  并明确写「未授权时只在 Mac 上提醒，不同步到手机」
- iOS 端不上客户端（无开发者账号，手机用 web），因此 iOS 侧不做镜像/授权

## 验证清单（真机）

1. 首次建提醒 → 系统弹日历授权 → 允许后日历里出现事件、本地无通知
2. iPhone 日历 App 打开即看到同一条事件（iCloud 同步）
3. 拒绝授权 → 提醒照旧走本地通知，日志一条、不反复请求
4. 只有本地日历（无 iCloud）→ 不写事件，回退通知，工具结果/日志给出人话
5. 取消提醒 → 日历事件消失
6. `calendar__list_events` 读当天 → 与日历 App 一致
7. `calendar__create_event` → 确认条显示标题/时间/账号，批准后 iPhone 可见
8. 重复日程（每周例会）改/删 → 只动列表里 `t=` 那一次，系列其他次不变
9. 关掉「提醒同步到日历」→ 已建事件**保留**、新提醒不再进日历、这些提醒不再排通知（不双响）；「清理…」按钮可显式删掉
10. `swift test`（CalendarCoreTests）绿；`swiftc -typecheck` macOS/iOS 两批绿

## 后续变更

### 2026-10-29 · 按 code review 修 P1/P2/P3

`docs/code-review/calendar-native/2026-10-29.md` 的处理结果：

- ~~**P1-2（关开关双响）**：关闭开关改为「回收」——删掉映射里的事件并清映射，回到纯通知~~
  **⚠️ 已撤销（同日第三轮）**：关开关改为「只停新建、事件保留、不排通知」，清理走设置页显式按钮。见下方第三轮条目。
- **P1-1（iPhone 双响）**：不修。没有 iOS 客户端（无开发者账号，手机走 web），本条不成立；
  代码里留 `shortcut:` 说明，将来上 iOS 时用事件备注的 `[xhanhelper:<reminder_id>]` 反查去重
- **P2-3（授权时机）**：实现不动（首次有提醒时请求是对的，日历是手机唯一提醒通道），改的是本文档措辞 + 设置页文案
- **P2-4（重复事件）**：`describe` 补 `t=`；`update/delete` 用「id + 1 秒窗口」定位那一次，重复事件不带 `t` 直接报错
- **P2-5（映射丢失）**：事件备注打标记 + 建前在截止时间附近认领
- **P2-6**：`shortcut:` 注释（主线程同步查询 EventKit）
- **P2-7**：`update_event` 补 `all_day` / `alarm_minutes`
- **P3 项**（第一轮编号）：schema 条数文案、`count`/`total` 分开、重叠撤除加注释、`.writeOnly` 置位、设置页随激活刷新、读工具显式登记 L2

真机补验两条：重复日程改/删只动那一次；关开关后不再双响。

### 2026-10-29 · 第二轮审查：撤销「关开关 = 回收」，手删改为尊重

上一条把「关开关」做成**批量删除已镜像事件**，被指出代价过大（会同步删到手机、抹掉用户在日历里的修改），
且「无权限就不排通知」那条分支在无读权限时恒为空集、实际不生效（日志还说已避免，属于说谎）。
本轮修正：

- **关开关只停新建**：事件保留、不排通知；清理改为设置页显式按钮（显示条数 + 红色确认）
- **手删事件 = 尊重删除**：墓碑集合 `reminder_calendar_dropped`（`UserDefaults`，随提醒结束清理），该提醒回落系统通知
- **无权限分支**：无法判断事件是否还在 → 用 `Set(mapping.keys)` 假定存活（不排通知、不新建），日志改成实话
- **性能**：`adoptExisting` 的「每条提醒一次谓词查询」合并成一次区间查询（`marker → id` 表）
- **读日历按名过滤**：新增 `resolveCalendarForReading`（本地/订阅日历也能读），写路径仍只认可同步日历
- **可测性**：抽出 `CalendarCore.matchIndex` / `needingNotification` 两个纯函数并补单测
- **静默跳过**：deadline 解析失败 / 用户已删的提醒，跳过时打 diag 日志

影响文件：`ReminderCalendarMirror.swift`（重写）、`CalendarStore.swift`、`CalendarToolProvider.swift`、
`CalendarEventCore.swift`、`xHelperMac/App.swift`、`Tests/FsCoreTests/CalendarCoreTests.swift`。

### 2026-10-29 · 第三轮：确认条人话摘要 + 通知排布抽测 + 读路径/全天/DST/翻页

- **L4 确认条**（此前 80 字截断看不到标题/时间/日历）：`CalendarCore.confirmationSummary` 出人话，形如
  `创建日程：评审会 ｜ 2026-10-30 14:00 至 15:00 ｜ 工作`；`ChatViewModel.awaitToolConfirmation` 对 `calendar__*` 走这条
- **通知排布抽成纯函数** `CalendarCore.notificationPlan`（`toSchedule` / `toRemove`），补两条单测（已镜像/关开关、截断不撤）；
  `NotificationManager` 只负责执行计划
- **读日历按名**：`resolveCalendarForReading` 允许任意日历（本地/订阅可读），写路径仍只认可同步日历
- **同名日历歧义**：`pick` 先在「可同步」候选里匹配，本地「工作」不再挡住 iCloud「工作」
- **全天事件归一**：`normalizeAllDay` 用 `Calendar.date(byAdding:.day)` 取当天 00:00 → 次日 00:00（夏令时也是整一天）
- **认领旧事件只搜可写日历**：避免认领到只读/订阅日历里的事件（那份删不掉、清不干净）
- **`calendar__delete_event` 删镜像事件**：解析备注标记 → `forget(reminderID:)` 记墓碑（不再重建），结果里说明「改为系统通知」
- **跳过原因不再静默**：deadline 解析失败 / 已过点 / 正文未生成，按提醒 id 去重打一条 diag
- **翻页**：`syncReminders` 按 50/页翻最多 4 页（200 条），第 51 条起不再漏镜像/漏通知
- **关开关不再弹授权框**：只在已授权时才做「已结束提醒」的事件清理

### 2026-10-29 · 第四轮：清理不再被重建 + 通知字典去重 + 认领先于判死

- **「清理…」不再转眼长回来**：`purgeAll` 删完把提醒 id 写进墓碑集合（`dropped`），这些提醒回落系统通知；
  此前是 `dropped.removeAll()`，下一轮同步 mapping 已空 + 认领不到 → 全量重建
- **`detectDropped` 先按标记认领再判死**：`eventIdentifier` 会因同步/拆分而变，映射里的旧 id 查不到不等于用户删了；
  先用一次区间查询的 `[xhanhelper:<id>]` 结果修正映射，认领不到才记墓碑（否则会「永久退出日历 + 又排通知」＝双响）
- **通知字典去重**：`Dictionary(..., uniquingKeysWith:)`——offset 分页 + `created_at DESC` 时两页边界项可能重复，原写法会 trap
- **确认条补内容**：`update_event` 展示「标题/开始/结束/日历/备注 → 新值」；`delete_event` 在确认前用 EventKit 查一次事件标题与时间（读不到则退回 id）
- **多日全天保留跨度**：`normalizeAllDay` 只把首尾落到日界，不再压成一天
- **开关关着时手删事件**：也会走 `detectDropped`（已授权时），该提醒回落系统通知，不再静默丢提醒
- **测试时区无关**：`testConfirmationSummary` 断言改用 `CalendarCore.displayDate`，`TZ=UTC` 下同样通过



### 2026-11-14 · Debug 构建默认关掉镜像

dev/prod 曾共用同一份 app 身份（同 bundle id `xhanx.cn.xHelperMac`），两个 build 的提醒都会写进**同一个真实日历**，
同一个待办可能在日历里出现两条。现在 Debug 构建改用独立 bundle id `xhanx.cn.xHelperMac.dev`
（见 `docs/bugs/068`），并让开关在 Debug 下默认关：

- `ReminderCalendarMirror.defaultEnabled = !LocalPaths.isDev`，`isEnabled` 与设置页 `@AppStorage` 共用这一处真值。
- 关着时走既有分支：不新建、不删已成事件、不为维护映射去要授权，提醒回落系统通知。
- 影响文件：`Services/ReminderCalendarMirror.swift`、`xHelperMac/App.swift`。
