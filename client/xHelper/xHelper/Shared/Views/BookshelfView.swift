#if os(macOS)
import AppKit
import SwiftUI
import UniformTypeIdentifiers

/// 书架页（`BrowsePanel.books`）：封面网格 + 空态 + 导入中/失败卡 + 拖拽导入。
///
/// 页头（返回 / 书架 / 导入 / 刷新）在 `BrowsePanelView.panelHeader`，这里只管内容区。
/// 视觉基准见 `Books/BookUIPrototype.swift`（已确认的 SwiftUI 原型）。
struct BookshelfView: View {
    @ObservedObject var viewModel: BookViewModel

    @State private var isDropTargeted = false
    @State private var pendingDelete: BookMeta?

    private let columns = [GridItem(.adaptive(minimum: 132), spacing: 14)]

    private var isEmpty: Bool {
        viewModel.books.isEmpty && viewModel.jobs.isEmpty && viewModel.failures.isEmpty
    }

    var body: some View {
        Group {
            if viewModel.loadError != nil {
                errorState
            } else if isEmpty {
                emptyState
            } else {
                grid
            }
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
        .overlay {
            if isDropTargeted {
                RoundedRectangle(cornerRadius: 10, style: .continuous)
                    .strokeBorder(Color("appAccent").opacity(0.5),
                                  style: StrokeStyle(lineWidth: 1.5, dash: [5, 4]))
                    .padding(10)
                    .allowsHitTesting(false)
            }
        }
        .onDrop(of: [.fileURL], isTargeted: $isDropTargeted, perform: handleDrop)
        .task { await viewModel.load() }
        .confirmationDialog(
            pendingDelete.map { "删除《\($0.title)》" } ?? "删除",
            isPresented: Binding(
                get: { pendingDelete != nil },
                set: { if !$0 { pendingDelete = nil } }
            ),
            titleVisibility: .visible
        ) {
            Button("删除", role: .destructive) {
                guard let book = pendingDelete else { return }
                pendingDelete = nil
                Task { await viewModel.delete(book) }
            }
            Button("取消", role: .cancel) { pendingDelete = nil }
        } message: {
            Text("会删掉这本书的原文件、解包内容与全部划线批注，不能恢复。")
        }
    }

    // MARK: - 空态

    private var errorState: some View {
        VStack(spacing: 10) {
            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 34, weight: .light))
                .foregroundColor(.secondary.opacity(0.5))
            Text("书架读取失败")
                .font(.system(size: 14, weight: .medium))
            Text(viewModel.loadError ?? "")
                .font(.system(size: 11.5))
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)
                .textSelection(.enabled)
            Button("刷新") { Task { await viewModel.load() } }
                .buttonStyle(.bordered)
                .controlSize(.small)
                .padding(.top, 4)
        }
        .padding(24)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "books.vertical")
                .font(.system(size: 34, weight: .light))
                .foregroundColor(.secondary.opacity(0.5))
            Text("还没有书")
                .font(.system(size: 14, weight: .medium))
            Text("支持可重排版的 .epub · 也可以直接把文件拖进来")
                .font(.system(size: 11.5))
                .foregroundColor(.secondary)
                .multilineTextAlignment(.center)
            Button("导入 EPUB") { viewModel.presentImportPanel() }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)
                .padding(.top, 4)
        }
        .padding(24)
    }

    // MARK: - 网格

    private var grid: some View {
        ScrollView {
            LazyVGrid(columns: columns, alignment: .leading, spacing: 18) {
                ForEach(viewModel.jobs) { job in
                    ImportingCard(job: job)
                }
                ForEach(viewModel.failures) { failure in
                    ImportFailedCard(
                        failure: failure,
                        onRetry: { viewModel.retry(failure) },
                        onRemove: { viewModel.dismiss(failure) }
                    )
                }
                ForEach(viewModel.books) { book in
                    BookShelfCard(
                        book: book,
                        coverURL: viewModel.coverURL(for: book),
                        onOpen: { viewModel.openBook = book },
                        onReveal: { viewModel.revealInFinder(book) },
                        onDelete: { pendingDelete = book }
                    )
                }
            }
            .padding(.horizontal, 16)
            .padding(.vertical, 16)
        }
        .overlay(alignment: .bottom) {
            // 读不出来的书不显示成书，但也别静默消失（详细原因在日志）
            if viewModel.brokenBookCount > 0 {
                Text("有 \(viewModel.brokenBookCount) 本书的元数据读不出来，已跳过")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .padding(.horizontal, 10)
                    .padding(.vertical, 6)
                    .background(.thinMaterial, in: Capsule())
                    .padding(.bottom, 12)
            }
        }
    }

    // MARK: - 拖拽导入

    /// 只接文件 URL；非 .epub 由 `importFiles` 记日志忽略（不做静默丢弃）。
    private func handleDrop(_ providers: [NSItemProvider]) -> Bool {
        let fileProviders = providers.filter {
            $0.hasItemConformingToTypeIdentifier(UTType.fileURL.identifier)
        }
        guard !fileProviders.isEmpty else { return false }
        for provider in fileProviders {
            provider.loadItem(forTypeIdentifier: UTType.fileURL.identifier, options: nil) { item, error in
                if let error {
                    LogBuffer.shared.error("书架：读取拖入项失败 \(error)")
                    return
                }
                guard let data = item as? Data,
                      let url = URL(dataRepresentation: data, relativeTo: nil) else {
                    LogBuffer.shared.warning("书架：拖入项不是文件 URL")
                    return
                }
                Task { @MainActor in
                    viewModel.importFiles([url])
                }
            }
        }
        return true
    }
}

