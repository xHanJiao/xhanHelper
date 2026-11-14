// 视觉基线原型（假数据）。真视图已落在 BookshelfView/BookReaderView，这里只留作对照，
// 只在 Debug 构建里参与编译，不进发行包。
#if DEBUG
#if os(macOS)
import SwiftUI

/// EPUB 阅读器 UI 原型（**假数据，不接业务**）。
///
/// 用途：在 Xcode 画布（⌘⌥P）里复核书架 / 阅读页 / 批注面板的结构与留白，
/// 确认后再落 `BookshelfView` / `BookReaderView` / `BookAnnotationPanel` 的真接线。
///
/// 约定：
/// - 只用设计 token（`appAccent` / `assistantText`），不新增色相；封面是占位块，真封面来自导入的 epub。
/// - 右栏固定按默认宽 **840** 预览（`RightSidebarManager.width` 默认值），不按窗口拉伸 —— 宽度账才是真相。
/// - 正文排版真实来源是阅读器模板（WKWebView），这里只给同字号的中文占位，用来判「正文还剩多宽」。

// MARK: - 假数据模型

private struct DemoBook: Identifiable {
    let id = UUID()
    let title: String
    let author: String
    let chapterIndex: Int
    let chapterCount: Int
}

private struct DemoTOCRow: Identifiable {
    let id = UUID()
    let title: String
    let depth: Int
    let isCurrent: Bool
}

private struct DemoAnnotation: Identifiable {
    let id = UUID()
    let quote: String
    let note: String
    let chapterTitle: String
    let timeText: String
}

private enum DemoData {
    static let books: [DemoBook] = [
        .init(title: "贫穷的本质", author: "阿比吉特·班纳吉", chapterIndex: 2, chapterCount: 10),
        .init(title: "思考，快与慢", author: "丹尼尔·卡尼曼", chapterIndex: 7, chapterCount: 38),
        .init(title: "人月神话", author: "弗雷德里克·布鲁克斯", chapterIndex: 0, chapterCount: 19),
        .init(title: "万历十五年", author: "黄仁宇", chapterIndex: 4, chapterCount: 7),
        .init(title: "复杂", author: "梅拉妮·米歇尔", chapterIndex: 11, chapterCount: 24),
        .init(title: "The Pragmatic Programmer", author: "Hunt & Thomas", chapterIndex: 3, chapterCount: 12),
    ]

    static let toc: [DemoTOCRow] = [
        .init(title: "第一章 贫穷的定义", depth: 0, isCurrent: false),
        .init(title: "第二章 饥饿与营养", depth: 0, isCurrent: false),
        .init(title: "2.1 卡路里陷阱", depth: 1, isCurrent: false),
        .init(title: "2.2 收入与消费", depth: 1, isCurrent: true),
        .init(title: "第三章 教育", depth: 0, isCurrent: false),
        .init(title: "第四章 人口", depth: 0, isCurrent: false),
        .init(title: "第五章 风险与保险", depth: 0, isCurrent: false),
        .init(title: "第六章 小额信贷", depth: 0, isCurrent: false),
        .init(title: "第七章 储蓄", depth: 0, isCurrent: false),
        .init(title: "第八章 创业", depth: 0, isCurrent: false),
    ]

    static let annotations: [DemoAnnotation] = [
        .init(quote: "穷人不是不努力，而是他们的努力在结构性约束下很难积累成资产。",
              note: "和《稀缺》第三章的说法可以对照着看。",
              chapterTitle: "第二章 2.2 收入与消费",
              timeText: "昨天"),
        .init(quote: "小额信贷解决的是现金流，不是资本积累。",
              note: "",
              chapterTitle: "第六章 小额信贷",
              timeText: "3 天前"),
    ]

    static let paragraphs: [String] = [
        "我们通常把贫穷理解为收入的函数：只要收入足够低，就可以称为穷人。但真正让贫穷持久的，并不是某一时点的收入水平，而是收入在时间上的不可积累性。",
        "在印度北部的田野调查里，研究者反复看到同一种循环：一场疾病、一次婚丧、一季歉收，就把一个家庭十年的积蓄抹平。储蓄不是不存在，而是无法跨越这些冲击。",
        "这也解释了为什么单纯提高当期收入的政策常常失效。钱进来了，风险还在原地；只要风险还在，家庭就会把新增的收入转化为更耐冲击的资产形式，而不是投入到能带来长期回报的教育或生产工具里。",
    ]
}

