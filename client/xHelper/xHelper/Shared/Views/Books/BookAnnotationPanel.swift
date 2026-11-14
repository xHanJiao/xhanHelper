#if os(macOS)
import SwiftUI

/// 书阅读页的划线与批注面板（全书的批注，按章分组）。
///
/// 不复用文档的 `AnnotationPanel`：那个绑死了服务端 `DocumentAnnotation` 与 AI 回复线程
/// （design §6.4）。这里只有本地实体：quote 预览、批注正文、时间、编辑、删除。
struct BookAnnotationPanel: View {
    @ObservedObject var controller: BookReaderController
    let onClose: () -> Void

    @Environment(\.colorScheme) private var colorScheme

    @State private var editingID: String?
    @State private var draft = ""
    @State private var pendingDelete: BookAnnotation?

    private var groups: [(chapterIndex: Int, title: String, items: [BookAnnotation])] {
        let grouped = Dictionary(grouping: controller.allAnnotations, by: { $0.chapterIndex })
        return grouped.keys.sorted().map { index in
            (chapterIndex: index,
             title: controller.book.chapter(at: index)?.title ?? "第 \(index + 1) 章",
             items: (grouped[index] ?? []).sorted { $0.createdAt < $1.createdAt })
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack {
                Text("划线与批注 · \(controller.allAnnotations.count)")
                    .font(.system(size: 11, weight: .semibold))
                    .foregroundColor(.secondary)
                Spacer()
                Button {
                    commitDraftIfNeeded()
                    onClose()
                } label: {
                    Image(systemName: "chevron.right").font(.system(size: 10, weight: .semibold))
                }
                .buttonStyle(.plain)
                .help("收起批注面板")
            }
            .padding(.horizontal, 12)
            .padding(.vertical, 9)

            if controller.allAnnotations.isEmpty {
                VStack(spacing: 6) {
                    Image(systemName: controller.annotationsFailed
                          ? "exclamationmark.triangle" : "text.badge.plus")
                        .font(.system(size: 22, weight: .light))
                        .foregroundColor(.secondary.opacity(0.5))
                    Text(controller.annotationsFailed
                         ? "批注没能载入（本次不改动已有批注）"
                         : "选中文字即可划线")
                        .font(.system(size: 11))
                        .foregroundColor(.secondary)
                        .multilineTextAlignment(.center)
                        .padding(.horizontal, 12)
                }
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                ScrollView {
                    LazyVStack(alignment: .leading, spacing: 12) {
                        ForEach(groups, id: \.chapterIndex) { group in                            Text("第 \(group.chapterIndex + 1) 章 · \(group.title)")
                                .font(.system(size: 10.5, weight: .semibold))
                                .foregroundColor(.secondary)
                                .lineLimit(1)
                                .padding(.horizontal, 10)
                            ForEach(group.items) { item in
                                card(item)
                            }
                        }
                    }
                    .padding(.horizontal, 10)
                    .padding(.bottom, 12)
                }
            }
        }
        .frame(width: 320)
        .background(Color.primary.opacity(0.02))
        // 收起面板 / 切章导致视图消失时，正在写的批注不能被静默丢掉
        .onDisappear { commitDraftIfNeeded() }
        .confirmationDialog(
            pendingDelete.map { _ in "删除这条划线" } ?? "删除",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("删除", role: .destructive) {
                if let item = pendingDelete {
                    controller.deleteAnnotation(id: item.id)
                    if editingID == item.id { editingID = nil }
                }
                pendingDelete = nil
            }
            Button("取消", role: .cancel) { pendingDelete = nil }
        } message: {
            Text("只会删掉这条划线与批注，书本身不受影响。")
        }
    }

    /// 有未保存的草稿就先落盘（收起面板 / 视图消失时调）
    private func commitDraftIfNeeded() {
        guard let id = editingID else { return }
        controller.updateNote(id: id, note: draft)
        editingID = nil
    }

    // MARK: - 卡片

    private func card(_ item: BookAnnotation) -> some View {
        VStack(alignment: .leading, spacing: 6) {
            Text(item.quote)
                .font(.system(size: 12))
                .foregroundColor(.primary)
                .lineLimit(3)
                .fixedSize(horizontal: false, vertical: true)
                // SwiftUI 没有波浪下划线：面板里用实线示意，正文里仍是波浪线
                .underline(true, color: AnnotationHighlightColor.value(colorScheme))
                .multilineTextAlignment(.leading)
                .contentShape(Rectangle())
                .onTapGesture { controller.scrollToAnnotation(id: item.id) }
                .help("跳到正文里的这段话")

            if editingID == item.id {
                TextEditor(text: $draft)
                    .font(.system(size: 12))
                    .frame(minHeight: 56, maxHeight: 110)
                    .scrollContentBackground(.hidden)
                    .padding(6)
                    .background(Color("surfaceInput"), in: RoundedRectangle(cornerRadius: 7))
                HStack(spacing: 8) {
                    Spacer()
                    Button("取消") { editingID = nil }
                        .buttonStyle(.link)
                    Button("保存") {
                        controller.updateNote(id: item.id, note: draft)
                        editingID = nil
                    }
                    .buttonStyle(.link)
                }
                .font(.system(size: 11))
            } else if !item.note.isEmpty {
                Text(item.note)
                    .font(.system(size: 12.5))
                    .foregroundColor(.primary.opacity(0.9))
                    .fixedSize(horizontal: false, vertical: true)
                    .textSelection(.enabled)
            }

            HStack(spacing: 10) {
                Text(Self.timeText(item.createdAt))
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .monospacedDigit()
                Spacer()
                if editingID != item.id {
                    Button("编辑") {
                        draft = item.note
                        editingID = item.id
                    }
                    .buttonStyle(.link)
                    Button("删除") { pendingDelete = item }
                        .buttonStyle(.link)
                        .foregroundColor(.red)
                }
            }
            .font(.system(size: 11))
        }
        .padding(10)
        .frame(maxWidth: .infinity, alignment: .leading)
        .background(Color.primary.opacity(item.chapterIndex == controller.chapterIndex ? 0.045 : 0.02),
                    in: RoundedRectangle(cornerRadius: 9))
        .overlay {
            RoundedRectangle(cornerRadius: 9)
                .strokeBorder(
                    item.id == controller.focusedAnnotationID
                        ? AnnotationHighlightColor.value(colorScheme).opacity(0.55)
                        : Color.clear,
                    lineWidth: 1
                )
        }
    }

    private static let timeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "M月d日 HH:mm"
        return f
    }()

    private static func timeText(_ date: Date) -> String {
        timeFormatter.string(from: date)
    }
}
#endif
