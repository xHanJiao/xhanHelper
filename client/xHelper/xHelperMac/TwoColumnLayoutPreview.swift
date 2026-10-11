import SwiftUI

#if os(macOS) && DEBUG

/// 双栏布局改造的 SwiftUI 设计预览（放 Xcode 画布看，⌘⌥P）。
/// 只用真 token + 生产卡片材质（GlassBackground / CardBackgroundModifier），
/// 覆盖 6 个形态：主形态 / 下拉展开 / 无痕生效 / 右侧最大化（PiP）/ 卡片吸顶（+ 仅中栏）。
/// 后两个是 `docs/design/mac-card-top-wrap` 的形态：中栏卡片上边停在窗口内、向上溢出包住顶栏，右栏裸态。
/// 仅展示用，不参与真实布局；真动手时会被实际视图取代。
struct TwoColumnLayoutPreview: View {
    enum Variant: String, CaseIterable, Identifiable {
        case main = "主形态"
        case dropdown = "下拉展开"
        case ephemeral = "无痕生效"
        case pip = "右侧最大化"
        case wrap = "卡片吸顶"
        case wrapChatOnly = "卡片吸顶·仅中栏"
        var id: String { rawValue }
    }

    var variant: Variant = .main

    /// 卡片吸顶形态：中栏卡片上边停在窗口内 `cardGap` 处、向上溢出包住红绿灯 +（关右栏时）全部顶栏按钮
    /// （生产同款写法 + 生产几何，见 docs/design/mac-card-top-wrap/design.md）。
    private var isWrap: Bool {
        variant == .wrap || variant == .wrapChatOnly
    }
    /// 仅中栏（右栏收起）——这时卡片铺满整宽，顶栏按钮全被包住。
    private var isChatOnly: Bool { variant == .wrapChatOnly }

    private let cardGap: CGFloat = 6
    private let currentTitle = "关于特斯拉的笔记"

    // 真 token
    private var accent: Color { Color("appAccent") }
    private var ink: Color { Color("assistantText") }
    private func ink(_ o: Double) -> Color { Color("assistantText").opacity(o) }
    private var userTint: Color { Color("userText") }
    private var surfaceInput: Color { Color("surfaceInput") }
    private var bubbleTool: Color { Color("bubbleTool") }

    var body: some View {
        ZStack(alignment: .topLeading) {
            GlassBackground()
            VStack(spacing: 0) {
                topBar
                content
            }
            if variant == .dropdown {
                conversationMenu.offset(x: 16, y: 52)
            }
            if isWrap { fakeTrafficLights }
        }
        .frame(width: 1340, height: 900)
    }

    /// 预览专用：系统红绿灯（AppKit 画的窗口按钮，不是我们的 UI）——取系统色、对齐生产几何
    /// （宽 14 / 距窗顶 23 / x=15·38·61，含 +6/−14 微移：与顶栏内容中心同一行居中）。
    private var fakeTrafficLights: some View {
        HStack(spacing: 9) {
            trafficLight(r: 1.00, g: 0.37, b: 0.34)   // 红
            trafficLight(r: 1.00, g: 0.74, b: 0.18)   // 黄
            trafficLight(r: 0.16, g: 0.79, b: 0.25)   // 绿
        }
        .offset(x: 15, y: 23)
    }

    /// 参数是 RGB 分量（不是"哪个灯"）。
    private func trafficLight(r: Double, g: Double, b: Double) -> some View {
        Circle()
            .fill(Color(red: r, green: g, blue: b))
            .frame(width: 14, height: 14)
    }

    // MARK: 顶栏

    private var topBar: some View {
        HStack(spacing: 10) {
            // 红绿灯让位（生产 `topBarTrafficClearance`；只有上爬形态才看得见红绿灯）
            if isWrap { Spacer().frame(width: Layout.topBarTrafficClearance) }
            if variant != .pip {
                HStack(spacing: 6) {
                    conversationTrigger
                    newConversationButton
                }
            }
            Spacer()
            Text("DEV").font(.system(size: 10, weight: .bold)).foregroundStyle(ink(0.35))
            Spacer()
            capsuleGroup(["globe", "doc.text", "creditcard", "chart.bar", "brain"])
            capsuleGroup(["arrow.up.left.and.arrow.down.right", "sidebar.trailing", "paintpalette", "gearshape"])
        }
        // 顶栏实高 = 生产 `Layout.topBarHeight`（上 12 / 下 6 + 胶囊 36 = 54；卡片上溢量由它派生）
        .frame(height: Layout.topBarCapsuleHeight)
        .padding(.horizontal, Layout.toolbarHorizontalPadding)
        .padding(.top, Layout.topBarTopGap)
        .padding(.bottom, Layout.toolbarVGap)
    }