// MARK: - 封面占位

/// 真封面来自导入的 epub；预览里用 token 派生一个稳定占位（左边一道书脊 + 书名）。
private struct BookCoverPlaceholder: View {
    let title: String
    var compact = false

    var body: some View {
        ZStack(alignment: .leading) {
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .fill(Color("assistantText").opacity(0.07))
            HStack(spacing: 0) {
                Rectangle()
                    .fill(Color("appAccent").opacity(0.35))
                    .frame(width: compact ? 2 : 3)
                Spacer()
            }
            Text(title)
                .font(.system(size: compact ? 11 : 13, weight: .semibold))
                .foregroundColor(Color("assistantText").opacity(0.85))
                .lineLimit(compact ? 2 : 3)
                .minimumScaleFactor(0.8)
                .multilineTextAlignment(.leading)
                .padding(.horizontal, compact ? 8 : 10)
                .padding(.leading, compact ? 4 : 6)
        }
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
        }
    }
}

// MARK: - 书架卡

private struct BookShelfCard: View {
    let book: DemoBook
    @State private var hovered = false

    private var progress: Double {
        guard book.chapterCount > 0 else { return 0 }
        return Double(book.chapterIndex) / Double(book.chapterCount)
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            BookCoverPlaceholder(title: book.title)
                .aspectRatio(0.75, contentMode: .fit)
                .overlay(alignment: .topTrailing) {
                    Button { } label: {
                        Image(systemName: "ellipsis")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundColor(Color("assistantText"))
                            .frame(width: 22, height: 22)
                            .background(.thinMaterial, in: Circle())
                    }
                    .buttonStyle(.plain)
                    .help("更多")
                    .padding(6)
                    .opacity(hovered ? 1 : 0)
                }

            Text(book.title)
                .font(.system(size: 12.5, weight: .semibold))
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

            Text(book.author)
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
                .lineLimit(1)

            HStack(spacing: 6) {
                GeometryReader { geo in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.primary.opacity(0.10))
                        Capsule().fill(Color("appAccent").opacity(0.75))
                            .frame(width: max(2, geo.size.width * progress))
                    }
                }
                .frame(height: 2)

                Text(book.chapterIndex == 0 ? "未读" : "第 \(book.chapterIndex + 1)/\(book.chapterCount) 章")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .monospacedDigit()
                    .fixedSize()
            }
            .padding(.top, 1)
        }
        .contentShape(Rectangle())
        .onHover { hovered = $0 }
        .animation(.easeInOut(duration: 0.15), value: hovered)
    }
}

private struct BookImportingCard: View {
    let title: String
    let step: String

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            BookCoverPlaceholder(title: title)
                .aspectRatio(0.75, contentMode: .fit)
                .overlay {
                    ProgressView()
                        .controlSize(.small)
                        .padding(8)
                        .background(.thinMaterial, in: Circle())
                }
            Text(title)
                .font(.system(size: 12.5, weight: .semibold))
                .lineLimit(1)
            Text(step)
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
            Spacer(minLength: 0)
        }
    }
}

private struct BookImportFailedCard: View {
    let title: String
    let badge: String
    let reason: String
    var detail = ""

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            BookCoverPlaceholder(title: title)
                .aspectRatio(0.75, contentMode: .fit)
                .overlay(alignment: .topLeading) {
                    Text(badge)
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(Color("chatBackground"))
                        .padding(.horizontal, 7)
                        .padding(.vertical, 3)
                        .background(Color("assistantText").opacity(0.82), in: Capsule())
                        .padding(7)
                }
            Text(title)
                .font(.system(size: 12.5, weight: .semibold))
                .lineLimit(1)
            Text(reason)
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
                .help(detail)
            HStack(spacing: 8) {
                Button("重试") { }.buttonStyle(.link)
                Button("移除") { }.buttonStyle(.link)
            }
            .font(.system(size: 11))
            Spacer(minLength: 0)
        }
    }
}

