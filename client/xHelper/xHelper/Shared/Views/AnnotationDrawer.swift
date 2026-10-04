import SwiftUI

/// 面板两种视图：讨论（常驻线程）/ 全部（扁平导航列表）。
/// 跨平台定义：iOS 也要读 `@AppStorage` 的默认值（面板本身仅 macOS 渲染）。
enum AnnotationPanelMode: String, CaseIterable, Identifiable {
    case thread
    case list

    var id: String { rawValue }

    var icon: String {
        switch self {
        case .thread: return "bubble.left.and.text.bubble.right"
        case .list:   return "list.bullet"
        }
    }

    var help: String {
        switch self {
        case .thread: return "讨论视图（批注下的 AI 回复）"
        case .list:   return "全部标注（只列划线/批注，不含讨论）"
        }
    }
}

/// 划线高亮色（与阅读器里 `mark.hl` 的 `--hl` 同一个色号）：浅色主题绿 appAccent / 深色薄荷。
/// 连线、预览波浪线都用它 —— 别改色，要和真机划线一致。
enum AnnotationHighlightColor {
    static func value(_ scheme: ColorScheme) -> Color {
        scheme == .dark
            ? Color(red: 146 / 255, green: 200 / 255, blue: 180 / 255)   // --hl dark（≈ rgba(146,200,180,.9)）
            : Color(red: 47 / 255, green: 125 / 255, blue: 99 / 255)     // --hl light #2F7D63
    }
}

/// Word 式连线：从批注卡片左缘连到正文划线（起点/终点都在 `.global`，本视图自己换算到本地）。
/// 画一条**水平出入的贝塞尔曲线**（两端切线水平，中间斜连），细、圆头，色号同划线高亮。
/// `allowsHitTesting(false)`。跨平台定义：iOS 不用（端点恒 nil），但共享文件必须能编。
struct AnnotationConnectorLayer: View {
    let start: CGPoint?
    let end: CGPoint?

    @Environment(\.colorScheme) private var colorScheme

    var body: some View {
        GeometryReader { geo in
            let origin = geo.frame(in: .global).origin
            if let s = start, let e = end {
                Path { path in
                    let from = CGPoint(x: s.x - origin.x, y: s.y - origin.y)
                    let to = CGPoint(x: e.x - origin.x, y: e.y - origin.y)
                    // 终点在起点左侧 → 起点向左出、终点从右入；反之镜像。
                    // bend 保底 28，两点很靠近时曲线也不塌成直线。
                    let dir: CGFloat = to.x >= from.x ? 1 : -1
                    let bend = max(28, abs(to.x - from.x) * 0.45)
                    path.move(to: from)
                    path.addCurve(
                        to: to,
                        control1: CGPoint(x: from.x + dir * bend, y: from.y),
                        control2: CGPoint(x: to.x - dir * bend, y: to.y)
                    )
                }
                .stroke(AnnotationHighlightColor.value(colorScheme),
                        style: StrokeStyle(lineWidth: 1, lineCap: .round))
            }
        }
        .allowsHitTesting(false)
    }
}

#if os(macOS)

/// 划线与批注面板：两种视图——
/// - **讨论**（常驻，Word 批注窗格式）：每条批注 = 卡片 + 可折叠线程，用来读/追问；
/// - **全部**（扁平一级列表 + 三 tab，纯导航）。
///
/// 面板本身是纯展示：数据（annotations / replies / 展开态 …）与动作全部由外部注入，
/// 好让 `#Preview` 直接喂 mock、不碰 WebView 控制器。吸附/收放交给宿主 `DocumentFullView`。
struct AnnotationPanel: View {

    let annotations: [DocumentAnnotation]
    let replies: [String: [AnnotationReply]]
    let expandedIDs: Set<String>
    let loadingIDs: Set<String>
    let busyIDs: Set<String>
    @Binding var mode: AnnotationPanelMode
    let relocatingID: String?
    let onToggle: (String) -> Void
    let onJump: (String) -> Void
    let onReply: (String, String?) -> Void
    let onDelete: (String) -> Void
    let onRelocate: (String) -> Void
    let onCancelRelocate: () -> Void
    let onCollapse: () -> Void
    /// 卡片悬停变化：(批注 id, 是否在卡内, 卡片 global frame)。用于画「卡片→划线」连线。
    let onHoverCard: (String, Bool, CGRect) -> Void

