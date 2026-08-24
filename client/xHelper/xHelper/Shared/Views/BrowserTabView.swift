import SwiftUI
import WebKit

struct BrowserTabView: View {
    let webView: WKWebView
    let onSetupErrorBinding: ((_ onError: @escaping (String) -> Void) -> Void)?
    @State private var urlText = ""
    @State private var isLoading = false
    @State private var progress: Double = 0
    @State private var currentURL: URL?
    @State private var canGoBack = false
    @State private var canGoForward = false
    @State private var isHovered = false
    @State private var observations: [NSKeyValueObservation] = []
    @State private var loadError: String?
    @State private var suggestions: [URLSuggestion] = []
    @State private var selectedSuggestion = -1
    @State private var showSuggestions = false
    @State private var suppressSuggestions = false
    @State private var didNavigateSelection = false
    @FocusState private var urlFocused: Bool
    @FocusState private var findFocused: Bool
    @ObservedObject private var findCoordinator = BrowserFindCoordinator.shared
    @ObservedObject private var downloadStore = DownloadHistoryStore.shared
    @ObservedObject private var bookmarkStore = BookmarkStore.shared
    @ObservedObject private var historyStore = HistoryStore.shared
    @ObservedObject private var bookmarksBarState = BookmarksBarState.shared

    /// 地址栏建议条目：历史与收藏合并，物理存储各自在 history.json / bookmarks.json，这里只是内存快照。
    fileprivate struct URLSuggestion: Identifiable, Equatable {
        let id: UUID
        let title: String
        let url: String
        let host: String
        let isBookmark: Bool
        let historyID: UUID?
        let bookmarkID: UUID?
    }

    var body: some View {
        VStack(spacing: 0) {
            toolbar
            if bookmarksBarState.isVisible {
                BookmarksBarView()
                Divider()
            }
            if isLoading && progress > 0 && progress < 1 {
                ProgressView(value: progress)
                    .progressViewStyle(.linear)
                    .tint(Color("appAccent"))
                    .frame(height: 2)
                    .padding(.horizontal, 8)
            }
            Divider()
            if findCoordinator.isPresented {
                findBar
                Divider()
            }
            // G2：地址栏下方统一放"确认条（状态 C）+ 活动条（A/B）"，两者同区、位置一致
            BrowserAgentConfirmBar()
            BrowserAgentActivityBar()
            if downloadStore.records.contains(where: { $0.state == .downloading }) {
                downloadsBar
                Divider()
            }
            if let error = loadError {
                BrowserErrorPageView(
                    url: urlText.isEmpty ? (webView.url?.absoluteString ?? "") : urlText,
                    error: error,
                    onRetry: { webView.reload() }
                )
                .frame(maxWidth: .infinity, maxHeight: .infinity)
            } else {
                BrowserWebView(webView: webView)
                    .frame(maxWidth: .infinity, maxHeight: .infinity)
                    .clipped()
            }
        }
        .onAppear {
            // 重置浏览器字段焦点标志（防旧 tab 残留；FocusState 初始 false 不触发 onChange）
            BrowserFocus.isUrlFieldFocused = false
            BrowserFocus.isFindFieldFocused = false
            let initial = webView.url?.absoluteString ?? ""
            urlText = initial == "about:blank" ? "" : initial
            currentURL = webView.url
            isLoading = webView.isLoading
            canGoBack = webView.canGoBack
            canGoForward = webView.canGoForward
            onSetupErrorBinding?({ [weak webView] msg in
                // 确保 error 只更新给当前 webView 对应的 tab
                loadError = msg
            })
            startObserving()
            findCoordinator.attach(webView)
            // 新标签页自动聚焦地址栏
            if webView.url == nil || webView.url?.absoluteString == "about:blank" {
                DispatchQueue.main.async { urlFocused = true }
            }
        }
        .onChange(of: urlFocused) { _, focused in
            BrowserFocus.isUrlFieldFocused = focused
            if !focused { hideSuggestions() }
        }
        .onChange(of: urlText) { _, new in
            updateSuggestions(for: new)
        }
        .onChange(of: findFocused) { _, focused in
            BrowserFocus.isFindFieldFocused = focused
        }
        .onChange(of: findCoordinator.isPresented) { _, presented in
            if presented { findFocused = true }
        }
        .onChange(of: isLoading) { _, loading in
            if loading { loadError = nil }
        }
        .onDisappear {
            BrowserFocus.isUrlFieldFocused = false
            BrowserFocus.isFindFieldFocused = false
            for obs in observations { obs.invalidate() }
            observations = []
        }
    }

