import SwiftUI

struct ShortcutItem {
    let keys: [String]
    let desc: String
}

struct ShortcutSection {
    let icon: String
    let title: String
    let hint: String
    let items: [ShortcutItem]
}

// ponytail: 手工维护的静态表，改快捷键时必须同步这里（AppDelegate 的 NSEvent 监听 / TopBarView / 各视图 .onKeyPress）；无法自动推导
struct ShortcutGuideView: View {
    private static let sections: [ShortcutSection] = [
        ShortcutSection(icon: "command", title: "通用", hint: "全局生效", items: [
            ShortcutItem(keys: ["⌘", "Q"], desc: "退出应用（长按约 0.5 秒）"),
            ShortcutItem(keys: ["⌘", ","], desc: "打开设置"),
            ShortcutItem(keys: ["⌘", "N"], desc: "新建对话"),
            ShortcutItem(keys: ["⌘", "⇧", "B"], desc: "切换右侧栏"),
            ShortcutItem(keys: ["⌘", "⇧", "↑"], desc: "上一个会话"),
            ShortcutItem(keys: ["⌘", "⇧", "↓"], desc: "下一个会话"),
            ShortcutItem(keys: ["⌘", "⇧", "M"], desc: "最大化 / 还原侧栏内容"),
            ShortcutItem(keys: ["⌘", "⇧", "E"], desc: "展开 / 收起 PiP 悬浮球"),
            ShortcutItem(keys: ["⌘", "="], desc: "增大字体（浏览器未聚焦时）"),
            ShortcutItem(keys: ["⌘", "-"], desc: "减小字体（浏览器未聚焦时）"),
            ShortcutItem(keys: ["⌘", "⌘"], desc: "按住左右两个 ⌘ 说话，松手发送（全局，关窗也能用）"),
            ShortcutItem(keys: ["⌥", "⌘", "⌘"], desc: "按住 ⌥ + 左右两个 ⌘ 说话，另开一个新会话（全局）"),
            ShortcutItem(keys: ["⌃", "⌥", "V"], desc: "同上（备用键）"),
        ]),
        ShortcutSection(icon: "menubar.rectangle", title: "面板导航", hint: "顶部工具栏", items: [
            ShortcutItem(keys: ["⌘", "1"], desc: "文档"),
            ShortcutItem(keys: ["⌘", "2"], desc: "灵阅"),
            ShortcutItem(keys: ["⌘", "3"], desc: "监控面板"),
            ShortcutItem(keys: ["⌘", "4"], desc: "浏览器"),
            ShortcutItem(keys: ["⌘", "5"], desc: "记忆"),
        ]),
        ShortcutSection(icon: "globe", title: "浏览器", hint: "浏览器聚焦时", items: [
            ShortcutItem(keys: ["⌘", "T"], desc: "新建标签页"),
            ShortcutItem(keys: ["⌘", "⇧", "T"], desc: "恢复最近关闭的标签页"),
            ShortcutItem(keys: ["⌘", "⇧", "["], desc: "上一个标签页"),
            ShortcutItem(keys: ["⌘", "⇧", "]"], desc: "下一个标签页"),
            ShortcutItem(keys: ["⌘", "W"], desc: "关闭当前标签页"),
            ShortcutItem(keys: ["⌘", "["], desc: "后退"),
            ShortcutItem(keys: ["⌘", "]"], desc: "前进"),
            ShortcutItem(keys: ["⌘", "R"], desc: "刷新页面"),
            ShortcutItem(keys: ["⌘", "="], desc: "页面放大"),
            ShortcutItem(keys: ["⌘", "-"], desc: "页面缩小"),
            ShortcutItem(keys: ["⌘", "F"], desc: "页内查找"),
            ShortcutItem(keys: ["⌘", "G"], desc: "查找下一个"),
            ShortcutItem(keys: ["⌘", "⇧", "G"], desc: "查找上一个"),
            ShortcutItem(keys: ["⌘", "D"], desc: "收藏 / 取消收藏当前页"),
            ShortcutItem(keys: ["Esc"], desc: "关闭查找栏"),
        ]),
        ShortcutSection(icon: "text.cursor", title: "地址栏", hint: "地址栏聚焦时", items: [
            ShortcutItem(keys: ["↑", "/", "↓"], desc: "选择自动补全项"),
            ShortcutItem(keys: ["⏎", "/", "Tab"], desc: "采纳选中建议"),
            ShortcutItem(keys: ["Esc"], desc: "关闭建议列表"),
        ]),
        ShortcutSection(icon: "text.bubble", title: "聊天输入", hint: "输入框聚焦时", items: [
            ShortcutItem(keys: ["⏎"], desc: "发送消息"),
            ShortcutItem(keys: ["⇧", "⏎"], desc: "换行"),
            ShortcutItem(keys: ["Tab"], desc: "切换计划模式（关/计划）"),
            ShortcutItem(keys: ["⌃", "C"], desc: "清空输入（文字与图片）"),
        ]),
        ShortcutSection(icon: "checkmark.shield", title: "工具确认", hint: "AI 请求工具授权时", items: [
            ShortcutItem(keys: ["Tab"], desc: "允许"),
            ShortcutItem(keys: ["Esc"], desc: "拒绝"),
        ]),
    ]

    var body: some View {
        ScrollView {
            VStack(spacing: 16) {
                ForEach(Self.sections, id: \.title) { section in
                    SettingsCard(icon: section.icon, title: section.title, hint: section.hint) {
                        ForEach(Array(section.items.enumerated()), id: \.offset) { index, item in
                            if index > 0 {
                                Divider().opacity(0.6).padding(.leading, 14)
                            }
                            shortcutRow(item)
                        }
                    }
                }
            }
            .padding(16)
        }
        .navigationTitle("快捷键")
    }

    private func shortcutRow(_ item: ShortcutItem) -> some View {
        HStack(spacing: 12) {
            Text(item.desc)
                .font(.system(size: 13))
            Spacer(minLength: 24)
            HStack(spacing: 4) {
                ForEach(Array(item.keys.enumerated()), id: \.offset) { _, key in
                    if key == "/" {
                        Text("/")
                            .font(.system(size: 12))
                            .foregroundStyle(.tertiary)
                    } else {
                        KeycapView(label: key)
                    }
                }
            }
            .frame(minWidth: 110, alignment: .trailing)
        }
        .padding(.horizontal, 14)
        .padding(.vertical, 8)
    }
}