// MARK: - 书架页

private struct BookshelfPreviewView: View {
    enum State { case grid, empty, importing, failed }
    var state: State = .grid

    private let columns = [GridItem(.adaptive(minimum: 132), spacing: 14)]

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
        }
        .frame(width: 840)
        .background(Color("chatBackground").opacity(0.35))
    }

    private var header: some View {
        HStack(spacing: 8) {
            Button { } label: {
                HStack(spacing: 3) {
                    Image(systemName: "chevron.left").font(.system(size: 11, weight: .semibold))
                    Text("返回").font(.system(size: 12))
                }
                .foregroundColor(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Divider().frame(height: 14)
            Text("书架").font(.system(size: 13, weight: .semibold))
            Spacer()

            if state == .importing {
                ProgressView().controlSize(.small)
            } else {
                Button { } label: {
                    Image(systemName: "tray.and.arrow.down").font(.system(size: 12))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain).help("导入 EPUB")
            }
            Button { } label: {
                Image(systemName: "arrow.clockwise").font(.system(size: 12))
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain).help("刷新")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    @ViewBuilder
    private var content: some View {
        switch state {
        case .empty:
            Spacer()
            VStack(spacing: 10) {
                Image(systemName: "books.vertical")
                    .font(.system(size: 34, weight: .light))
                    .foregroundColor(.secondary.opacity(0.5))
                Text("还没有书").font(.system(size: 14, weight: .medium))
                Text("支持可重排版的 .epub · 也可以直接把文件拖进来")
                    .font(.system(size: 11.5))
                    .foregroundColor(.secondary)
                Button("导入 EPUB") { }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    .padding(.top, 4)
            }
            Spacer()
        case .grid, .importing, .failed:
            ScrollView {
                LazyVGrid(columns: columns, alignment: .leading, spacing: 18) {
                    if state == .importing {
                        BookImportingCard(title: "技术的本质", step: "正在解析目录…")
                    }
                    if state == .failed {
                        BookImportFailedCard(
                            title: "三体",
                            badge: "DRM 保护",
                            reason: "内容已加密，无法读取",
                            detail: "这本 EPUB 受 DRM 保护，正文被加密，阅读器无法解密。请导入未加密的版本。"
                        )
                    }
                    ForEach(state == .importing ? Array(DemoData.books.prefix(4)) : DemoData.books) { book in
                        BookShelfCard(book: book)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.vertical, 16)
            }
            .overlay(alignment: .bottom) {
                // 拖拽态示意：真实现只在拖入时出现
                if state == .importing {
                    RoundedRectangle(cornerRadius: 10, style: .continuous)
                        .strokeBorder(Color("appAccent").opacity(0.5),
                                      style: StrokeStyle(lineWidth: 1.5, dash: [5, 4]))
                        .padding(10)
                        .allowsHitTesting(false)
                }
            }
        }
    }
}

// MARK: - 阅读页

private struct BookReaderPreviewView: View {
    var tocExpanded = false
    var annotExpanded = false
    var hasChapters = true

    @State private var mode: ReadingMode
    @State private var settingsOpen: Bool
    @State private var fontSize: Double = 100
    @State private var linePreset: Preset = .standard
    @State private var paraPreset: Preset = .standard
    @State private var widthPreset: PageWidth = .standard
    @State private var fontChoice: FontChoice = .system
    @State private var progress: Double = 0.42

    init(tocExpanded: Bool = false, annotExpanded: Bool = false, hasChapters: Bool = true,
         mode: ReadingMode = .scroll, settingsOpen: Bool = false) {
        self.tocExpanded = tocExpanded
        self.annotExpanded = annotExpanded
        self.hasChapters = hasChapters
        _mode = State(initialValue: mode)
        _settingsOpen = State(initialValue: settingsOpen)
    }

    private var currentChapter: DemoBook { DemoData.books[0] }

    var body: some View {
        ZStack(alignment: .bottomTrailing) {
            VStack(spacing: 0) {
                header
                Divider()
                HStack(spacing: 0) {
                    if tocExpanded { tocSidebar } else { collapsedStrip(help: "展开目录") }
                    Divider()
                    readingColumn
                    Divider().opacity(0.4)
                    if annotExpanded { annotationPanel } else { collapsedStrip(help: "展开划线与批注") }
                }
                .frame(maxHeight: .infinity)
                Divider()
                bottomBar
            }

            // Aa 弹层：贴底栏右上方，不遮正文列
            if settingsOpen {
                settingsPopover
                    .padding(.trailing, 12)
                    .padding(.bottom, 52)
            }
        }
        .frame(width: 840, height: 680)
        .background(Color("chatBackground").opacity(0.35))
    }

    private var header: some View {
        HStack(spacing: 8) {
            Button { } label: {
                HStack(spacing: 3) {
                    Image(systemName: "chevron.left").font(.system(size: 11, weight: .semibold))
                    Text("书架").font(.system(size: 12))
                }
                .foregroundColor(.secondary)
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            Divider().frame(height: 14)
            Text(currentChapter.title).font(.system(size: 13, weight: .semibold)).lineLimit(1)
            Spacer()

            if hasChapters {
                Text("第 3/10 章")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .monospacedDigit()

                Button { } label: { Image(systemName: "chevron.left").font(.system(size: 12)) }
                    .buttonStyle(.plain).help("上一章（⌥⌘←）")
                Button { } label: { Image(systemName: "chevron.right").font(.system(size: 12)) }
                    .buttonStyle(.plain).help("下一章（⌥⌘→）")

                Button { } label: {
                    Image(systemName: "list.bullet")
                        .font(.system(size: 12))
                        .foregroundColor(tocExpanded ? Color("appAccent") : .secondary)
                }
                .buttonStyle(.plain).help("目录")
            }

            Button { } label: { Image(systemName: "magnifyingglass").font(.system(size: 12)) }
                .buttonStyle(.plain).help("本章查找（⌘F）")

            Button { } label: {
                Image(systemName: "list.bullet.rectangle")
                    .font(.system(size: 12))
                    .foregroundColor(annotExpanded ? Color("appAccent") : .secondary)
            }
            .buttonStyle(.plain).help("划线与批注（⌘⇧A）")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func collapsedStrip(help: String) -> some View {
        VStack {
            Spacer()
            Button { } label: {
                Image(systemName: help == "展开目录" ? "chevron.right" : "chevron.left")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 28, height: 28)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help(help)
            Spacer()
        }
        .frame(width: 32)
        .background(Color.primary.opacity(0.02))
    }

    private var tocSidebar: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("目录 · 10 章")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundColor(.secondary)
                Spacer()
                Button { } label: {
                    Image(systemName: "chevron.left").font(.system(size: 10, weight: .semibold))
                }
                .buttonStyle(.plain)
                .help("收起目录")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 9)

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 1) {
                    ForEach(DemoData.toc) { row in
                        HStack(spacing: 6) {
                            Text(row.title)
                                .font(.system(size: 12, weight: row.isCurrent ? .semibold : .regular))
                                .foregroundColor(row.isCurrent ? .primary : .secondary)
                                .lineLimit(1)
                            Spacer(minLength: 0)
                        }
                        .padding(.leading, CGFloat(row.depth) * 12 + 10)
                        .padding(.trailing, 10)
                        .padding(.vertical, 5)
                        .background(
                            row.isCurrent ? Color("appAccent").opacity(0.10) : .clear,
                            in: RoundedRectangle(cornerRadius: 5)
                        )
                        .padding(.horizontal, 6)
                    }
                }
                .padding(.bottom, 8)
            }
        }
        .frame(width: 220)
        .background(Color.primary.opacity(0.02))
    }

    private var baseFontSize: CGFloat { FontSizeManager.shared.scaledSize * fontSize / 100 }
    private var lineGap: CGFloat {
        let lh: CGFloat = linePreset == .compact ? 1.55 : linePreset == .standard ? 1.78 : 2.0
        return baseFontSize * (lh - 1.2)
    }
    private var paragraphGap: CGFloat {
        paraPreset == .compact ? 6 : paraPreset == .standard ? 11 : 18
    }
    private var measureWidth: CGFloat {
        widthPreset == .narrow ? 640 : widthPreset == .standard ? 728 : 860
    }
    private var paragraphFont: Font {
        let size = baseFontSize * 1.18
        return fontChoice == .serif ? .custom("Songti SC", size: size) : .system(size: size)
    }

    private var readingColumn: some View {
        ScrollView(.vertical) {
            VStack(alignment: .leading, spacing: paragraphGap) {
                Text("2.2 收入与消费")
                    .font(.system(size: baseFontSize * 1.5, weight: .semibold))

                ForEach(Array(DemoData.paragraphs.enumerated()), id: \.offset) { _, paragraph in
                    Text(paragraph)
                        .font(paragraphFont)
                        .lineSpacing(lineGap)
                        .fixedSize(horizontal: false, vertical: true)
                }

                // 无章节的书（E20）不出现章末「下一章」
                if hasChapters {
                    HStack {
                        Spacer()
                        Button("下一章 ›") { }
                            .buttonStyle(.plain)
                            .font(.system(size: 13))
                            .foregroundColor(Color("appAccent"))
                    }
                    .padding(.top, 8)
                }
            }
            .frame(maxWidth: measureWidth, alignment: .leading)
            .padding(.horizontal, 28)
            .padding(.vertical, 24)
            .frame(maxWidth: .infinity, alignment: .center)
        }
        .scrollDisabled(mode == .paged)
        .overlay {
            // 翻页模式：左右各 28% 是翻页热区，中间 44% 不拦截（留给选字）
            if mode == .paged {
                GeometryReader { geo in
                    HStack(spacing: 0) {
                        PagedTapZone(systemImage: "chevron.left", help: "上一页（←）")
                            .frame(width: geo.size.width * 0.28)
                        Color.clear.frame(width: geo.size.width * 0.44)
                        PagedTapZone(systemImage: "chevron.right", help: "下一页（→）")
                            .frame(width: geo.size.width * 0.28)
                    }
                }
            }
        }
    }

    // MARK: 底部阅读栏（常驻）

    private var progressTooltip: String {
        let pct = Int(progress * 100)
        return hasChapters ? "第 3/10 章 · \(pct)%" : "\(pct)%"
    }

    private var bottomBar: some View {
        HStack(spacing: 10) {
            Text(hasChapters ? "2.2 收入与消费" : currentChapter.title)
                .font(.system(size: 11))
                .foregroundColor(.secondary)
                .lineLimit(1)
                .truncationMode(.tail)
                .frame(maxWidth: 210, alignment: .leading)
                .fixedSize(horizontal: false, vertical: true)

            BookProgressRail(progress: $progress, tooltip: progressTooltip)

            if hasChapters {
                chromeButton("list.bullet", help: "目录", active: tocExpanded) { }
            }
            chromeButton("list.bullet.rectangle", help: "划线与批注（⌘⇧A）", active: annotExpanded) { }
            chromeButton("textformat.size", help: "阅读设置", active: settingsOpen) {
                settingsOpen.toggle()
            }
        }
        .padding(.horizontal, 12)
        .frame(height: 46)
    }

    private func chromeButton(_ systemName: String, help: String, active: Bool,
                              action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 12))
                .foregroundColor(active ? Color("appAccent") : .secondary)
                .frame(width: 24, height: 24)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(help)
    }

    // MARK: Aa 弹层

    private var settingsPopover: some View {
        VStack(alignment: .leading, spacing: 10) {
            Text("阅读设置（全局生效）")
                .font(.system(size: 11, weight: .semibold))
                .foregroundColor(.secondary)

            settingsRow("翻页") {
                Picker("", selection: $mode) {
                    ForEach(ReadingMode.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 150)
            }

            settingsRow("字号") {
                HStack(spacing: 2) {
                    stepButton("minus") { fontSize = max(70, fontSize - 10) }
                    Text("\(Int(fontSize))%")
                        .font(.system(size: 11.5))
                        .monospacedDigit()
                        .frame(width: 46)
                    stepButton("plus") { fontSize = min(150, fontSize + 10) }
                }
            }

            settingsRow("行距") { presetPicker($linePreset) }
            settingsRow("段距") { presetPicker($paraPreset) }

            settingsRow("页宽") {
                Picker("", selection: $widthPreset) {
                    ForEach(PageWidth.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 150)
            }

            settingsRow("字体") {
                Picker("", selection: $fontChoice) {
                    ForEach(FontChoice.allCases, id: \.self) { Text($0.rawValue).tag($0) }
                }
                .pickerStyle(.segmented)
                .labelsHidden()
                .controlSize(.small)
                .frame(width: 110)
            }

            Text("背景跟随 app 主题；划线一期单色（多色二期）")
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
                .padding(.top, 2)
        }
        .padding(12)
        .frame(width: 308)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 12, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.10), lineWidth: 0.5)
        }
        .shadow(color: .black.opacity(0.18), radius: 16, y: 8)
    }

    private func settingsRow<Content: View>(_ label: String,
                                            @ViewBuilder content: () -> Content) -> some View {
        HStack {
            Text(label).font(.system(size: 12))
            Spacer(minLength: 8)
            content()
        }
    }

    private func presetPicker(_ selection: Binding<Preset>) -> some View {
        Picker("", selection: selection) {
            ForEach(Preset.allCases, id: \.self) { Text($0.rawValue).tag($0) }
        }
        .pickerStyle(.segmented)
        .labelsHidden()
        .controlSize(.small)
        .frame(width: 150)
    }

    private func stepButton(_ systemName: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Image(systemName: systemName)
                .font(.system(size: 10, weight: .semibold))
                .frame(width: 22, height: 20)
                .background(Color.primary.opacity(0.06), in: RoundedRectangle(cornerRadius: 5))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }

    private var annotationPanel: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("划线与批注 · \(DemoData.annotations.count)")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundColor(.secondary)
                Spacer()
                Button { } label: {
                    Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold))
                }
                .buttonStyle(.plain)
                .help("收起批注面板")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 9)

            ScrollView {
                LazyVStack(alignment: .leading, spacing: 10) {
                    ForEach(DemoData.annotations) { a in
                        BookAnnotationCardPreview(annotation: a)
                    }
                    Text("选中文字即可划线")
                        .font(.system(size: 10.5))
                        .foregroundColor(.secondary.opacity(0.7))
                        .frame(maxWidth: .infinity)
                        .padding(.top, 6)
                }
                .padding(.horizontal, 10)
                .padding(.bottom, 12)
            }
        }
        .frame(width: 320)
        .background(Color.primary.opacity(0.02))
    }
}