    private var toolbar: some View {
        VStack(spacing: 0) {
            addressBarRow
            if showSuggestions && !suggestions.isEmpty {
                suggestionList
            }
        }
        // 显式直角 Rectangle：隔离外层卡片 clipShape 圆角对材质的渲染影响（地址栏保持直角）
        .background(Rectangle().fill(.ultraThinMaterial))
    }

    private var addressBarRow: some View {
        HStack(spacing: 6) {
            // 后退 / 前进
            Button {
                webView.goBack()
            } label: {
                Image(systemName: "chevron.left")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.plain)
            .disabled(!canGoBack)
            .opacity(canGoBack ? 1 : 0.3)
            .help("后退")

            Button {
                webView.goForward()
            } label: {
                Image(systemName: "chevron.right")
                    .font(.system(size: 12, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 22, height: 22)
            }
            .buttonStyle(.plain)
            .disabled(!canGoForward)
            .opacity(canGoForward ? 1 : 0.3)
            .help("前进")

            // 加载中显示停止，否则刷新（放在 < > 右边）
            Button {
                if isLoading {
                    webView.stopLoading()
                } else {
                    webView.reload()
                }
            } label: {
                Image(systemName: isLoading ? "xmark" : "arrow.clockwise")
                    .font(.system(size: 12, weight: .medium))
                    .foregroundColor(.secondary)
            }
            .buttonStyle(.plain)
            .help(isLoading ? "停止" : "重新加载")

            securityIndicator

            TextField("输入网址或搜索", text: $urlText)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .focused($urlFocused)
                .onSubmit { navigateToURL() }
                .onKeyPress(.downArrow, phases: .down) { _ in moveSuggestionSelection(1) }
                .onKeyPress(.upArrow, phases: .down) { _ in moveSuggestionSelection(-1) }
                .onKeyPress(.return, phases: .down) { keyPress in
                    #if os(macOS)
                    // 输入法组词中回车是确认拼音，放行给输入法
                    if (NSApp.keyWindow?.firstResponder?.value(forKey: "hasMarkedText") as? Bool) == true {
                        return .ignored
                    }
                    #endif
                    return showSuggestions && didNavigateSelection ? acceptSuggestionFromKeyboard() : .ignored
                }
                .onKeyPress(.tab, phases: .down) { _ in acceptSuggestionFromKeyboard() }
                .onKeyPress(.escape, phases: .down) { _ in handleEscapeKey() }

            mediaPanelButton
            bookmarkButton
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 6)
    }

    /// 本页资源（媒体下载面板）：捕获当前页 webView 打开侧栏「本页资源」tab。
    /// 仅 http(s) 页面可用；每次点击都重新绑定来源页并重扫。
    private var mediaPanelButton: some View {
        Button {
            BrowserToolProvider.shared.rightSidebar?.openMediaPanel(sourceWebView: webView)
        } label: {
            Image(systemName: "photo.on.rectangle.angled")
                .font(.system(size: 12))
                .foregroundColor(.secondary)
                .frame(width: 22, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(currentPageURL() == nil)
        .opacity(currentPageURL() == nil ? 0.3 : 1)
        .help("本页媒体资源（视频/图片下载）")
    }

    // MARK: - 地址栏建议（历史 + 收藏，前缀匹配，兼容有无协议头）

    /// 建议列表：favicon + 标题 + URL（高亮匹配前缀），悬停显示 X 可单独删除。
    private var suggestionList: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(spacing: 0) {
                    ForEach(Array(suggestions.enumerated()), id: \.element.id) { index, s in
                        SuggestionRow(
                            suggestion: s,
                            isSelected: index == selectedSuggestion,
                            query: urlText,
                            onSelect: { selectSuggestion(at: index) },
                            onDelete: { deleteSuggestion(s) }
                        )
                        .id(s.id)
                    }
                }
                .padding(4)
            }
            .onChange(of: selectedSuggestion) { _, new in
                guard !suggestions.isEmpty, new < suggestions.count else { return }
                withAnimation(.snappy) { proxy.scrollTo(suggestions[new].id, anchor: .center) }
            }
        }
        .frame(maxHeight: 260)
        // 直角：与地址栏/收藏夹栏保持一致的方形风格
        .background(Rectangle().fill(.regularMaterial))
        .overlay(Rectangle().stroke(Color.primary.opacity(0.08), lineWidth: 0.5))
        .shadow(color: .black.opacity(0.15), radius: 8, y: 3)
        .padding(.horizontal, 8)
        .padding(.bottom, 6)
    }

