import Foundation

/// 本地产物在消息列表里的重定位（纯逻辑，便于单测）。
///
/// 背景：本地产物消息（`ChartBubble` / 文档卡片）随工具结果即时 append，`createdAt`
/// 与 assistant 消息的落库时间不保证同序（同秒 tie / 时区格式不一致），且流式期
/// assistant 的 id 还会变。所以不按 id 绑定。
///
/// 归位规则：把每条本地产物移到**它前面最近一条 user 消息之后**——本地产物总是产生于
/// 某一轮 user 发出之后，故该 user 就是它所属轮次的锚点；这样卡片落在这一轮的 user 与
/// assistant（思考 + 正文）之间，不会因为「该轮 assistant 恰好没有可见正文（被服务端
/// 过滤）」而被吸到上一轮。找不到 user（如隐藏轮）时回退到「最近的 assistant」。
enum LocalArtifactMerge {
    static func reposition<T>(
        _ items: [T],
        isLocal: (T) -> Bool,
        isUser: (T) -> Bool,
        isAssistant: (T) -> Bool
    ) -> [T] {
        let n = items.count
        guard n > 1 else { return items }

        let userIndices = (0..<n).filter { isUser(items[$0]) }
        let assistantIndices = (0..<n).filter { isAssistant(items[$0]) }
        guard !userIndices.isEmpty || !assistantIndices.isEmpty else { return items }

        // 每条本地产物 → 锚点下标，按原始顺序分组（同锚点保持出现顺序）
        var localsForAnchor: [Int: [Int]] = [:]
        for i in 0..<n where isLocal(items[i]) {
            let anchor: Int?
            if let user = userIndices.last(where: { $0 < i }) {
                anchor = user
            } else {
                anchor = assistantIndices.min {
                    let d0 = abs($0 - i), d1 = abs($1 - i)
                    return d0 != d1 ? d0 < d1 : $0 < $1
                }
            }
            guard let a = anchor else { continue }
            localsForAnchor[a, default: []].append(i)
        }

        // 重建：user 锚点 → 产物插在该 user 之后；assistant 锚点（回退）→ 插在该 assistant 之前。
        var result: [T] = []
        result.reserveCapacity(n)
        for i in 0..<n {
            if isLocal(items[i]) { continue }
            if isAssistant(items[i]), let locals = localsForAnchor[i] {
                result.append(contentsOf: locals.map { items[$0] })
            }
            result.append(items[i])
            if isUser(items[i]), let locals = localsForAnchor[i] {
                result.append(contentsOf: locals.map { items[$0] })
            }
        }
        return result
    }
}
