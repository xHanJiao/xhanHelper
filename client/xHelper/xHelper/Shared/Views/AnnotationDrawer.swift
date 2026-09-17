#if os(macOS)
import SwiftUI

/// 划线与批注抽屉（P4）：和目录栏同级的第三列，宽 300。
///
/// 数据全部来自 `DocumentReaderController.annotations`（服务端那一份，DOM 只负责渲染），
/// 所以这里没有任何自己拉取 / 自己缓存的逻辑。失活与否由服务端裁定，抽屉只按 `orphaned` 渲染灰态。
struct AnnotationDrawer: View {

    @ObservedObject var reader: DocumentReaderController
    /// 关抽屉（点标题栏的 chevron 或某一行跳转后用不到 —— 点行不关，见 design.md §8.6）
    let onCollapse: () -> Void

    private enum Tab: String, CaseIterable {
        case all = "全部"
        case note = "仅批注"
        case orphan = "已失活"
    }

    @State private var tab: Tab = .all

    private var allAnnotations: [DocumentAnnotation] { reader.annotations }
    private var noteCount: Int { allAnnotations.filter { !$0.note.isEmpty }.count }
    private var orphanCount: Int { allAnnotations.filter(\.orphaned).count }

    private var visible: [DocumentAnnotation] {
        switch tab {
        case .all:    return allAnnotations
        case .note:   return allAnnotations.filter { !$0.note.isEmpty }
        case .orphan: return allAnnotations.filter(\.orphaned)
        }
    }

    private func count(of tab: Tab) -> Int {
        switch tab {
        case .all:    return allAnnotations.count
        case .note:   return noteCount
        case .orphan: return orphanCount
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            header
            Divider().opacity(0.3)
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
        .frame(width: 300)
    }

    // MARK: - 头 / tabs

    private var header: some View {
        HStack(spacing: 6) {
            Text("划线与批注")
                .font(.caption.weight(.semibold))
                .foregroundColor(.secondary)
            Text("\(allAnnotations.count)")
                .font(.caption2)
                .foregroundColor(.secondary)
                .padding(.horizontal, 6)
                .padding(.vertical, 1)
                .background(Color.secondary.opacity(0.12))
                .clipShape(Capsule())
            Spacer()
            Button(action: onCollapse) {
                Image(systemName: "chevron.right")
                    .font(.caption.weight(.semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 24, height: 24)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("收起划线列表")
        }
        .padding(.horizontal, 16)
        .padding(.vertical, 8)
    }

    private var tabs: some View {
        HStack(spacing: 4) {
            ForEach(Tab.allCases, id: \.self) { item in
                Button { tab = item } label: {
                    Text("\(item.rawValue) \(count(of: item))")
                        .font(.caption2)
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
                .font(.caption)
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

    // MARK: - 行

    /// 正常行：点击滚到对应划线并闪烁（抽屉不关，方便连着看几条）
    private func normalRow(_ a: DocumentAnnotation) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            rowContent(a)
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
        .contentShape(Rectangle())
        .onTapGesture { reader.scrollToAnnotation(id: a.id) }
    }

    /// 失活行：整行灰 + quote 删除线，右侧两个动作（点击不跳转，正文里本来就没有它）
    private func orphanRow(_ a: DocumentAnnotation) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            rowContent(a, orphaned: true)
            if reader.relocatingID == a.id {
                HStack(spacing: 6) {
                    Text("请在正文中重新选中一段文字")
                        .font(.caption2)
                        .foregroundColor(Color("appAccent"))
                    Spacer()
                    Button("取消") { reader.cancelRelocate() }
                        .buttonStyle(.plain)
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .padding(.top, 6)
            } else {
                HStack(spacing: 6) {
                    Spacer()
                    Button("重新定位") { reader.beginRelocate(id: a.id) }
                        .buttonStyle(.plain)
                        .font(.caption2)
                        .foregroundColor(Color("appAccent"))
                    Button("删除") { reader.deleteAnnotation(id: a.id) }
                        .buttonStyle(.plain)
                        .font(.caption2)
                        .foregroundColor(.red.opacity(0.85))
                }
                .padding(.top, 6)
            }
        }
        .padding(.horizontal, 10)
        .padding(.vertical, 8)
    }

    private func rowContent(_ a: DocumentAnnotation, orphaned: Bool = false) -> some View {
        VStack(alignment: .leading, spacing: 5) {
            Text(a.quote)
                .font(.caption)
                .foregroundColor(orphaned ? .secondary : .primary)
                .strikethrough(orphaned, color: .secondary)
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

            if !a.note.isEmpty {
                Text(a.note)
                    .font(.caption2)
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
                        .font(.system(size: 9))
                        .foregroundColor(.red.opacity(0.85))
                        .padding(.horizontal, 5)
                        .padding(.vertical, 1)
                        .background(Color.red.opacity(0.1))
                        .clipShape(Capsule())
                }
                Spacer()
                Text(Self.timeLabel(a.createdAt))
                    .font(.system(size: 9))
                    .foregroundColor(.secondary.opacity(0.7))
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    /// 与服务端 `created_at`（ISO8601，带小数秒）对齐；今天只显示时分，更早带日期。
    private static func timeLabel(_ iso: String) -> String {
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
#endif