    /// 逐条删除：历史条目删 history.json，收藏删 bookmarks.json（建议无独立存储，删完即时重算）。
    private func deleteSuggestion(_ s: URLSuggestion) {
        if let hid = s.historyID { historyStore.remove(id: hid) }
        if let bid = s.bookmarkID { bookmarkStore.remove(id: bid) }
        updateSuggestions(for: urlText)
    }

    private func selectSuggestion(at index: Int) {
        guard suggestions.indices.contains(index) else { return }
        urlText = suggestions[index].url
        // 抑制下一次 urlText 变化的建议重算（否则填入完整 URL 后列表又弹出来）
        suppressSuggestions = true
        hideSuggestions()
        navigateToURL()
    }

    private func moveSuggestionSelection(_ delta: Int) -> KeyPress.Result {
        guard showSuggestions, !suggestions.isEmpty else { return .ignored }
        if selectedSuggestion < 0 {
            selectedSuggestion = delta > 0 ? 0 : suggestions.count - 1
        } else {
            selectedSuggestion = (selectedSuggestion + delta + suggestions.count) % suggestions.count
        }
        didNavigateSelection = true
        return .handled
    }

    private func acceptSuggestionFromKeyboard() -> KeyPress.Result {
        guard showSuggestions, !suggestions.isEmpty else { return .ignored }
        selectSuggestion(at: selectedSuggestion)
        return .handled
    }

    private func handleEscapeKey() -> KeyPress.Result {
        guard showSuggestions else { return .ignored }
        hideSuggestions()
        return .handled
    }

    private func hideSuggestions() {
        showSuggestions = false
        suggestions = []
    }

    /// 输入变化时重算建议：<2 字符或失焦不显示；历史优先，收藏补位，按归一化 URL 去重。
    private func updateSuggestions(for text: String) {
        let suppressed = suppressSuggestions
        suppressSuggestions = false
        didNavigateSelection = false
        let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
        guard trimmed.count >= 2, urlFocused, !suppressed else {
            hideSuggestions()
            return
        }
        var seen: Set<String> = []
        var merged: [URLSuggestion] = []
        func appendHistory(_ e: HistoryStore.HistoryEntry) {
            let key = HistoryStore.normalizeURL(e.url)
            guard !seen.contains(key) else { return }
            seen.insert(key)
            merged.append(URLSuggestion(id: e.id, title: e.title, url: e.url, host: e.host,
                                        isBookmark: false, historyID: e.id, bookmarkID: nil))
        }
        func appendBookmark(_ b: BookmarkStore.Bookmark) {
            let key = HistoryStore.normalizeURL(b.url)
            guard !seen.contains(key) else { return }
            seen.insert(key)
            merged.append(URLSuggestion(id: b.id, title: b.title, url: b.url, host: b.host,
                                        isBookmark: true, historyID: nil, bookmarkID: b.id))
        }
        for e in historyStore.suggest(query: text) { appendHistory(e) }
        for b in bookmarkStore.suggest(query: text, maxResults: 6) { appendBookmark(b) }
        suggestions = merged
        showSuggestions = !merged.isEmpty
        if selectedSuggestion >= merged.count { selectedSuggestion = -1 }
    }