    /// 字号跟随全局设置（面板内所有文本按 scaledSize 比例放大）
    @ObservedObject private var fontManager = FontSizeManager.shared

    /// 与服务端 `List` 同序（start_offset ASC, id ASC），卡片顺序才稳定
    private var ordered: [DocumentAnnotation] {
        annotations.sorted { a, b in
            a.startOffset != b.startOffset ? a.startOffset < b.startOffset : a.id < b.id
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().opacity(0.3)
            content
        }
        .frame(width: Layout.docAnnotPanelWidth)
    }

    // MARK: - 头

    private var header: some View {
        HStack(spacing: 6) {
            Text("划线与批注")
                .font(.system(size: fontManager.scaledSize * 0.76, weight: .semibold))
                .foregroundColor(.secondary)
            Text("\(annotations.count)")
                .font(.system(size: fontManager.scaledSize * 0.68))
                .foregroundColor(.secondary)
                .padding(.horizontal, 6)
                .padding(.vertical, 1)
                .background(Color.secondary.opacity(0.12))
                .clipShape(Capsule())
            Spacer()
            modeButton(.thread)
            modeButton(.list)
            Button(action: onCollapse) {
                Image(systemName: "chevron.right")
                    .font(.system(size: fontManager.scaledSize * 0.68, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 24, height: 24)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("收起批注面板")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private func modeButton(_ item: AnnotationPanelMode) -> some View {
        let active = mode == item
        return Button { mode = item } label: {
            Image(systemName: item.icon)
                .font(.system(size: fontManager.scaledSize * 0.64, weight: .medium))
                .foregroundColor(active ? Color("appAccent") : .secondary)
                .frame(width: 24, height: 24)
                .background(active ? Color("appAccent").opacity(0.12) : .clear)
                .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .help(item.help)
    }

    // MARK: - 内容

    @ViewBuilder
    private var content: some View {
        if annotations.isEmpty {
            emptyState
        } else if mode == .thread {
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 4) {
                    ForEach(ordered) { a in
                        AnnotationThreadCard(
                            annotation: a,
                            replies: replies[a.id] ?? [],
                            expanded: expandedIDs.contains(a.id),
                            loading: loadingIDs.contains(a.id),
                            busy: busyIDs.contains(a.id),
                            onToggle: { onToggle(a.id) },
                            onJump: { onJump(a.id) },
                            onReply: { onReply(a.id, $0) },
                            onDelete: { onDelete(a.id) },
                            onHoverChange: { inside, frame in onHoverCard(a.id, inside, frame) }
                        )
                    }
                }
                .padding(8)
            }
        } else {
            AnnotationListBody(
                annotations: ordered,
                relocatingID: relocatingID,
                onJump: onJump,
                onDelete: onDelete,
                onRelocate: onRelocate,
                onCancelRelocate: onCancelRelocate
            )
        }
    }

    private var emptyState: some View {
        VStack {
            Spacer()
            Text("还没有划线与批注")
                .font(.system(size: fontManager.scaledSize * 0.76))
                .foregroundColor(.secondary)
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// 讨论视图的单条卡片：划线圈引 + 批注 + 可折叠线程 + 追问输入。
struct AnnotationThreadCard: View {

    let annotation: DocumentAnnotation
    let replies: [AnnotationReply]
    let expanded: Bool
    let loading: Bool
    let busy: Bool
    let onToggle: () -> Void
    let onJump: () -> Void
    let onReply: (String?) -> Void
    let onDelete: () -> Void
    /// 悬停变化：(是否在卡内, 卡片 global frame)
    var onHoverChange: (Bool, CGRect) -> Void = { _, _ in }

    @State private var draft = ""
    @State private var frame: CGRect = .zero

    /// 字号跟随全局设置
    @ObservedObject private var fontManager = FontSizeManager.shared

    /// 线程条数：服务端计数优先，本地已加载条数兜底
    private var replyCount: Int { max(annotation.replyCount ?? 0, replies.count) }
    private var lastReply: AnnotationReply? { replies.last ?? annotation.lastReply }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            quoteBlock
            if !annotation.note.isEmpty { noteBlock }
            threadSection
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.secondary.opacity(0.06))
        .clipShape(RoundedRectangle(cornerRadius: 8, style: .continuous))
        .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { frame = $0 }
        .onHover { onHoverChange($0, frame) }
        .contextMenu {
            Button(annotation.note.isEmpty ? "删除划线" : "删除批注", role: .destructive) { onDelete() }
        }
    }

    // MARK: - 划线圈引 / 批注

    private var quoteBlock: some View {
        Text(annotation.quote)
            .font(.system(size: fontManager.scaledSize * 0.88))
            .foregroundColor(annotation.orphaned ? .secondary : .primary)
            .strikethrough(annotation.orphaned, color: .secondary)
            .lineLimit(3)
            .multilineTextAlignment(.leading)
            .fixedSize(horizontal: false, vertical: true)
            .frame(maxWidth: .infinity, alignment: .leading)
            .contentShape(Rectangle())
            .onTapGesture { onJump() }
    }

    private var noteBlock: some View {
        Text(annotation.note)
            .font(.system(size: fontManager.scaledSize * 0.88))
            .foregroundColor(.secondary)
            .lineLimit(4)
            .fixedSize(horizontal: false, vertical: true)
            .padding(.leading, 7)
            .overlay(alignment: .leading) {
                Rectangle()
                    .fill(annotation.orphaned ? Color.secondary.opacity(0.3) : Color("appAccent"))
                    .frame(width: 2)
            }
    }

    // MARK: - 线程

    @ViewBuilder
    private var threadSection: some View {
        HStack(spacing: 6) {
            if annotation.orphaned {
                orphanBadge
            }
            Spacer()
            Text(AnnotationTime.label(annotation.createdAt))
                .font(.system(size: fontManager.scaledSize * 0.68))
                .foregroundColor(.secondary.opacity(0.7))
        }

        if replyCount == 0 && !expanded && !busy {
            HStack {
                Spacer()
                Button { onReply(nil) } label: {
                    HStack(spacing: 4) {
                        Image(systemName: "sparkles")
                        Text("让 AI 回复")
                    }
                    .font(.system(size: fontManager.scaledSize * 0.76))
                    .foregroundColor(Color("appAccent"))
                    .contentShape(Rectangle())
                }
                .buttonStyle(.plain)
            }
        } else {
            collapseRow
        }

        if expanded {
            threadBody
        }
    }

    private var collapseRow: some View {
        Button { onToggle() } label: {
            HStack(spacing: 5) {
                Image(systemName: expanded ? "chevron.down" : "chevron.right")
                    .font(.system(size: fontManager.scaledSize * 0.60, weight: .semibold))
                    .foregroundColor(.secondary)
                Text("回复 \(replyCount)")
                    .font(.system(size: fontManager.scaledSize * 0.76))
                    .foregroundColor(.secondary)
                if !expanded, let last = lastReply {
                    Text(last.content)
                        .font(.system(size: fontManager.scaledSize * 0.76))
                        .foregroundColor(.secondary.opacity(0.7))
                        .lineLimit(1)
                }
                Spacer()
                if busy { ProgressView().controlSize(.mini) }
            }
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(busy)
    }

    @ViewBuilder
    private var threadBody: some View {
        if loading {
            HStack(spacing: 6) {
                ProgressView().controlSize(.mini)
                Text("载入讨论…").font(.system(size: fontManager.scaledSize * 0.76)).foregroundColor(.secondary)
            }
        } else {
            VStack(alignment: .leading, spacing: 8) {
                ForEach(replies) { reply in
                    replyRow(reply)
                }
                inputRow
            }
            .padding(.top, 2)
        }
    }

    private func replyRow(_ reply: AnnotationReply) -> some View {
        HStack(alignment: .top, spacing: 6) {
            Rectangle()
                .fill(reply.isAssistant ? Color("appAccent").opacity(0.5) : Color.secondary.opacity(0.3))
                .frame(width: 2)
            VStack(alignment: .leading, spacing: 2) {
                HStack(spacing: 6) {
                    Text(reply.isAssistant ? "AI" : "你")
                        .font(.system(size: fontManager.scaledSize * 0.68, weight: .medium))
                        .foregroundColor(reply.isAssistant ? Color("appAccent") : .secondary)
                    Spacer()
                    Text(AnnotationTime.label(reply.createdAt))
                        .font(.system(size: fontManager.scaledSize * 0.68))
                        .foregroundColor(.secondary.opacity(0.6))
                }
                Text(reply.content)
                    .font(.system(size: fontManager.scaledSize * 0.94))
                    .foregroundColor(.primary)
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }
        }
    }

    private var inputRow: some View {
        HStack(alignment: .bottom, spacing: 6) {
            TextField("追问…", text: $draft, axis: .vertical)
                .textFieldStyle(.plain)
                .font(.system(size: fontManager.scaledSize * 0.88))
                .lineLimit(1...4)
                .padding(.horizontal, 8)
                .padding(.vertical, 5)
                .background(Color.secondary.opacity(0.08))
                .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
                .disabled(busy)
                .onSubmit(submit)
            Button(action: submit) {
                if busy {
                    ProgressView().controlSize(.mini)
                } else {
                    Image(systemName: draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty
                          ? "sparkles" : "paperplane.fill")
                        .font(.system(size: fontManager.scaledSize * 0.72, weight: .medium))
                        .foregroundColor(Color("appAccent"))
                        .frame(width: 24, height: 24)
                        .contentShape(Rectangle())
                }
            }
            .buttonStyle(.plain)
            .disabled(busy)
            .help(draft.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty ? "让 AI 回复" : "发送追问")
        }
    }

    private func submit() {
        guard !busy else { return }
        let content = draft.trimmingCharacters(in: .whitespacesAndNewlines)
        onReply(content.isEmpty ? nil : content)
        draft = ""
    }

    private var orphanBadge: some View {
        Text("已失活")
            .font(.system(size: fontManager.scaledSize * 0.68))
            .foregroundColor(.red.opacity(0.85))
            .padding(.horizontal, 5)
            .padding(.vertical, 1)
            .background(Color.red.opacity(0.1))
            .clipShape(Capsule())
    }
}

/// 「全部」视图：原扁平抽屉——三 tab + 行列表，纯导航，不含讨论。
struct AnnotationListBody: View {

    let annotations: [DocumentAnnotation]
    let relocatingID: String?
    let onJump: (String) -> Void
    let onDelete: (String) -> Void
    let onRelocate: (String) -> Void
    let onCancelRelocate: () -> Void

    /// 字号跟随全局设置
    @ObservedObject private var fontManager = FontSizeManager.shared

    private enum Tab: String, CaseIterable {
        case all = "全部"
        case note = "仅批注"
        case orphan = "已失活"
    }

    @State private var tab: Tab = .all

    private var visible: [DocumentAnnotation] {
        switch tab {
        case .all:    return annotations
        case .note:   return annotations.filter { !$0.note.isEmpty }
        case .orphan: return annotations.filter(\.orphaned)
        }
    }

    private func count(of tab: Tab) -> Int {
        switch tab {
        case .all:    return annotations.count
        case .note:   return annotations.filter { !$0.note.isEmpty }.count
        case .orphan: return annotations.filter(\.orphaned).count
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            tabs
            Divider().opacity(0.3)
            if visible.isEmpty {
                emptyState
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 2) {
                        ForEach(visible) { annotation in
                            if annotation.orphaned {
                                orphanRow(annotation)
                            } else {
                                normalRow(annotation)
                            }
                        }
                    }
                    .padding(8)
                }
            }
        }
    }

    private var tabs: some View {
        HStack(spacing: 4) {
            ForEach(Tab.allCases, id: \.self) { item in
                Button { tab = item } label: {
                    Text("\(item.rawValue) \(count(of: item))")
                        .font(.system(size: fontManager.scaledSize * 0.76))
                        .foregroundColor(tab == item ? .primary : .secondary)
                        .padding(.horizontal, 8)
                        .padding(.vertical, 3)
                        .background(tab == item ? Color.secondary.opacity(0.16) : .clear)
                        .clipShape(RoundedRectangle(cornerRadius: 6, style: .continuous))
                }
                .buttonStyle(.plain)
            }
            Spacer()
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 7)
    }

    private var emptyState: some View {
        VStack {
            Spacer()
            Text(emptyText)
                .font(.system(size: fontManager.scaledSize * 0.76))
                .foregroundColor(.secondary)
            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    private var emptyText: String {
        switch tab {
        case .all:    return "还没有划线与批注"
        case .note:   return "还没有写批注的划线"
        case .orphan: return "没有已失活的划线"
        }
    }

    /// 正常行：点击滚到对应划线并闪烁（面板不关，方便连着看几条）
    private func normalRow(_ a: DocumentAnnotation) -> some View {
        rowContent(a)
            .padding(.horizontal, 10)
            .padding(.vertical, 8)
            .contentShape(Rectangle())
            .onTapGesture { onJump(a.id) }
            .contextMenu { rowMenu(a) }
    }

    /// 失活行：整行灰 + quote 删除线，右侧两个动作（点击不跳转，正文里本来就没有它）
    private func orphanRow(_ a: DocumentAnnotation) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            rowContent(a, orphaned: true)
            if relocatingID == a.id {
                HStack(spacing: 6) {
                    Text("请在正文中重新选中一段文字")
                        .font(.system(size: fontManager.scaledSize * 0.76))
                        .foregroundColor(Color("appAccent"))
                    Spacer()
                    Button("取消") { onCancelRelocate() }
                        .buttonStyle(.plain)
                        .font(.system(size: fontManager.scaledSize * 0.76))
                        .foregroundColor(.secondary)
                }
                .padding(.top, 6)
            } else {
                HStack(spacing: 6) {
                    Spacer()
                    Button("重新定位") { onRelocate(a.id) }
                        .buttonStyle(.plain)
                        .font(.system(size: fontManager.scaledSize * 0.76))
                        .foregroundColor(Color("appAccent"))
                    Button("删除") { onDelete(a.id) }
                        .buttonStyle(.plain)
                        .font(.system(size: fontManager.scaledSize * 0.76))
                        .foregroundColor(.red.opacity(0.85))
                }
                .padding(.top, 6)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .contextMenu { rowMenu(a) }
    }

    /// 右键菜单：正文里定位不到 / 失活的批注在正文中点不到，这里是唯一入口。
    @ViewBuilder
    private func rowMenu(_ a: DocumentAnnotation) -> some View {
        if a.orphaned {
            Button("重新定位") { onRelocate(a.id) }
        }
        Button(a.note.isEmpty ? "删除划线" : "删除批注", role: .destructive) { onDelete(a.id) }
    }

    private func rowContent(_ a: DocumentAnnotation, orphaned: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(a.quote)
                .font(.system(size: fontManager.scaledSize * 0.88))
                .foregroundColor(orphaned ? .secondary : .primary)
                .strikethrough(orphaned, color: .secondary)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

            if !a.note.isEmpty {
                Text(a.note)
                    .font(.system(size: fontManager.scaledSize * 0.82))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
                    .padding(.leading, 7)
                    .overlay(alignment: .leading) {
                        Rectangle()
                            .fill(orphaned ? Color.secondary.opacity(0.3) : Color("appAccent"))
                            .frame(width: 2)
                    }
            }

            HStack(spacing: 6) {
                if orphaned {
                    Text("已失活")
                        .font(.system(size: fontManager.scaledSize * 0.68))
                        .foregroundColor(.red.opacity(0.85))
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(Color.red.opacity(0.1))
                        .clipShape(Capsule())
                }
                Spacer()
                Text(AnnotationTime.label(a.createdAt))
                    .font(.system(size: fontManager.scaledSize * 0.68))
                    .foregroundColor(.secondary.opacity(0.7))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// 与服务端 `created_at`（ISO8601，可能带小数秒）对齐；今天只显示时分，更早带日期。
enum AnnotationTime {
    static func label(_ iso: String) -> String {
        let parser = ISO8601DateFormatter()
        parser.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        guard let date = parser.date(from: iso) ?? ISO8601DateFormatter().date(from: iso) else {
            return String(iso.prefix(10))
        }
        let f = DateFormatter()
        if Calendar.current.isDateInToday(date) {
            f.dateFormat = "HH:mm"
        } else {
            f.dateFormat = "yyyy/MM/dd HH:mm"
        }
        return f.string(from: date)
    }
}

// MARK: - Preview

#if DEBUG
private enum AnnotationPreviewData {
    static let plain = DocumentAnnotation(
        id: "a1", documentId: "d1", quote: "第三个方案的部署成本明显高于前两个，需要重新评估。",
        prefix: "", suffix: "", note: "这里记一下，回头和财务对一下数字。",
        startOffset: 10, endOffset: 40, orphaned: false, quoteRewritten: false,
        createdAt: "2026-09-30T12:00:00+08:00", updatedAt: "2026-09-30T12:00:00+08:00",
        replyCount: 3,
        lastReply: AnnotationReply(id: "r3", annotationId: "a1", role: "assistant",
                                   content: "按当前报价，第三方案的年化成本约为前两个的 1.8 倍。",
                                   source: "button", createdAt: "2026-09-30T12:05:00+08:00")
    )
    static let threaded = DocumentAnnotation(
        id: "a2", documentId: "d1", quote: "本节结论与第 2 章的假设冲突。",
        prefix: "", suffix: "", note: "",
        startOffset: 100, endOffset: 116, orphaned: false, quoteRewritten: false,
        createdAt: "2026-09-30T09:10:00+08:00", updatedAt: "2026-09-30T09:10:00+08:00",
        replyCount: 0, lastReply: nil
    )
    static let orphan = DocumentAnnotation(
        id: "a3", documentId: "d1", quote: "已被改写掉的一段原文。",
        prefix: "", suffix: "", note: "这条线对不上了。",
        startOffset: 0, endOffset: 0, orphaned: true, quoteRewritten: true,
        createdAt: "2026-09-28T08:00:00+08:00", updatedAt: "2026-09-28T08:00:00+08:00",
        replyCount: 0, lastReply: nil
    )

    static let replies: [AnnotationReply] = [
        AnnotationReply(id: "r1", annotationId: "a1", role: "user", content: "为什么高这么多？",
                        source: "followup", createdAt: "2026-09-30T12:02:00+08:00"),
        AnnotationReply(id: "r2", annotationId: "a1", role: "assistant",
                        content: "主要差在第三方案要额外的专线带宽与两地容灾，见文档 4.2 节。",
                        source: "followup", createdAt: "2026-09-30T12:03:00+08:00"),
        AnnotationReply(id: "r3", annotationId: "a1", role: "assistant",
                        content: "按当前报价，第三方案的年化成本约为前两个的 1.8 倍。",
                        source: "button", createdAt: "2026-09-30T12:05:00+08:00"),
    ]
}

/// 模拟文档页三栏（目录 / 正文占位 / 真实批注面板），让预览一眼是「电脑版」而非 300 宽窄条。
/// 只用于预览，不进产品代码。
private struct DocPreviewShell<Panel: View>: View {
    var tocExpanded = false
    @ViewBuilder var panel: Panel

    private let toc = ["一、背景", "二、方案对比", "　2.1 成本", "　2.2 工期", "三、结论"]

    var body: some View {
        HStack(spacing: 0) {
            if tocExpanded {
                VStack(alignment: .leading, spacing: 6) {
                    Text("目录")
                        .font(.caption.weight(.semibold))
                        .foregroundColor(.secondary)
                        .padding(.bottom, 2)
                    ForEach(toc, id: \.self) { Text($0).font(.caption2).foregroundColor(.secondary) }
                    Spacer()
                }
                .padding(12)
                .frame(width: Layout.docTocExpandedWidth, alignment: .leading)
            } else {
                VStack {
                    Spacer()
                    Image(systemName: "chevron.right").font(.caption).foregroundColor(.secondary)
                    Spacer()
                }
                .frame(width: Layout.docPanelStripWidth)
            }
            Divider()
            VStack(alignment: .leading, spacing: 10) {
                Text("文档正文（占位）")
                    .font(.title3.weight(.semibold))
                    .foregroundColor(.secondary)
                ForEach(0..<9, id: \.self) { _ in
                    RoundedRectangle(cornerRadius: 3)
                        .fill(Color.secondary.opacity(0.12))
                        .frame(height: 11)
                }
                Spacer()
            }
            .padding(24)
            .frame(maxWidth: .infinity, alignment: .leading)
            Divider()
            panel
        }
        .frame(width: 1200, height: 720)
        .background(Color("chatBackground"))
    }
}

@MainActor
private func previewPanel(
    annotations: [DocumentAnnotation] = [
        AnnotationPreviewData.plain, AnnotationPreviewData.threaded, AnnotationPreviewData.orphan,
    ],
    replies: [String: [AnnotationReply]] = [:],
    expandedIDs: Set<String> = [],
    loadingIDs: Set<String> = [],
    busyIDs: Set<String> = [],
    relocatingID: String? = nil,
    mode: AnnotationPanelMode = .thread,
    onHoverCard: @escaping (String, Bool, CGRect) -> Void = { _, _, _ in }
) -> AnnotationPanel {
    AnnotationPanel(
        annotations: annotations,
        replies: replies,
        expandedIDs: expandedIDs,
        loadingIDs: loadingIDs,
        busyIDs: busyIDs,
        mode: .constant(mode),
        relocatingID: relocatingID,
        onToggle: { _ in }, onJump: { _ in }, onReply: { _, _ in },
        onDelete: { _ in }, onRelocate: { _ in }, onCancelRelocate: {}, onCollapse: {},
        onHoverCard: onHoverCard
    )
}

/// 预览用：正文划线的真实样子是「文字底部波浪线」（浅色主题绿 / 深色薄荷），不是底色高亮。
/// SwiftUI 的 `Text` 不支持 wavy，这里用 `Path` 画一段正弦波凑近真机观感（仅预览）。
private struct WavyUnderline: View {
    @Environment(\.colorScheme) private var colorScheme

    private var color: Color { AnnotationHighlightColor.value(colorScheme) }

    var body: some View {
        GeometryReader { geo in
            let w = geo.size.width
            let mid = geo.size.height / 2
            let amp: CGFloat = 1.6
            let half: CGFloat = 3.5
            Path { p in
                p.move(to: CGPoint(x: 0, y: mid))
                var x: CGFloat = 0
                var up = true
                while x < w {
                    let nx = min(x + half, w)
                    p.addQuadCurve(
                        to: CGPoint(x: nx, y: mid),
                        control: CGPoint(x: (x + nx) / 2, y: mid + (up ? -amp : amp)))
                    x = nx
                    up.toggle()
                }
            }
            .stroke(color, style: StrokeStyle(lineWidth: 1.5, lineCap: .round))
        }
    }
}

/// 连线交互演示：正文占位里放一条假划线（底部波浪线），悬停右侧卡片即从卡片左缘拉线到划线。
/// （真机上划线矩形来自 WKWebView 的 `reader.annotationRect`；预览用占位文本代。）
private struct ConnectorPreviewDemo: View {
    @State private var highlightFrame: CGRect = .zero
    @State private var cardFrame: CGRect?

    var body: some View {
        HStack(spacing: 0) {
            VStack(alignment: .leading, spacing: 14) {
                Text("文档正文（占位）")
                    .font(.title3.weight(.semibold))
                    .foregroundColor(.secondary)
                ForEach(0..<4, id: \.self) { _ in
                    RoundedRectangle(cornerRadius: 3).fill(Color.secondary.opacity(0.12)).frame(height: 11)
                }
                Text("第三个方案的部署成本明显高于前两个，需要重新评估。")
                    .font(.body)
                    .overlay(alignment: .bottom) {
                        WavyUnderline()
                            .frame(height: 4)
                            .offset(y: 3)
                    }
                    .onGeometryChange(for: CGRect.self) { $0.frame(in: .global) } action: { highlightFrame = $0 }
                ForEach(0..<5, id: \.self) { _ in
                    RoundedRectangle(cornerRadius: 3).fill(Color.secondary.opacity(0.12)).frame(height: 11)
                }
                Spacer()
            }
            .padding(24)
            .frame(maxWidth: .infinity, alignment: .leading)
            Divider()
            previewPanel(onHoverCard: { _, inside, frame in cardFrame = inside ? frame : nil })
        }
        .overlay {
            AnnotationConnectorLayer(
                start: cardFrame.map { CGPoint(x: $0.minX, y: $0.midY) },
                end: highlightFrame == .zero ? nil : CGPoint(x: highlightFrame.maxX + 6, y: highlightFrame.midY)
            )
        }
        .frame(width: 1200, height: 720)
        .background(Color("chatBackground"))
    }
}

#Preview("连线 · 悬停卡片") {
    ConnectorPreviewDemo()
}

#Preview("桌面 · 讨论（折叠）") {
    DocPreviewShell { previewPanel() }
}

#Preview("桌面 · 讨论（展开线程）") {
    DocPreviewShell {
        previewPanel(replies: ["a1": AnnotationPreviewData.replies], expandedIDs: ["a1"])
    }
}

#Preview("桌面 · 讨论（生成中）") {
    DocPreviewShell {
        previewPanel(replies: ["a1": AnnotationPreviewData.replies],
                     expandedIDs: ["a1"], busyIDs: ["a1"])
    }
}

#Preview("桌面 · 讨论（追问入口）") {
    DocPreviewShell {
        previewPanel(expandedIDs: ["a2"])
    }
}

#Preview("桌面 · 全部（列表）") {
    DocPreviewShell { previewPanel(mode: .list) }
}

#Preview("桌面 · 目录展开对比") {
    DocPreviewShell(tocExpanded: true) { previewPanel() }
}

#Preview("空态") {
    DocPreviewShell { previewPanel(annotations: []) }
}
#endif
#endif