// MARK: - 封面

/// 真封面从 `Books/<id>/cover.<ext>` 读；读不到就画个 token 派生的占位（书脊 + 书名）。
/// 图片缓存在 `@State`：body 因 hover 重算时不重复解码（一屏几十本时很贵）。
private struct BookCoverImage: View {
    let title: String
    let url: URL?

    @State private var image: NSImage?

    var body: some View {
        Group {
            if let image {
                Image(nsImage: image)
                    .resizable()
                    .scaledToFill()
            } else {
                placeholder
            }
        }
        .aspectRatio(0.75, contentMode: .fit)
        .clipShape(RoundedRectangle(cornerRadius: 10, style: .continuous))
        .overlay {
            RoundedRectangle(cornerRadius: 10, style: .continuous)
                .strokeBorder(Color.primary.opacity(0.08), lineWidth: 0.5)
        }
        .task(id: url) {
            guard let url else { image = nil; return }
            // 封面解码不进主线程：一屏几十本时逐个同步解码会卡滚动
            let data: Data
            do {
                data = try await Task.detached(priority: .utility) {
                    try Data(contentsOf: url)
                }.value
            } catch {
                LogBuffer.shared.warning("书架封面读取失败（\(url.lastPathComponent)）：\(error)")
                return
            }
            guard let decoded = NSImage(data: data) else {
                LogBuffer.shared.warning("书架封面解码失败：\(url.lastPathComponent)")
                return
            }
            image = decoded
        }
    }

    private var placeholder: some View {
        ZStack(alignment: .leading) {
            Color("assistantText").opacity(0.07)
            HStack(spacing: 0) {
                Rectangle()
                    .fill(Color("appAccent").opacity(0.35))
                    .frame(width: 3)
                Spacer()
            }
            Text(title)
                .font(.system(size: 13, weight: .semibold))
                .foregroundColor(Color("assistantText").opacity(0.85))
                .lineLimit(3)
                .minimumScaleFactor(0.8)
                .multilineTextAlignment(.leading)
                .padding(.horizontal, 10)
                .padding(.leading, 6)
        }
    }
}

// MARK: - 书架卡

private struct BookShelfCard: View {
    let book: BookMeta
    let coverURL: URL?
    let onOpen: () -> Void
    let onReveal: () -> Void
    let onDelete: () -> Void

    @State private var hovered = false