    /// 收藏当前页（原顶栏收藏按钮移至地址栏最右）
    private var bookmarkButton: some View {
        let isBookmarked = currentPageBookmarked()
        return Button {
            guard let url = currentURL ?? webView.url,
                  let scheme = url.scheme?.lowercased(),
                  scheme == "http" || scheme == "https" else { return }
            BookmarkStore.shared.toggle(title: webView.title ?? url.host ?? "", url: url.absoluteString)
        } label: {
            Image(systemName: isBookmarked ? "star.fill" : "star")
                .font(.system(size: 12))
                .foregroundColor(isBookmarked ? Color("appAccent") : .secondary)
                .frame(width: 22, height: 22)
                .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
        .disabled(currentPageURL() == nil)
        .opacity(currentPageURL() == nil ? 0.3 : 1)
        .help(isBookmarked ? "取消收藏当前页" : "收藏当前页")
    }

    private func currentPageBookmarked() -> Bool {
        guard let urlStr = currentPageURL() else { return false }
        return BookmarkStore.shared.isBookmarked(url: urlStr)
    }

    private func currentPageURL() -> String? {
        guard let url = (currentURL ?? webView.url)?.absoluteString,
              let scheme = URL(string: url)?.scheme?.lowercased(),
              scheme == "http" || scheme == "https" else { return nil }
        return url
    }

    private func navigateToURL() {
        let raw = urlText.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !raw.isEmpty else { return }
        // 非 URL → 默认搜索引擎兜底（含空格/无域名/无协议等）
        let looksLikeURL = raw.contains("://") || (raw.contains(".") && !raw.contains(" "))
        let target: URL
        if looksLikeURL {
            let s = raw.hasPrefix("http") ? raw : "https://\(raw)"
            guard let u = URL(string: s) else { return }
            target = u
        } else {
            let q = raw.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? ""
            guard let u = URL(string: "https://cn.bing.com/search?q=\(q)") else { return }
            target = u
        }
        webView.load(URLRequest(url: target, timeoutInterval: 15))
        loadError = nil
    }
    /// 地址栏安全指示：https=锁 / http=警示 / 其他（空白页等）=globe
    @ViewBuilder
    private var securityIndicator: some View {
        switch currentURL?.scheme?.lowercased() {
        case "https":
            Image(systemName: "lock.fill")
                .font(.system(size: 10))
                .foregroundColor(.secondary)
                .help("HTTPS 加密连接")
        case "http":
            Image(systemName: "exclamationmark.triangle.fill")
                .font(.system(size: 11))
                .foregroundColor(.orange)
                .help("明文 HTTP，内容可能被篡改")
        default:
            Image(systemName: "globe")
                .font(.system(size: 12))
                .foregroundColor(.secondary)
        }
    }

    /// 进行中下载的紧凑进度行（每个带取消按钮）
    /// 进行中下载的紧凑进度行（速度/剩余/已用 + 取消按钮），每秒刷新
    private var downloadsBar: some View {
        TimelineView(.periodic(from: .now, by: 1)) { _ in
            VStack(spacing: 4) {
                ForEach(downloadStore.records.filter { $0.state == .downloading }) { item in
                    HStack(spacing: 8) {
                        Image(systemName: "arrow.down.circle")
                            .font(.system(size: 10, weight: .semibold))
                            .foregroundColor(.secondary)
                        VStack(alignment: .leading, spacing: 1) {
                            Text(item.filename)
                                .font(.system(size: 10))
                                .lineLimit(1)
                                .truncationMode(.middle)
                            Text(downloadStats(item))
                                .font(.system(size: 9))
                                .foregroundColor(.secondary)
                                .lineLimit(1)
                        }
                        Spacer(minLength: 8)
                        ProgressView(value: item.progress)
                            .frame(width: 60)
                        Button {
                            downloadStore.cancel(id: item.id)
                        } label: {
                            Image(systemName: "xmark")
                                .font(.system(size: 9, weight: .semibold))
                                .foregroundColor(.secondary)
                                .frame(width: 18, height: 18)
                                .contentShape(Rectangle())
                        }
                        .buttonStyle(.plain)
                        .help("取消下载")
                    }
                    .padding(.horizontal, 10)
                }
            }
            .padding(.vertical, 5)
            .background(.ultraThinMaterial)
        }
    }

    /// 下载统计行：速度 · 剩余 · 已用（totalBytes 未知时退化为百分比 · 已用）
    private func downloadStats(_ record: DownloadHistoryStore.DownloadRecord) -> String {
        var parts: [String] = []
        if let total = record.totalBytes, total > 0 {
            if record.speedBytesPerSec > 0 {
                let remaining = Double(max(total - record.completedBytes, 0)) / record.speedBytesPerSec
                parts.append("\(DownloadFormat.speedString(record.speedBytesPerSec)) · 剩余 \(DownloadFormat.durationString(remaining))")
            } else {
                parts.append("\(DownloadFormat.byteString(record.completedBytes)) / \(DownloadFormat.byteString(total))")
            }
        } else {
            parts.append("\(Int(record.progress * 100))%")
        }
        parts.append("已用 \(DownloadFormat.durationString(Date().timeIntervalSince(record.startedAt)))")
        return parts.joined(separator: " · ")
    }

    // MARK: - 页内查找（Cmd+F 唤出，Esc 关闭）

    private var findBar: some View {
        HStack(spacing: 6) {
            Image(systemName: "magnifyingglass")
                .font(.system(size: 11))
                .foregroundColor(.secondary)

            TextField("在页面中查找", text: $findCoordinator.query)
                .textFieldStyle(.plain)
                .font(.system(size: 12))
                .focused($findFocused)
                .onSubmit { findCoordinator.next() }
                .onChange(of: findCoordinator.query) { _, _ in findCoordinator.queryChanged() }

            if !findCoordinator.query.isEmpty {
                Image(systemName: findCoordinator.matchFound ? "checkmark" : "xmark")
                    .font(.system(size: 10, weight: .bold))
                    .foregroundColor(findCoordinator.matchFound ? .green : .red)
            }

            Button { findCoordinator.previous() } label: {
                Image(systemName: "chevron.up")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("上一个匹配（Shift+⌘G）")

            Button { findCoordinator.next() } label: {
                Image(systemName: "chevron.down")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("下一个匹配（⌘G）")

            Button { findCoordinator.close() } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 10, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 20, height: 20)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .help("关闭查找（Esc）")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .background(.ultraThinMaterial)
    }

    private func startObserving() {
        observations = [
            webView.observe(\.url, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    guard let url = change.newValue as? URL?,
                          let u = url else { return }
                    // currentURL 无条件跟随（媒体按钮/收藏等内部判断依赖它）。
                    // 此前整段被 !urlFocused 拦截：新标签页 onAppear 自动聚焦地址栏，
                    // 首次导航的 URL 更新被吞 → currentURL 永 nil → 按钮/收藏永远不可用。
                    currentURL = u
                    // 地址栏文本仅在未聚焦时同步（聚焦 = 用户可能正在输入，别覆盖）
                    if !urlFocused {
                        // 新标签页默认页（about:blank）不显示在地址栏
                        urlText = u.absoluteString == "about:blank" ? "" : u.absoluteString
                    }
                }
            },
            webView.observe(\.isLoading, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { isLoading = v }
                }
            },
            webView.observe(\.estimatedProgress, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { progress = v }
                }
            },
            webView.observe(\.canGoBack, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { canGoBack = v }
                }
            },
            webView.observe(\.canGoForward, options: [.new]) { _, change in
                DispatchQueue.main.async {
                    if let v = change.newValue { canGoForward = v }
                }
            },
        ]
    }
}