    private func capsuleGroup(_ icons: [String]) -> some View {
        HStack(spacing: 10) {
            ForEach(icons, id: \.self) {
                Image(systemName: $0).font(.system(size: 12)).foregroundStyle(ink(0.7))
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 5)
        .background(Capsule().fill(.regularMaterial))
        .overlay(Capsule().strokeBorder(ink(0.08), lineWidth: 0.5))
    }

    private var conversationTrigger: some View {
        HStack(spacing: 4) {
            Image(systemName: "theatermasks.fill")
                .font(.system(size: 12))
                .foregroundStyle(variant == .ephemeral ? accent : ink(0.6))
            Text(currentTitle)
                .font(.system(size: 14, weight: .medium))
                .foregroundStyle(ink(0.85))
                .lineLimit(1)
            Image(systemName: "chevron.down")
                .font(.system(size: 10, weight: .bold))
                .foregroundStyle(ink(0.4))
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 6)
        .background(Capsule().fill(.regularMaterial))
        .overlay(Capsule().strokeBorder(ink(0.1), lineWidth: 0.5))
        .fixedSize(horizontal: true, vertical: false)
    }

    private var newConversationButton: some View {
        Image(systemName: "plus")
            .font(.system(size: 16, weight: .medium))
            .foregroundStyle(ink(0.7))
            .frame(width: 34, height: 34)
            .background(Circle().fill(.regularMaterial))
            .overlay(Circle().strokeBorder(ink(0.1), lineWidth: 0.5))
            .help("新会话")
    }

    // MARK: 会话下拉

    private var conversationMenu: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text("会话")
                .font(.system(size: 11, weight: .semibold))
                .foregroundStyle(ink(0.45))
                .padding(.horizontal, 16).padding(.top, 10).padding(.bottom, 6)

            menuRow(icon: "theatermasks", title: currentTitle, current: true, pinned: false)
            menuRow(icon: nil, title: "项目排期讨论", current: false, pinned: true)
            menuRow(icon: nil, title: "周报整理", current: false, pinned: false)
            menuRow(icon: nil, title: "产品想法收集", current: false, pinned: false)

            Text("加载更多")
                .font(.system(size: 11)).foregroundStyle(ink(0.4))
                .frame(maxWidth: .infinity).padding(.vertical, 8)
        }
        .padding(.bottom, 6)
        .frame(width: 280)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(RoundedRectangle(cornerRadius: 12).strokeBorder(ink(0.1), lineWidth: 0.5))
        .shadow(color: .black.opacity(0.18), radius: 18, y: 8)
    }

    private func menuRow(icon: String?, title: String, current: Bool, pinned: Bool) -> some View {
        HStack(spacing: 6) {
            if let icon { Image(systemName: icon).font(.system(size: 12)).foregroundStyle(accent) }
            Text(title)
                .font(.system(size: 14, weight: current ? .semibold : .regular))
                .foregroundStyle(ink(current ? 0.9 : 0.72))
                .lineLimit(1)
            Spacer(minLength: 4)
            if current { Image(systemName: "checkmark").font(.system(size: 12, weight: .bold)).foregroundStyle(accent) }
            Image(systemName: pinned ? "pin.fill" : "pin").font(.system(size: 12)).foregroundStyle(ink(pinned ? 0.5 : 0.25))
            Image(systemName: "xmark").font(.system(size: 12)).foregroundStyle(ink(0.25))
        }
        .padding(.horizontal, 10).padding(.vertical, 7)
        .background(current ? accent.opacity(0.08) : Color.clear, in: RoundedRectangle(cornerRadius: 6))
        .padding(.horizontal, 6)
    }

    // MARK: 主体两栏

    private var content: some View {
        Group {
            if variant == .pip {
                ZStack(alignment: .bottomTrailing) {
                    rightCard.frame(maxWidth: .infinity, maxHeight: .infinity)
                    chatCard.frame(width: 470, height: 380).padding(14)
                }
            } else {
                HStack(spacing: cardGap) {
                    chatCard
                    if !isChatOnly { rightCard.frame(width: 330) }
                }
            }
        }
        .padding(.horizontal, cardGap)
        .padding(.bottom, cardGap)
    }

    // MARK: 中栏

    @ViewBuilder
    private var chatCard: some View {
        if isWrap {
            // 生产同款：上边停在窗口内 `cardGap` 处（向上溢出 `cardTopRise`）、白霜提亮底 + 描边 + 只在卡片外的投影
            chatBody
                .background(alignment: .top) {
                    RoundedRectangle(cornerRadius: 12)
                        .fill(Color.white.opacity(Layout.cardFillOpacity))
                        .padding(.top, -Layout.cardTopRise)
                }
                .modifier(CardElevationShadow(topExtension: Layout.cardTopRise))
                .overlay(
                    RoundedRectangle(cornerRadius: 12)
                        .strokeBorder(ink(0.12), lineWidth: 0.5)
                        .padding(.top, -Layout.cardTopRise))
        } else {
            chatBody.cardBackgroundForPreview()
        }
    }

    private var chatBody: some View {
        VStack(spacing: 0) {
            messages
            Spacer(minLength: 0)
            inputBar.padding(.horizontal, 16).padding(.bottom, 14)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity, alignment: .top)
    }

    private var messages: some View {
        VStack(alignment: .leading, spacing: 12) {
            HStack { Spacer(); bubble("帮我把上周特斯拉相关的笔记整理成一份文档", isUser: true) }
            bubble("好的，我先检索了你最近的笔记，找到 3 条和特斯拉能耗相关的内容：\n1. 能耗与胎压的关系\n2. 冬季续航衰减记录\n3. 超充费用统计", isUser: false)
            toolBubble("已调用 note · 语义检索")
            HStack { Spacer(); bubble("把第 2 条的衰减数据画成图表", isUser: true) }
            bubble("已为你生成折线图，可在右侧「文档」区查看。", isUser: false)
        }
        .padding(.horizontal, 20)
        .padding(.top, 22)
    }

    private func bubble(_ text: String, isUser: Bool) -> some View {
        Text(text)
            .font(.system(size: 13))
            .foregroundStyle(isUser ? .white : ink)
            .padding(.horizontal, 13).padding(.vertical, 9)
            .background(
                RoundedRectangle(cornerRadius: 12)
                    .fill(isUser ? userTint : Color(nsColor: .textBackgroundColor).opacity(0.9))
            )
            .frame(maxWidth: 430, alignment: .leading)
    }

    private func toolBubble(_ text: String) -> some View {
        Text(text)
            .font(.system(size: 11)).foregroundStyle(ink(0.55))
            .padding(.horizontal, 10).padding(.vertical, 5)
            .background(Capsule().fill(bubbleTool.opacity(0.7)))
    }

    private var inputBar: some View {
        VStack(alignment: .leading, spacing: 7) {
            if variant == .ephemeral {
                HStack(spacing: 5) {
                    Image(systemName: "exclamationmark.triangle.fill").font(.system(size: 10))
                    Text("无痕：最后一轮对话约 30 分钟后自动销毁，可撤销").font(.system(size: 11))
                }
                .foregroundStyle(.orange)
            }
            Text("输入消息，或按 Tab 切换计划模式…")
                .font(.system(size: 13)).foregroundStyle(ink(0.4))
                .frame(maxWidth: .infinity, alignment: .leading)
            HStack(spacing: 8) {
                pill("无痕", "theatermasks.fill", active: variant == .ephemeral, dot: variant == .ephemeral)
                pill("模式", "slider.horizontal.3")
                pill("模型", "bolt.fill")
                pill("Brief", "text.alignleft")
                Spacer()
                ZStack {
                    Circle().fill(accent).frame(width: 28, height: 28)
                    Image(systemName: "arrow.up").font(.system(size: 12, weight: .bold)).foregroundStyle(.white)
                }
            }
        }
        .padding(12)
        .background(RoundedRectangle(cornerRadius: 14).fill(.regularMaterial))
        .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(ink(0.08), lineWidth: 0.5))
    }

    private func pill(_ text: String, _ icon: String, active: Bool = false, dot: Bool = false) -> some View {
        HStack(spacing: 4) {
            Image(systemName: icon).font(.system(size: 10, weight: .semibold))
            Text(text).font(.system(size: 11, weight: .medium))
            if dot { Circle().fill(.white).frame(width: 5, height: 5) }
        }
        .foregroundStyle(active ? .white : ink(0.7))
        .padding(.horizontal, 9).padding(.vertical, 4)
        .background(Capsule().fill(active ? accent : ink(0.06)))
        .overlay(Capsule().strokeBorder(active ? .clear : ink(0.1), lineWidth: 0.5))
    }

    // MARK: 右栏

    @ViewBuilder
    private var rightCard: some View {
        if isWrap {
            // 生产同款：右栏不画边缘、直接坐底图
            rightBody
        } else {
            rightBody.cardBackgroundForPreview()
        }
    }

    private var rightBody: some View {
        VStack(alignment: .leading, spacing: 14) {
            HStack {
                Text("功能区").font(.system(size: 13, weight: .semibold)).foregroundStyle(ink(0.8))
                Spacer()
                Image(systemName: "xmark").font(.system(size: 11)).foregroundStyle(ink(0.3))
            }
            LazyVGrid(columns: [GridItem(.flexible()), GridItem(.flexible())], spacing: 10) {
                Tile(title: "账单", icon: "creditcard")
                Tile(title: "记忆", icon: "brain")
                Tile(title: "笔记", icon: "note.text")
                Tile(title: "文档", icon: "doc.text")
                Tile(title: "图片", icon: "photo")
                Tile(title: "主题", icon: "circle.hexagongrid")
                Tile(title: "看板", icon: "chart.bar")
                Tile(title: "智能体", icon: "person.crop.circle")
            }
            Divider()
            Text("近日文档").font(.system(size: 12, weight: .medium)).foregroundStyle(ink(0.6))
            ForEach(["特斯拉能耗周报", "2026 年度预算表", "读书笔记 · 反脆弱"], id: \.self) { name in
                HStack(spacing: 8) {
                    Image(systemName: "doc.text").font(.system(size: 12)).foregroundStyle(accent)
                    Text(name).font(.system(size: 12)).foregroundStyle(ink(0.7))
                    Spacer()
                }
                .padding(.vertical, 3)
            }
            Spacer()
        }
        .padding(16)
        .frame(maxHeight: .infinity, alignment: .top)
    }

    private struct Tile: View {
        let title: String
        let icon: String
        var body: some View {
            VStack(spacing: 6) {
                Image(systemName: icon).font(.system(size: 16)).foregroundStyle(Color("appAccent"))
                Text(title).font(.system(size: 11)).foregroundStyle(Color("assistantText").opacity(0.75))
            }
            .frame(maxWidth: .infinity)
            .padding(.vertical, 14)
            .background(RoundedRectangle(cornerRadius: 10).fill(.regularMaterial))
            .overlay(RoundedRectangle(cornerRadius: 10).strokeBorder(Color("assistantText").opacity(0.06), lineWidth: 0.5))
        }
    }
}

private extension View {
    func cardBackgroundForPreview() -> some View {
        modifier(CardBackgroundModifier(isLifted: false))
    }
}

#Preview("主形态") { TwoColumnLayoutPreview(variant: .main) }
#Preview("下拉展开") { TwoColumnLayoutPreview(variant: .dropdown) }
#Preview("无痕生效") { TwoColumnLayoutPreview(variant: .ephemeral) }
#Preview("右侧最大化 PiP") { TwoColumnLayoutPreview(variant: .pip) }
#Preview("卡片吸顶（右栏开）") { TwoColumnLayoutPreview(variant: .wrap) }
#Preview("卡片吸顶·仅中栏") { TwoColumnLayoutPreview(variant: .wrapChatOnly) }

#endif