// MARK: - 阅读页小部件

private enum ReadingMode: String, CaseIterable {
    case scroll = "上下滚动"
    case paged = "左右翻页"
}

private enum Preset: String, CaseIterable {
    case compact = "紧凑"
    case standard = "标准"
    case loose = "宽松"
}

private enum PageWidth: String, CaseIterable {
    case narrow = "窄"
    case standard = "标准"
    case wide = "宽"
}

private enum FontChoice: String, CaseIterable {
    case system = "系统"
    case serif = "衬线"
}

/// 翻页热区：悬停才浮出方向箭头，平时完全透明。
private struct PagedTapZone: View {
    let systemImage: String
    let help: String
    @State private var hovered = false

    var body: some View {
        ZStack {
            Image(systemName: systemImage)
                .font(.system(size: 22, weight: .light))
                .foregroundColor(.secondary.opacity(hovered ? 0.7 : 0))
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .contentShape(Rectangle())
        .onHover { hovered = $0 }
        .help(help)
    }
}

/// 底栏进度条：3pt 轨道 + 圆点，可拖；数字只走 tooltip。
private struct BookProgressRail: View {
    @Binding var progress: Double
    let tooltip: String

    var body: some View {
        GeometryReader { geo in
            let width = geo.size.width
            ZStack(alignment: .leading) {
                Capsule()
                    .fill(Color.primary.opacity(0.12))
                    .frame(height: 3)
                Capsule()
                    .fill(Color("appAccent").opacity(0.72))
                    .frame(width: max(0, width * progress), height: 3)
                Circle()
                    .fill(Color("appAccent"))
                    .frame(width: 9, height: 9)
                    .overlay {
                        Circle().strokeBorder(Color("appAccent").opacity(0.18), lineWidth: 3)
                    }
                    .offset(x: min(max(0, width * progress - 4.5), max(0, width - 9)))
            }
            .frame(maxHeight: .infinity)
            .contentShape(Rectangle())
            .gesture(
                DragGesture(minimumDistance: 0)
                    .onChanged { value in
                        progress = min(1, max(0, value.location.x / max(1, width)))
                    }
            )
        }
        .frame(height: 14)
        .help(tooltip)
    }
}

private struct BookAnnotationCardPreview: View {
    let annotation: DemoAnnotation
    @ObservedObject private var fontManager = FontSizeManager.shared

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(annotation.quote)
                .font(.system(size: fontManager.scaledSize * 0.88))
                .lineLimit(3)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)
                .frame(maxWidth: .infinity, alignment: .leading)

