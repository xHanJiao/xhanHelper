import Foundation

/// 附表 A 登记的全部事件 type——客户端 `record` 出口的白名单。
///
/// 只**告警不拦截**：客户端事件本地生成、丢了没法补，白名单过期时宁可让未知 type 落盘
/// （服务端至少看得到），也不能把真事件吞掉。因此这是个提醒器，不是闸门。
nonisolated enum ActivityEventTypes {
    static let known: Set<String> = [
        // 对话与 LLM（服务端）
        "chat.message.sent", "chat.turn.started", "chat.turn.ended",
        "llm.call.started", "llm.call.ended", "llm.first_token",
        // 工具与执行
        "tool.call.started", "tool.call.ended", "tool.bridge.dispatched", "tool.bridge.returned",
        "client.tool.exec.started", "client.tool.exec.ended", "task.spawned", "task.ended",
        // 浏览（客户端）
        "browser.nav.started", "browser.nav.ended", "browser.page.dwell", "browser.bookmark.added",
        // 用户显式动作（客户端 UI 层）
        "user.action.confirmed", "user.action.dismissed", "user.action.deleted",
        "user.action.saved", "user.action.retried",
        // 系统变更（服务端仓储层）
        "doc.created", "doc.updated", "doc.deleted",
        "memory.entity.changed", "memory.topic.cleaned",
        "bill.created", "reminder.created", "reminder.acknowledged",
        // 在场与电源（客户端）
        "client.app.launched", "client.app.quit",
        "client.focus.gained", "client.focus.lost",
        "client.idle.started", "client.idle.ended",
        "client.input.active", "client.input.ended",
        "system.sleep", "system.wake", "system.screens.sleep", "system.screens.wake",
        "system.power.off", "system.boundary",
        // 组件（客户端）
        "component.opened", "component.closed", "component.focus.gained", "component.focus.lost",
        // 读取自记（服务端）
        "read.query",
    ]
}
