import Foundation

/// 本地产物在消息列表里的重定位（纯逻辑，便于单测）。
///
/// 背景：本地产物消息（`ChartBubble` / 文档卡片）随工具结果即时 append，`createdAt`
/// 与 assistant 消息的落库时间不保证同序，且流式期 assistant 的 id 还会变。所以不按 id 绑定，
/// 而是按时间排好序后，把每条本地产物移到「其后最近一条 assistant」之后——卡片天然挨在
/// 产生它的那条回复下面。
enum LocalArtifactMerge {
    static func reposition<T>(_ items: [T], isLocal: (T) -> Bool, isAssistant: (T) -> Bool) -> [T] {
        var result = items
        guard result.count > 1 else { return result }
        for i in stride(from: result.count - 1, through: 0, by: -1) {
            guard isLocal(result[i]) else { continue }
            for j in (i + 1) ..< result.count where isAssistant(result[j]) {
                let item = result.remove(at: i)
                let insertAt = j > i ? j : j + 1
                result.insert(item, at: min(insertAt, result.count))
                break
            }
        }
        return result
    }
}