    /// 卡上的状态行：切过节的书显示**当前节对应的目录标题**（"第 2/2 章" 对整本一个 xhtml
    /// 的书恒为 2/2，等于假信息）；普通书仍是章号；无目录只给百分比。
    private var statusText: String {
        guard let progress = book.progress else { return "未读" }
        let percent = "\(Int((book.progressFraction * 100).rounded()))%"
        if book.effectiveSections.count > book.chapterCount,
           let section = book.section(at: progress.sectionIndex ?? 0) {
            let title = section.title.trimmingCharacters(in: .whitespacesAndNewlines)
            return title.isEmpty ? percent : title
        }
        if book.hasChapterNavigation {
            return "第 \(progress.chapterIndex + 1)/\(book.chapterCount) 章"
        }
        return percent
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            BookCoverImage(title: book.title, url: coverURL)
                .overlay(alignment: .topTrailing) {
                    Menu {
                        Button("在 Finder 中显示", action: onReveal)
                        Divider()
                        Button("删除", role: .destructive, action: onDelete)
                    } label: {
                        Image(systemName: "ellipsis")
                            .font(.system(size: 11, weight: .semibold))
                            .foregroundColor(Color("assistantText"))
                            .frame(width: 22, height: 22)
                            .background(.thinMaterial, in: Circle())
                    }
                    .menuStyle(.borderlessButton)
                    .menuIndicator(.hidden)
                    .frame(width: 22, height: 22)
                    .help("更多")
                    .padding(6)
                    .opacity(hovered ? 1 : 0)
                    .allowsHitTesting(hovered)
                }

            Text(book.title)
                .font(.system(size: 12.5, weight: .semibold))
                .lineLimit(2)
                .multilineTextAlignment(.leading)
                .fixedSize(horizontal: false, vertical: true)

            Text(book.author ?? "未知作者")
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
                .lineLimit(1)

            HStack(spacing: 6) {
                GeometryReader { geo in
                    ZStack(alignment: .leading) {
                        Capsule().fill(Color.primary.opacity(0.10))
                        Capsule().fill(Color("appAccent").opacity(0.75))
                            .frame(width: max(2, geo.size.width * book.progressFraction))
                    }
                }
                .frame(height: 2)

                Text(statusText)
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .monospacedDigit()
                    .fixedSize()
            }
            .padding(.top, 1)
        }
        .contentShape(Rectangle())
        .onTapGesture(perform: onOpen)
        .onHover { hovered = $0 }
        .animation(.easeInOut(duration: 0.15), value: hovered)
        .help(book.title)
    }
}

// MARK: - 导入中 / 失败

private struct ImportingCard: View {
    let job: BookViewModel.ImportJob

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            BookCoverImage(title: job.title, url: nil)
                .overlay {
                    ProgressView()
                        .controlSize(.small)
                        .padding(8)
                        .background(.thinMaterial, in: Circle())
                }
            Text(job.title)
                .font(.system(size: 12.5, weight: .semibold))
                .lineLimit(1)
            Text("正在\(job.step.rawValue)…")
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
            Spacer(minLength: 0)
        }
    }
}

private struct ImportFailedCard: View {
    let failure: BookViewModel.ImportFailure
    let onRetry: () -> Void
    let onRemove: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            BookCoverImage(title: failure.title, url: nil)
                .overlay(alignment: .topLeading) {
                    Text(failure.error.badge)
                        .font(.system(size: 10, weight: .semibold))
                        .foregroundColor(Color("chatBackground"))
                        .padding(.horizontal, 7)
                        .padding(.vertical, 3)
                        .background(Color("assistantText").opacity(0.82), in: Capsule())
                        .padding(7)
                }
            Text(failure.title)
                .font(.system(size: 12.5, weight: .semibold))
                .lineLimit(1)
            Text(failure.error.reason)
                .font(.system(size: 10.5))
                .foregroundColor(.secondary)
                .lineLimit(2)
                .fixedSize(horizontal: false, vertical: true)
                .help(failure.error.detail)
            HStack(spacing: 8) {
                Button("重试", action: onRetry)
                    .buttonStyle(.link)
                Button("移除", action: onRemove)
                    .buttonStyle(.link)
            }
            .font(.system(size: 11))
            Spacer(minLength: 0)
        }
    }
}
#endif
