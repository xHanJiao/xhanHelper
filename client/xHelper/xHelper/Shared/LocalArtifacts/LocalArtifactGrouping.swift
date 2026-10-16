import Foundation

/// 把消息里**相邻的卡片类本地产物**并成一行（聊天里的横向卡片条），其余一条一行。
/// 纯逻辑、泛型 + 谓词（不依赖 `Message`），便于单测。
///
/// 为什么按「相邻」而不是按轮次：`LocalArtifactMerge.reposition` 已把同一轮的产物排到一起、
/// 与其它消息隔开（且非卡片类产物会打断分组），所以这里的「相邻」等价于「同一轮」。
enum LocalArtifactGrouping {
    /// 返回逐行的分组：卡片行是多元素数组，其余是单元素数组。顺序与入参一致。
    static func rows<T>(_ items: [T], isCard: (T) -> Bool) -> [[T]] {
        var rows: [[T]] = []
        var lastWasCard = false
        for item in items {
            let card = isCard(item)
            if card, lastWasCard {
                rows[rows.count - 1].append(item)
            } else {
                rows.append([item])
            }
            lastWasCard = card
        }
        return rows
    }
}
