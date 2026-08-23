import Foundation
import SwiftUI
import WebKit

/// 「本页资源」面板：手动扫描当前页视频/图片，勾选后顺序下载（间隔 ~1s）。
/// 视频为主（小红书/抖音站点增强提取），图片次要（通用 DOM 扫描，截图兜底）。
/// 下载走 BrowserToolProvider.downloadMedia（复用 UA/Referer/Cookie/魔数校验/下载历史全链路），
/// 进度/状态直接从 DownloadHistoryStore 按 URL 匹配读取，本视图不维护下载状态机。
struct MediaPickerPanelView: View {
    /// 来源页 webView（打开面板那一刻捕获；面板激活后该 tab 可能脱离视图层级，但实例存活、JS 可执行）
    let sourceWebView: WKWebView

    @State private var result: MediaExtractor.ScanResult?
    @State private var scanning = false
    @State private var scanFailed = false
    @State private var selected = Set<String>()
    @State private var showImages = false
    @State private var downloading = false
    /// 水合竞态自动补扫只做一轮（防死循环）
    @State private var didAutoRetry = false
    @ObservedObject private var dlStore = DownloadHistoryStore.shared

    /// 勾选池始终含全部条目（图片区折叠时已勾选的图片仍会参与下载）
    private var items: [MediaExtractor.Item] {
        (result?.videos ?? []) + (result?.images ?? [])
    }

    var body: some View {
        VStack(spacing: 0) {
            header
            Divider()
            content
            Divider()
            footer
        }
        .task { await rescan() }
    }

    // MARK: - Header / Footer

