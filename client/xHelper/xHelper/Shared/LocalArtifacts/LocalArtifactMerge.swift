import Foundation

/// 本地产物在消息列表里的重定位（纯逻辑，便于单测）。
///
/// 背景：本地产物消息（`ChartBubble` / 文档卡片）随工具结果即时 append，`createdAt`
/// 与 assistant 消息的落库时间不保证同序（同秒 tie 时字符串排序不稳定），且流式期
/// assistant 的 id 还会变。所以不按 id 绑定，而是按位置把每条本地产物移到**最近的
/// assistant 之前**——卡片天然落在产生它的那条回复的思考 + 正文上方。
enum LocalArtifactMerge {
    static func reposition<T>(_ items: [T], isLocal: (T) -> Bool, isAssistant: (T) -> Bool) -> [T] {
        let n = items.count
        guard n > 1 else { return items }

        let assistantIndices = (0..<n).filter { isAssistant(items[$0]) }
        guard !assistantIndices.isEmpty else { return items }

        // 每条本地产物 → 最近的 assistant（位置距离最近；等距取更靠前者），按原始顺序分组
        var localsForAssistant: [Int: [Int]] = [:]
        for i in 0..<n where isLocal(items[i]) {
            let anchor = assistantIndices.min {
                let d0 = abs($0 - i), d1 = abs($1 - i)
                return d0 != d1 ? d0 < d1 : $0 < $1
            }
            guard let anchor else { continue }
            localsForAssistant[anchor, default: []].append(i)
        }

        // 重建：本地产物从原位移除，改注入到其 anchor assistant 之前（同 anchor 保持原始顺序）
        var result: [T] = []
        result.reserveCapacity(n)
        for i in 0..<n {
            if isLocal(items[i]) { continue }
            if let locals = localsForAssistant[i] {
                result.append(contentsOf: locals.map { items[$0] })
            }
            result.append(items[i])
        }
        return result
    }
}