            if !annotation.note.isEmpty {
                Text(annotation.note)
                    .font(.system(size: fontManager.scaledSize * 0.88))
                    .foregroundColor(.secondary)
                    .lineLimit(4)
                    .fixedSize(horizontal: false, vertical: true)
                    .padding(.leading, 7)
                    .overlay(alignment: .leading) {
                        Rectangle().fill(Color("appAccent")).frame(width: 2)
                    }
            }

            HStack(spacing: 6) {
                Text(annotation.chapterTitle)
                    .font(.system(size: fontManager.scaledSize * 0.68))
                    .foregroundColor(.secondary.opacity(0.75))
                    .lineLimit(1)
                Spacer()
                Text(annotation.timeText)
                    .font(.system(size: fontManager.scaledSize * 0.68))
                    .foregroundColor(.secondary.opacity(0.7))
            }
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
    }
}

// MARK: - 画廊入口

/// 一个页面里切场景，方便连续看；#Preview 也按场景逐个给了。
struct BookUIStudio: View {
    private enum Scene: String, CaseIterable {
        case shelf = "书架"
        case shelfEmpty = "空态"
        case shelfImport = "导入中"
        case shelfFail = "导入失败"
        case reader = "阅读页"
        case readerNoChapter = "阅读页·无章节"
        case readerPaged = "阅读页·翻页"
        case readerSettings = "阅读页·Aa"
        case readerOpen = "阅读页·双栏"
    }

