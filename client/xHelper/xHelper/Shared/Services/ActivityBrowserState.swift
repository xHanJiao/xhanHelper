import Foundation

/// 浏览事件的状态机（纯逻辑，不依赖 WebKit；AppKit 挂点见 `BrowserToolProvider`）。
///
/// **每个浏览器标签一个实例**，产两类事件：
/// - `browser.nav.started` / `browser.nav.ended`：一次加载的开始与结束（`outcome` 区分
///   成功 / 失败 / 被下一次导航打断），`pair_id` 互指；
/// - `browser.page.dwell`：一次停留（URL 变为该页 → 下一次 URL 变化 / 关标签）的时长，
///   离开时才发。字典里它是辅助值，权威是 nav 配对区间。
///
/// `owner`（nil = 用户标签）决定 `actor`：用户操作记 `user`，助手代开记 `assistant`。
/// 页面标题由调用方（`BrowserToolProvider`）在收事件时传入，落进 `page_title`——它是明细级
/// 读取（`EventDetail.page_title`）唯一的内容文本来源；此前无人写入，该字段恒空。
nonisolated struct ActivityBrowserState {
    private let tabId: String

    private var navPair: String?
    private var navActor: String = "user"

    private var pageURL: String?
    private var pageSince: Date?
    private var pageActor: String = "user"
    /// 助手代开的标签所属会话上下文；用户标签为 nil。
    private var agentContext: AgentActivityContext?

    init(tabId: String) { self.tabId = tabId }

    /// 设置/清空 agent 会话上下文（助手工具执行前由 BrowserToolProvider 盖章）。
    mutating func setAgentContext(_ ctx: AgentActivityContext?) { agentContext = ctx }

    /// URL 变化（KVO 提交 = provisional；didFinish = finished）。
    /// 返回要发的事件，顺序 = 收旧页 dwell → 收被打断的 nav → 起新 nav → （同一 URL 则）收 nav。
    /// `pageTitle` 是收事件那一刻的页面标题：provisional 时是**旧页**标题（正好配要收的旧页），
    /// didFinish 时是新页标题。
    mutating func urlChanged(url: String, host: String, owner: String?, finished: Bool,
                             pageTitle: String? = nil, now: Date = Date()) -> [ActivityEvent] {
        // 只记 http/https（与 HistoryStore 口径一致），挡掉 about:blank / 内建新标签页等噪声。
        guard let u = URL(string: url), let scheme = u.scheme?.lowercased(),
              scheme == "http" || scheme == "https" else { return [] }
        var out: [ActivityEvent] = []
        let actor = owner == nil ? "user" : "assistant"
        let clean = ActivityUrls.sanitized(url)
        // 用「当前页 URL」判换页，而非「进行中的 nav」——nav 收尾后它已清空，重复的 didFinish
        // 会因此被误判成新页（多发一条 nav.started）。pageURL 一直在，重复 didFinish 自然被忽略。
        if clean != pageURL {
            if let hang = closeHangingNav(outcome: "cancelled", pageTitle: pageTitle) { out.append(hang) }
            if let dwell = closePageDwell(now: now, pageTitle: pageTitle) { out.append(dwell) }
            let pair = UUID().uuidString
            navPair = pair
            navActor = actor
            pageURL = clean
            pageSince = now
            pageActor = actor
            // 换页：nav.started 还不知道新页标题（provisional 时 title 仍是旧页），不带 title。
            out.append(navStarted(url: clean, host: host, owner: owner, actor: actor, pair: pair))
        }
        if finished, navPair != nil {
            out.append(closeNav(outcome: "ok", errorType: nil, pageTitle: pageTitle))
        } else if finished, clean == pageURL {
            // 同 URL 重载：没换页（无 dwell、无新 nav），但确有一次加载 → 记一条零时长 nav 补上。
            // （重复 didFinish 会再记一条；实际主框架一个加载只回调一次，可接受。）
            let pair = UUID().uuidString
            navPair = pair
            navActor = actor
            out.append(navStarted(url: clean, host: host, owner: owner, actor: actor, pair: pair, pageTitle: pageTitle))
            out.append(closeNav(outcome: "ok", errorType: nil, pageTitle: pageTitle))
        }
        return out
    }

    private func navStarted(url: String, host: String, owner: String?, actor: String,
                            pair: String, pageTitle: String? = nil) -> ActivityEvent {
        var attrs: [String: JSONValue] = [
            "url_raw": .string(url),
            "host": .string(host),
            "owner": .string(owner == nil ? "user" : "agent"),
            "tab_id": .string(tabId),
        ]
        if let title = pageTitle, !title.isEmpty { attrs["page_title"] = .string(title) }
        return ActivityEvent(
            type: "browser.nav.started", actor: actor, trigger: "manual",
            parentEventId: agentContext?.bridgeEventId, pairId: pair,
            conversationId: agentContext?.conversationId,
            turnId: agentContext?.turnId,
            attrs: attrs)
    }

    /// 加载失败（didFail / didFailProvisional）：收当前 nav 为 failed（页面 dwell 不收——页面可能还在）。
    mutating func navFailed(errorType: String, pageTitle: String? = nil) -> [ActivityEvent] {
        guard navPair != nil else { return [] }
        return [closeNav(outcome: "failed", errorType: errorType, pageTitle: pageTitle)]
    }

    /// 标签关闭：收被悬挂的 nav + 当前页 dwell。
    mutating func closed(pageTitle: String? = nil, now: Date = Date()) -> [ActivityEvent] {
        var out: [ActivityEvent] = []
        if let hang = closeHangingNav(outcome: "cancelled", pageTitle: pageTitle) { out.append(hang) }
        if let dwell = closePageDwell(now: now, pageTitle: pageTitle) { out.append(dwell) }
        return out
    }

    /// 收藏（用户显式动作，点事件，根）。
    static func bookmarkAdded(url: String, titleLength: Int) -> ActivityEvent {
        ActivityEvent(
            type: "browser.bookmark.added", actor: "user", trigger: "manual",
            attrs: ["url": .string(ActivityUrls.sanitized(url)), "title_len": .number(Double(titleLength))])
    }

    private mutating func closeNav(outcome: String, errorType: String?, pageTitle: String?) -> ActivityEvent {
        var attrs: [String: JSONValue]? = nil
        if let title = pageTitle, !title.isEmpty { attrs = ["page_title": .string(title)] }
        let ev = ActivityEvent(
            type: "browser.nav.ended", actor: navActor, trigger: "manual",
            outcome: outcome, errorType: errorType,
            parentEventId: agentContext?.bridgeEventId, pairId: navPair,
            conversationId: agentContext?.conversationId,
            turnId: agentContext?.turnId,
            attrs: attrs)
        navPair = nil
        return ev
    }

    private mutating func closeHangingNav(outcome: String, pageTitle: String?) -> ActivityEvent? {
        guard navPair != nil else { return nil }
        return closeNav(outcome: outcome, errorType: nil, pageTitle: pageTitle)
    }

    private mutating func closePageDwell(now: Date, pageTitle: String?) -> ActivityEvent? {
        guard let url = pageURL, let since = pageSince else { return nil }
        let ms = (max(0, now.timeIntervalSince(since)) * 1000).rounded()
        var attrs: [String: JSONValue] = ["url": .string(url), "dwell_ms": .number(ms)]
        if let title = pageTitle, !title.isEmpty { attrs["page_title"] = .string(title) }
        let ev = ActivityEvent(
            type: "browser.page.dwell", actor: pageActor, trigger: "manual",
            parentEventId: agentContext?.bridgeEventId,
            conversationId: agentContext?.conversationId,
            turnId: agentContext?.turnId,
            attrs: attrs)
        pageURL = nil
        pageSince = nil
        return ev
    }
}

/// 助手代开/操作标签时的会话上下文（随 `client_tool_call` 下发）。挂到浏览事件上，
/// 让「助手替你打开的页面」能归到会话与轮次（附表 A `browser.nav.started` 的父 = `tool.bridge.dispatched`）。
nonisolated struct AgentActivityContext: Equatable, Sendable {
    let conversationId: String?
    let turnId: String?
    let bridgeEventId: String?
}
