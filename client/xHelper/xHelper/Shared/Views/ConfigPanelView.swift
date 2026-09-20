import SwiftUI
#if os(macOS)
import AppKit

/// 浏览器配置面板（浏览器内特殊 tab）：展示内置默认值 + 管理用户自定义条目。
/// 四类配置：高敏域名 / 高敏路径 / 高危扩展名 / 内容屏蔽。改动即时生效。
struct ConfigPanelView: View {
    @ObservedObject private var store = UserConfigStore.shared
    @State private var newHost = ""
    @State private var newPath = ""
    @State private var newExtension = ""
    @State private var newBlockerDomain = ""
    @State private var toast: String?

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            ScrollView {
                VStack(spacing: 12) {
                    section(
                        title: "高敏域名",
                        icon: "lock.shield",
                        builtIn: UserConfigStore.builtInSensitiveHosts,
                        custom: store.config.sensitiveHosts,
                        list: .hosts,
                        input: $newHost,
                        placeholder: "如 steam、taobao、amazon",
                        help: "作用：agent 在此类页面上执行交互（点击/填写/输入/按键）时，自动弹出 L3 确认，信任模式也无法跳过。保护银行/支付/邮箱/登录等敏感页面不被 agent 自动操作。\n格式：域名关键词，子串匹配、不区分大小写。如 bank 会匹配 bank.com、mybank.cn、alipaybank.com。\n注意：子串匹配可能误伤，比如 mail 会匹配 dailymail.com，建议尽量具体。"
                    )
                    section(
                        title: "高敏路径",
                        icon: "lock",
                        builtIn: UserConfigStore.builtInSensitivePaths,
                        custom: store.config.sensitivePaths,
                        list: .paths,
                        input: $newPath,
                        placeholder: "如 /settings、/checkout、/wallet",
                        help: "作用：和高敏域名类似，但匹配 URL 路径。agent 在包含这些路径的页面交互时自动弹 L3 确认。\n格式：路径关键词，子串匹配、不区分大小写。建议带 / 前缀避免误伤（/admin 只匹配路径，不会匹配域名里的 admin）。"
                    )
                    section(
                        title: "高危扩展名",
                        icon: "exclamationmark.triangle",
                        builtIn: UserConfigStore.builtInDangerousExtensions,
                        custom: store.config.dangerousExtensions,
                        list: .extensions,
                        input: $newExtension,
                        placeholder: "如 scr、vbs、ps1",
                        help: "作用：agent 调用下载工具时，文件扩展名在此列表中的一律拒绝下载。防 agent 被诱导下载可执行/脚本/压缩等危险文件。\n格式：纯扩展名，不带点。如 scr（不是 .scr）。"
                    )
                    section(
                        title: "内容屏蔽",
                        icon: "shield",
                        builtIn: UserConfigStore.builtInContentBlockerDomains,
                        custom: store.config.contentBlockerDomains,
                        list: .blocker,
                        input: $newBlockerDomain,
                        placeholder: "如 clarity.ms、hotjar.com",
                        help: "作用：屏蔽网页中的第三方追踪/广告脚本和资源（script/image/XHR/media），减少隐私泄露、注入面、CPU 与带宽消耗。\n格式：域名，如 clarity.ms。自动生成拦截规则。\n注意：可能影响部分网站功能（第三方登录、验证码等），如遇问题回来删除该域名即可。",
                        onChange: { ContentBlocker.reload() }
                    )
                    if let toast {
                        Text(toast)
                            .font(.system(size: 11))
                            .foregroundColor(.secondary)
                    }
                }
                .padding(12)
            }
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: 8) {
            Image(systemName: "shield")
                .foregroundColor(.secondary)
            Text("浏览器配置")
                .font(.system(size: 13, weight: .semibold))
            Text("内置默认值不可改，可在此追加自定义条目")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 10)
    }

    // MARK: - Section

    private func section(
        title: String,
        icon: String,
        builtIn: [String],
        custom: [String],
        list: UserConfigStore.MergedList,
        input: Binding<String>,
        placeholder: String,
        help: String,
        onChange: (() -> Void)? = nil
    ) -> some View {
        VStack(alignment: .leading, spacing: 8) {
            Label(title, systemImage: icon)
                .font(.system(size: 12, weight: .semibold))
            Text(help)
                .font(.system(size: 11))
                .foregroundColor(.secondary)
                .fixedSize(horizontal: false, vertical: true)

            // 内置默认（灰底，不可删）
            if !builtIn.isEmpty {
                Text("内置（不可修改）")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                FlowWrap(items: builtIn) { value in
                    tag(value, deletable: false)
                }
            }

            // 用户自定义（可删）
            Text("自定义")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
            if custom.isEmpty {
                Text("暂无自定义条目")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .opacity(0.5)
            } else {
                FlowWrap(items: custom) { value in
                    tag(value, deletable: true) {
                        store.remove(value, from: list)
                        onChange?()
                    }
                }
            }

            // 添加输入
            HStack(spacing: 6) {
                TextField(placeholder, text: input)
                    .textFieldStyle(.roundedBorder)
                    .font(.system(size: 11))
                Button {
                    let value = input.wrappedValue
                    if store.add(value, to: list) {
                        input.wrappedValue = ""
                        onChange?()
                        showToast("已添加")
                    } else {
                        showToast("无效或已存在")
                    }
                } label: {
                    Image(systemName: "plus")
                        .font(.system(size: 11, weight: .semibold))
                        .frame(width: 26, height: 22)
                }
                .buttonStyle(.plain)
                .foregroundColor(Color("appAccent"))
                .disabled(input.wrappedValue.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty)
            }
        }
        .padding(10)
        .modifier(CardBackgroundModifier(isLifted: false))
    }

    private func tag(_ value: String, deletable: Bool, onDelete: (() -> Void)? = nil) -> some View {
        HStack(spacing: 4) {
            Text(value)
                .font(.system(size: 11))
                .foregroundColor(deletable ? Color("appAccent") : .secondary)
                .lineLimit(1)
                .fixedSize(horizontal: true, vertical: false)
            if deletable {
                Button {
                    onDelete?()
                } label: {
                    Image(systemName: "xmark.circle.fill")
                        .font(.system(size: 10))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                .help("删除")
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(
            Capsule()
                .fill(Color("appAccent").opacity(0.12))
        )
    }

    private func showToast(_ msg: String) {
        withAnimation {
            toast = msg
        }
        Task {
            try? await Task.sleep(for: .seconds(1.5))
            withAnimation {
                if toast == msg { toast = nil }
            }
        }
    }
}

/// 自动换行流式布局：每个标签按内容自然宽度排列，放不下自动折行，完整展示不截断。
private struct FlowLayout: SwiftUI.Layout {
    var hSpacing: CGFloat = 6
    var vSpacing: CGFloat = 6

    func sizeThatFits(proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) -> CGSize {
        let maxWidth = proposal.width ?? .infinity
        var width: CGFloat = 0
        var x: CGFloat = 0
        var y: CGFloat = 0
        var rowH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(ProposedViewSize.unspecified)
            if x + s.width > maxWidth, x > 0 {
                x = 0
                y += rowH + vSpacing
                rowH = 0
            }
            x += s.width + hSpacing
            rowH = max(rowH, s.height)
            width = max(width, x - hSpacing)
        }
        return CGSize(width: width, height: y + rowH)
    }

    func placeSubviews(in bounds: CGRect, proposal: ProposedViewSize, subviews: Subviews, cache: inout ()) {
        var x = bounds.minX
        var y = bounds.minY
        var rowH: CGFloat = 0
        for v in subviews {
            let s = v.sizeThatFits(ProposedViewSize.unspecified)
            if x + s.width > bounds.maxX, x > bounds.minX {
                x = bounds.minX
                y += rowH + vSpacing
                rowH = 0
            }
            v.place(at: CGPoint(x: x, y: y), proposal: ProposedViewSize.unspecified)
            x += s.width + hSpacing
            rowH = max(rowH, s.height)
        }
    }
}

/// 通用流式容器（供 FlowLayout 使用，标签项从 content 闭包构建）。
private struct FlowWrap<Data: RandomAccessCollection, Content: View>: View where Data.Element == String {
    let items: Data
    let content: (String) -> Content

    var body: some View {
        FlowLayout(hSpacing: 6, vSpacing: 6) {
            ForEach(Array(items.enumerated()), id: \.offset) { _, item in
                content(item)
            }
        }
    }
}

#endif
