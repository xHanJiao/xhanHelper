import SwiftUI
#if os(macOS)
import AppKit

/// 下载记录页（贴近 chrome://downloads）：统一展示用户点击下载 + agent 下载的历史与实时进度。
struct DownloadHistoryPanelView: View {
    @ObservedObject private var store = DownloadHistoryStore.shared

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            if store.records.isEmpty {
                emptyState
            } else {
                // TimelineView 每秒刷新：进行中行显示已用时间/速度/剩余
                TimelineView(.periodic(from: .now, by: 1)) { _ in
                    List {
                        ForEach(store.records) { record in
                            recordRow(record)
                        }
                    }
                    .listStyle(.plain)
                }
            }
        }
    }

    // MARK: - Header

    private var header: some View {
        HStack(spacing: 8) {
            Text("下载")
                .font(.system(size: 13, weight: .semibold))
            Spacer()
            Button {
                openDownloadsFolder()
            } label: {
                Image(systemName: "folder")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("打开下载文件夹")
            Button {
                store.clearCompleted()
            } label: {
                Image(systemName: "trash")
                    .font(.system(size: 11))
                    .frame(width: 22, height: 22)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("清空已完成记录")
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var emptyState: some View {
        VStack(spacing: 10) {
            Image(systemName: "arrow.down.circle")
                .font(.system(size: 30))
                .foregroundColor(.secondary.opacity(0.5))
            Text("暂无下载记录")
                .font(.system(size: 12))
                .foregroundColor(.secondary)
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }

    // MARK: - Row

    private func recordRow(_ record: DownloadHistoryStore.DownloadRecord) -> some View {
        HStack(spacing: 8) {
            Image(systemName: statusIcon(record.state))
                .font(.system(size: 12, weight: .semibold))
                .foregroundColor(statusColor(record.state))
                .frame(width: 18)

            VStack(alignment: .leading, spacing: 2) {
                Text(record.filename)
                    .font(.system(size: 12, weight: .medium))
                    .lineLimit(1)
                    .truncationMode(.middle)
                Text(subtitle(record))
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }

            Spacer(minLength: 8)

            if record.state == .downloading {
                ProgressView(value: record.progress)
                    .frame(width: 80)
                Text("\(Int(record.progress * 100))%")
                    .font(.system(size: 10, design: .monospaced))
                    .foregroundColor(.secondary)
            }

            actions(for: record)
        }
        .padding(.vertical, 5)
    }

    private func subtitle(_ record: DownloadHistoryStore.DownloadRecord) -> String {
        let src = record.url.isEmpty ? "" : record.url
        switch record.state {
        case .downloading:
            var parts: [String] = []
            if let total = record.totalBytes, total > 0 {
                parts.append("\(DownloadFormat.byteString(record.completedBytes)) / \(DownloadFormat.byteString(total))")
                if record.speedBytesPerSec > 0 {
                    let remaining = Double(max(total - record.completedBytes, 0)) / record.speedBytesPerSec
                    parts.append("\(DownloadFormat.speedString(record.speedBytesPerSec)) · 剩余 \(DownloadFormat.durationString(remaining))")
                }
            } else {
                parts.append("\(Int(record.progress * 100))%")
            }
            parts.append("已用 \(DownloadFormat.durationString(Date().timeIntervalSince(record.startedAt)))")
            return parts.joined(separator: " · ")
        case .done:
            let sizeText = record.sizeBytes > 0 ? DownloadFormat.byteString(record.sizeBytes) : ""
            return "\(sizeText) · \(record.destinationPath)"
        case .failed: return "下载失败 · \(src)"
        case .cancelled: return "已取消 · \(src)"
        }
    }

    @ViewBuilder
    private func actions(for record: DownloadHistoryStore.DownloadRecord) -> some View {
        switch record.state {
        case .downloading:
            Button { store.cancel(id: record.id) } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .semibold))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("取消下载")
        case .done:
            Button { openFile(record) } label: {
                Text("打开").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .foregroundColor(.accentColor)
            Button { revealFile(record) } label: {
                Text("在 Finder 中显示").font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            Button { store.remove(id: record.id) } label: {
                Image(systemName: "trash")
                    .font(.system(size: 10))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("移除记录")
        case .failed, .cancelled:
            Button { store.retry(id: record.id) } label: {
                Image(systemName: "arrow.clockwise")
                    .font(.system(size: 10, weight: .semibold))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("重试下载")
            Button { store.remove(id: record.id) } label: {
                Image(systemName: "trash")
                    .font(.system(size: 10))
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .foregroundColor(.secondary)
            .help("移除记录")
        }
    }

    // MARK: - Actions

    private func openFile(_ record: DownloadHistoryStore.DownloadRecord) {
        let url = URL(fileURLWithPath: record.destinationPath)
        guard FileManager.default.fileExists(atPath: url.path) else {
            store.remove(id: record.id)
            return
        }
        NSWorkspace.shared.open(url)
    }

    private func revealFile(_ record: DownloadHistoryStore.DownloadRecord) {
        let url = URL(fileURLWithPath: record.destinationPath)
        guard FileManager.default.fileExists(atPath: url.path) else {
            store.remove(id: record.id)
            return
        }
        NSWorkspace.shared.activateFileViewerSelecting([url])
    }

    private func openDownloadsFolder() {
        let dir = FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Downloads/xhanHelper", isDirectory: true)
        NSWorkspace.shared.open(dir)
    }

    private func statusIcon(_ state: DownloadHistoryStore.State) -> String {
        switch state {
        case .downloading: return "arrow.down.circle"
        case .done: return "checkmark.circle.fill"
        case .failed: return "xmark.circle.fill"
        case .cancelled: return "minus.circle"
        }
    }

    private func statusColor(_ state: DownloadHistoryStore.State) -> Color {
        switch state {
        case .downloading: return .secondary
        case .done: return .green
        case .failed: return .red
        case .cancelled: return .secondary
        }
    }



}
#endif
