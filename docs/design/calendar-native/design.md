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
| `update_event` | `event_id` + 可选字段 | 重复事件用 `.thisEvent` |
| `delete_event` | `event_id` | |

- 读取结果形如 `2026-10-30 14:00-15:00 周会 @工作`，单行一条，超预算截断并提示缩小范围
- 风险等级：读 = 默认 L2（免确认）；`create/update/delete` = **L4（红色必弹）**，登记在 `ToolRiskLevels.byTool`
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
| pending 且能写日历 | 建事件（标题=content 截 60 字，备注=`notification_body`，`deadline → +15min`，`EKAlarm(relativeOffset: 0)`），**不排本地通知** |
| pending 但写不了（无授权 / 无可同步日历 / save 报错） | 走原来的 `schedule(reminder:)`，行为不变 |
| 映射里有、这次 pending 里没了（ack 或 cancel） | 删事件 + 清映射 |

- 事件在提醒**仍是 pending** 期间一直保留（含已过点但还没 ack 的），过期后日历里仍看得到今天开过什么会
- 用户手动在日历里删掉事件 → 下次同步会补建（想彻底不要就取消提醒）
- 关掉开关：只停止新建，已建事件与映射都不回收（再打开会继续用旧映射）

- 映射 `reminder.id → eventIdentifier` 存 `UserDefaults`（key 走 `LocalPaths.scoped`，dev/prod 不串味）
- 权限/日历不可用做**会话级负缓存**：denied 之后不再请求、不再逐条试，只打一条日志
- **截断保护**：`total != pending.count`（超过一页 50 条）时跳过「删除」这一轮，防误删日历事件
- 开关：`@AppStorage` key `reminder_sync_to_calendar`，默认开；设置页给状态展示 + 「请求权限」按钮

### 取舍：日历闹钟替代本地通知（方案 A，用户已拍板）

日历事件带 `EKAlarm(relativeOffset: 0)`，Mac 上不再排本地通知 —— 两边都排会在同一分钟弹两次。
代价：点击闹钟打开的是日历 App，**失去「点通知 → 助手在聊天里把提醒提起来」**（`handleReminderTap`）。
该流程保留，仅服务于回退路径（写不进日历的提醒）。用户明确同意。

## 权限

- Info.plist：`NSCalendarsFullAccessUsageDescription`（`project.pbxproj` 的 `INFOPLIST_KEY_*`，macOS Debug + Release 各一条）
- 读写共用一次「完整访问」授权；app 未开沙盒（`ENABLE_APP_SANDBOX = NO`），不需要 calendars entitlement
- 懒请求：第一次用到时弹系统框；设置页只做状态展示与兜底请求

## 验证清单（真机）

1. 首次建提醒 → 系统弹日历授权 → 允许后日历里出现事件、本地无通知
2. iPhone 日历 App 打开即看到同一条事件（iCloud 同步）
3. 拒绝授权 → 提醒照旧走本地通知，日志一条、不反复请求
4. 只有本地日历（无 iCloud）→ 不写事件，回退通知，工具结果/日志给出人话
5. 取消提醒 → 日历事件消失
6. `calendar__list_events` 读当天 → 与日历 App 一致
7. `calendar__create_event` → 确认条显示标题/时间/账号，批准后 iPhone 可见
8. `swift test`（CalendarCoreTests）绿；`swiftc -typecheck` macOS/iOS 两批绿

## 后续变更

（暂无）
