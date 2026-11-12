import SwiftUI

/// 备忘面板：只读的待办提醒卡片列表。
///
/// 数据与首页「备忘」卡同源（`SidebarHomeViewModel.reminders`，一次 `limit=50` 的 pending 拉取），
/// 这里只做展示：内容作卡标题、到期文案作页脚，整卡不可点、无操作按钮（提醒的处理仍走通知）。
/// 纯展示视图：数据与重试闭包都由调用方注入，不依赖任何 VM。
struct ReminderPageView: View {
    let items: [Reminder]
    let isLoading: Bool
    let errorMessage: String?
    let onRetry: () -> Void

    var body: some View {
        PageContainer(
            title: "备忘",
            icon: "checklist",
            isLoading: isLoading,
            errorMessage: errorMessage,
            isEmpty: items.isEmpty && !isLoading,
            emptyMessage: "没有待办",
            onRetry: onRetry
        ) {
            // 3 列 / 最小 260 与记忆面板同口径；提醒本身量小，一次取完不分页。
            CardGridView(items: items, maxColumns: 3, minCardWidth: 260) { item in
                let overdue = SidebarHomeFormat.isOverdue(item.deadline)
                return CardConfig(
                    title: item.content,
                    tags: overdue ? [TagBadge(text: "已过期", color: .orange)] : [],
                    footerText: SidebarHomeFormat.dueText(item.deadline),
                    style: .reminder
                )
            }
        }
    }
}