    @State private var scene: Scene = .shelf

    var body: some View {
        VStack(spacing: 0) {
            Picker("", selection: $scene) {
                ForEach(Scene.allCases, id: \.self) { Text($0.rawValue).tag($0) }
            }
            .pickerStyle(.segmented)
            .labelsHidden()
            .padding(10)

            Divider()

            switch scene {
            case .shelf:            BookshelfPreviewView(state: .grid)
            case .shelfEmpty:       BookshelfPreviewView(state: .empty)
            case .shelfImport:      BookshelfPreviewView(state: .importing)
            case .shelfFail:        BookshelfPreviewView(state: .failed)
            case .reader:           BookReaderPreviewView()
            case .readerNoChapter:  BookReaderPreviewView(hasChapters: false)
            case .readerPaged:      BookReaderPreviewView(mode: .paged)
            case .readerSettings:   BookReaderPreviewView(mode: .paged, settingsOpen: true)
            case .readerOpen:       BookReaderPreviewView(tocExpanded: true, annotExpanded: true)
            }
        }
    }
}

#Preview("书架 · 网格") { BookshelfPreviewView(state: .grid) }
#Preview("书架 · 空态") { BookshelfPreviewView(state: .empty) }
#Preview("书架 · 导入中") { BookshelfPreviewView(state: .importing) }
#Preview("书架 · 导入失败") { BookshelfPreviewView(state: .failed) }
#Preview("阅读页 · 默认收起") { BookReaderPreviewView() }
#Preview("阅读页 · 无章节") { BookReaderPreviewView(hasChapters: false) }
#Preview("阅读页 · 左右翻页") { BookReaderPreviewView(mode: .paged) }
#Preview("阅读页 · Aa 弹层") { BookReaderPreviewView(mode: .paged, settingsOpen: true) }
#Preview("阅读页 · 双栏展开") { BookReaderPreviewView(tocExpanded: true, annotExpanded: true) }
#Preview("画廊") { BookUIStudio() }

#endif
#endif