#if os(macOS)
private struct BrowserWebView: NSViewRepresentable {
    let webView: WKWebView

    func makeNSView(context: Context) -> WKWebView {
        return webView
    }

    func updateNSView(_ nsView: WKWebView, context: Context) {
        if nsView !== webView {
            pipLog("webview UPDATE (stale!): attached=\(ObjectIdentifier(nsView)) want=\(ObjectIdentifier(webView))")
        }
    }
}
#else
private struct BrowserWebView: UIViewRepresentable {
    let webView: WKWebView

    func makeUIView(context: Context) -> WKWebView {
        return webView
    }

    func updateUIView(_ uiView: WKWebView, context: Context) {
        if uiView !== webView {
            pipLog("webview UPDATE (stale!): attached=\(ObjectIdentifier(uiView)) want=\(ObjectIdentifier(webView))")
        }
    }
}
#endif

struct BrowserErrorPageView: View {
    let url: String
    let error: String
    let onRetry: () -> Void
    @State private var isHovered = false
    var body: some View {
        VStack(spacing: 16) {
            Spacer()

            Image(systemName: "exclamationmark.triangle")
                .font(.system(size: 32))
                .foregroundColor(.secondary.opacity(0.5))

            VStack(spacing: 4) {
                Text("无法打开网页")
                    .font(.system(size: 14, weight: .semibold))
                Text(url)
                    .font(.system(size: 12))
                    .foregroundColor(.secondary)
                    .lineLimit(2)
                Text(error)
                    .font(.system(size: 11))
                    .foregroundColor(.secondary)
            }

            Button("重试") { onRetry() }
                .buttonStyle(.borderedProminent)
                .controlSize(.small)

            Spacer()
        }
        .frame(maxWidth: .infinity, maxHeight: .infinity)
    }
}

