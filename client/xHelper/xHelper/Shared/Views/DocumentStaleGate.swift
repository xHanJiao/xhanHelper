import Foundation

/// 「正文疑似过旧 → 自动拉一次新正文」的闸门。
///
/// 文档由 agent 改写，客户端手里那份经常是旧的；旧正文里，服务端夹逼改写过的 quote
/// 画不出线（`docs/design/document-highlight-annotation/design.md` 文末「正文旧了要自己发现」）。
/// 单独抽出来是因为这层只有状态、没有视图：可以脱离 SwiftUI / WebKit 用 `swift test` 覆盖
/// （见 `Tests/DocumentStaleGateTests`），而它是防死循环的那道闸。
///
/// 语义三条：
/// - 同一份**屏幕正文**上最多自动拉一次新正文（拉完还画不出就只提示，不再拉）；
/// - 正文换了（`screenChanged()`）就重新开口 —— 上一轮画不出的划线，新一轮可能就能画了；
/// - 「提示」整篇只给一次，别每轮正文变化都吵一遍。
struct DocumentStaleGate {
    private var reloadedThisScreen = false
    private var warned = false

    /// 又发现一次「服务端说没失活、正文里却画不出来」。返回 true = 该拉一次新正文。
    mutating func noteStale() -> Bool {
        guard !reloadedThisScreen else { return false }
        reloadedThisScreen = true
        return true
    }

    /// 屏幕上的正文换了（DOM 重渲染）→ 重新开口。
    mutating func screenChanged() {
        reloadedThisScreen = false
    }

    /// 拉过新正文还是画不出来：返回 true = 该提示一次。
    mutating func noteStillUndrawn() -> Bool {
        guard !warned else { return false }
        warned = true
        return true
    }
}