    private var header: some View {
        HStack(spacing: 8) {
            VStack(alignment: .leading, spacing: 2) {
                Text("本页资源")
                    .font(.system(size: 13, weight: .semibold))
                if let result {
                    Text(hostLabel(result.pageURL))
                        .font(.system(size: 11))
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                }
            }
            Spacer()
            if downloading {
                Text("下载中…")
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
            }
            Button {
                Task { await rescan() }
            } label: {
                Label("重新扫描", systemImage: "arrow.clockwise")
                    .font(.system(size: 11))
            }
            .buttonStyle(.plain)
            .disabled(scanning || downloading)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    private var footer: some View {
        HStack(spacing: 8) {
            Text("已选 \(selected.count) 项")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
            Spacer()
            Text("~/Downloads/xhanHelper")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
            Button {
                Task { await downloadSelected() }
            } label: {
                Text(downloading ? "下载中…" : "下载选中(\(selected.count))")
                    .font(.system(size: 12, weight: .medium))
                    .padding(.horizontal, 12)
                    .padding(.vertical, 5)
            }
            .buttonStyle(.borderedProminent)
            .disabled(selected.isEmpty || downloading)
        }
        .padding(.horizontal, 12)
        .padding(.vertical, 8)
    }

    // MARK: - Content

    @ViewBuilder
    private var content: some View {
        ScrollView {
            LazyVStack(alignment: .leading, spacing: 12) {
                if scanning {
                    HStack { Spacer(); ProgressView().controlSize(.small); Text("正在扫描…").font(.system(size: 12)).foregroundColor(.secondary); Spacer() }
                        .padding(.top, 24)
                } else if scanFailed {
                    Text("扫描失败：页面可能尚未加载完成")
                        .font(.system(size: 12))
                        .foregroundColor(.secondary)
                        .frame(maxWidth: .infinity)
                        .padding(.top, 24)
                } else if let result {
                    if result.isEmpty {
                        Text("未发现可下载的媒体资源。视频类页面若需先播放才有直链，可播放后点「重新扫描」")
                            .font(.system(size: 12))
                            .foregroundColor(.secondary)
                            .frame(maxWidth: .infinity)
                            .padding(.top, 24)
                    } else {
                        section(title: "视频 (\(result.videos.count))", items: result.videos)
                        imageSection(result.images)
                    }
                }
            }
            .padding(12)
        }
    }

    /// 图片区：默认折叠（次要资源，截图兜底），点标题展开
    @ViewBuilder
    private func imageSection(_ images: [MediaExtractor.Item]) -> some View {
        if !images.isEmpty {
            VStack(alignment: .leading, spacing: 8) {
                Button {
                    showImages.toggle()
                } label: {
                    Label("\(showImages ? "收起" : "展开")图片 (\(images.count))",
                          systemImage: showImages ? "chevron.down" : "chevron.right")
                        .font(.system(size: 11, weight: .medium))
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
                if showImages {
                    section(title: "", items: images)
                }
            }
        }
    }

    private func section(title: String, items: [MediaExtractor.Item]) -> some View {
        VStack(alignment: .leading, spacing: 4) {
            if !title.isEmpty {
                Text(title).font(.system(size: 11, weight: .semibold)).foregroundColor(.secondary)
            }
            ForEach(items) { item in
                MediaRow(
                    item: item,
                    isSelected: selected.contains(item.id),
                    record: dlStore.records.first(where: { $0.url == item.url.absoluteString }),
                    onToggle: { toggle(item) }
                )
            }
        }
    }

    private func toggle(_ item: MediaExtractor.Item) {
        if selected.contains(item.id) {
            selected.remove(item.id)
        } else {
            selected.insert(item.id)
        }
    }

    // MARK: - Actions

    /// 手动入口：重置自动补扫标记后扫描
    private func rescan() async {
        didAutoRetry = false
        await scanOnce()
    }

    private func scanOnce() async {
        scanning = true
        scanFailed = false
        selected.removeAll()
        defer { scanning = false }
        guard let r = await MediaExtractor.scan(webView: sourceWebView) else {
            scanFailed = true
            result = nil
            return
        }
        result = r
        // 水合竞态兜底：视频为空且是已知站点时，静默等 1.5s 补扫一轮（SPA 首扫可能跑在 state 就绪之前）
        if r.videos.isEmpty, !didAutoRetry, isKnownMediaSite(r.pageURL) {
            didAutoRetry = true
            try? await Task.sleep(for: .seconds(1.5))
            if !downloading { await scanOnce() }
        }
    }

    private func isKnownMediaSite(_ urlStr: String) -> Bool {
        guard let host = URL(string: urlStr)?.host?.lowercased() else { return false }
        return host.contains("xiaohongshu.") || host.contains("douyin.")
    }

    private func downloadSelected() async {
        let targets = items.filter { selected.contains($0.id) }
        downloading = true
        defer { downloading = false }
        for (i, item) in targets.enumerated() {
            // 拟人间隔：逐条间隔 ~1s，降低风控触发概率
            if i > 0 { try? await Task.sleep(for: .seconds(1)) }
            _ = await BrowserToolProvider.shared.downloadMedia(
                url: item.url,
                preferredName: suggestedName(item),
                sourceWebView: sourceWebView
            )
        }
    }

    /// 有上下文标题用「标题前 24 字符.扩展名」，否则交给响应 Content-Disposition 建议
    private func suggestedName(_ item: MediaExtractor.Item) -> String? {
        let base = item.title.trimmingCharacters(in: .whitespacesAndNewlines)
        guard base.count >= 2 else { return nil }
        let cleaned = base.replacingOccurrences(of: "/", with: "-")
        let trimmed = String(cleaned.prefix(24)).trimmingCharacters(in: .whitespacesAndNewlines)
        guard !trimmed.isEmpty else { return nil }
        let ext = item.kind == .video ? "mp4" : "jpg"
        return "\(trimmed).\(ext)"
    }

    private func hostLabel(_ urlStr: String) -> String {
        URL(string: urlStr)?.host ?? urlStr
    }
}

// MARK: - Row

private struct MediaRow: View {
    let item: MediaExtractor.Item
    let isSelected: Bool
    let record: DownloadHistoryStore.DownloadRecord?
    let onToggle: () -> Void

    var body: some View {
        HStack(spacing: 10) {
            thumbnail
            VStack(alignment: .leading, spacing: 3) {
                Text(metaText)
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
                if let title = displayTitle, !title.isEmpty {
                    Text(title)
                        .font(.system(size: 12))
                        .lineLimit(1)
                }
                statusView
            }
            Spacer(minLength: 0)
            Image(systemName: isSelected ? "checkmark.circle.fill" : "circle")
                .font(.system(size: 15))
                .foregroundColor(isSelected ? Color("appAccent") : .secondary.opacity(0.5))
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
        .background(
            RoundedRectangle(cornerRadius: 6)
                .fill(isSelected ? Color("appAccent").opacity(0.08) : Color.clear)
        )
        .contentShape(Rectangle())
        .onTapGesture {
            // 已完成/失败仍允许勾选重试；下载中禁止改动
            if record?.state != .downloading { onToggle() }
        }
    }

    private var thumbnail: some View {
        Group {
            if let url = item.poster ?? (item.kind == .image ? item.url : nil) {
                AsyncImage(url: url) { phase in
                    switch phase {
                    case .success(let img): img.resizable().scaledToFill()
                    default: placeholderIcon
                    }
                }
            } else {
                placeholderIcon
            }
        }
        .frame(width: 56, height: 56)
        .clipShape(RoundedRectangle(cornerRadius: 6))
        .overlay(RoundedRectangle(cornerRadius: 6).stroke(Color.primary.opacity(0.08)))
    }

    private var placeholderIcon: some View {
        Rectangle().fill(Color.secondary.opacity(0.12))
            .overlay(Image(systemName: item.kind == .video ? "video" : "photo").font(.system(size: 16)).foregroundColor(.secondary))
    }

    private var displayTitle: String? {
        item.title.isEmpty ? nil : item.title
    }

    private var metaText: String {
        var parts: [String] = []
        if item.kind == .video && item.playing { parts.append("▶ 播放中") }
        if let d = item.duration {
            parts.append(String(format: "%02d:%02d", d / 60, d % 60))
        }
        if let w = item.width, let h = item.height {
            parts.append("\(w)×\(h)")
        }
        return parts.isEmpty ? (item.kind == .video ? "视频" : "图片") : parts.joined(separator: " · ")
    }

    @ViewBuilder
    private var statusView: some View {
        switch record?.state {
        case .downloading:
            HStack(spacing: 6) {
                ProgressView(value: record?.progress ?? 0)
                    .progressViewStyle(.linear)
                    .tint(Color("appAccent"))
                    .frame(width: 90)
                Text("\(Int((record?.progress ?? 0) * 100))%")
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
            }
        case .done:
            Label(record?.filename ?? "已完成", systemImage: "checkmark.circle.fill")
                .font(.system(size: 10))
                .foregroundColor(.green)
                .lineLimit(1)
        case .failed:
            Label("下载失败，可重新勾选重试", systemImage: "exclamationmark.triangle.fill")
                .font(.system(size: 10))
                .foregroundColor(.red)
        case .cancelled:
            Label("已取消", systemImage: "minus.circle")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
        case nil:
            EmptyView()
        }
    }
}