/// 地址栏建议行：favicon + 标题（收藏带星）+ URL（高亮匹配前缀），悬停出现 X 可单独删除。
private struct SuggestionRow: View {
    let suggestion: BrowserTabView.URLSuggestion
    let isSelected: Bool
    let query: String
    let onSelect: () -> Void
    let onDelete: () -> Void
    @State private var isHovered = false
    @ObservedObject private var faviconStore = FaviconStore.shared

    var body: some View {
        HStack(spacing: 8) {
            favicon
            VStack(alignment: .leading, spacing: 1) {
                HStack(spacing: 4) {
                    Text(suggestion.title)
                        .font(.system(size: 12))
                        .foregroundColor(.primary)
                        .lineLimit(1)
                    if suggestion.isBookmark {
                        Image(systemName: "star.fill")
                            .font(.system(size: 8))
                            .foregroundColor(.yellow)
                    }
                }
                highlightURL
                    .font(.system(size: 10))
                    .foregroundColor(.secondary)
                    .lineLimit(1)
            }
            Spacer(minLength: 4)
            Button {
                onDelete()
            } label: {
                Image(systemName: "xmark")
                    .font(.system(size: 9, weight: .semibold))
                    .foregroundColor(.secondary)
                    .frame(width: 16, height: 16)
                    .contentShape(Rectangle())
            }
            .buttonStyle(.plain)
            .opacity(isHovered || isSelected ? 1 : 0)
            .help(suggestion.isBookmark ? "删除该收藏" : "从历史记录删除")
        }
        .padding(.horizontal, 8)
        .padding(.vertical, 5)
        .contentShape(Rectangle())
        .background(
            isSelected ? Color.accentColor.opacity(0.15) : Color.clear,
            in: RoundedRectangle(cornerRadius: 5)
        )
        .onHover { isHovered = $0 }
        .onTapGesture { onSelect() }
    }

    @ViewBuilder
    private var favicon: some View {
        if let img = faviconStore.image(forHost: suggestion.host) {
            #if os(macOS)
            Image(nsImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 14, height: 14)
            #else
            Image(uiImage: img)
                .resizable()
                .interpolation(.medium)
                .frame(width: 14, height: 14)
            #endif
        } else {
            Image(systemName: "globe")
                .font(.system(size: 11))
                .foregroundColor(.secondary)
        }
    }

    /// URL 显示去掉协议头，命中前缀加粗高亮（选中行高亮更明显）。
    private var highlightURL: Text {
        let display = HistoryStore.normalizeURL(suggestion.url)
        let q = HistoryStore.normalizeURL(query)
        guard !q.isEmpty, let range = display.range(of: q) else {
            return Text(display)
        }
        let before = String(display[..<range.lowerBound])
        let match = String(display[range])
        let after = String(display[range.upperBound...])
        if isSelected {
            return Text(before) + Text(match).fontWeight(.semibold).foregroundColor(.primary) + Text(after)
        }
        return Text(before) + Text(match).fontWeight(.semibold) + Text(after)
    }
}
