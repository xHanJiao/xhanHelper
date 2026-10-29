import Foundation

/// 本地产物与其所属消息的时间戳比较。
///
/// **为什么不能直接比字符串**：本地卡片用 `ISO8601DateFormatter`（UTC，如 `2026-09-26T04:04:32Z`），
/// 而服务端（本地 SQLite 后端）下发的是东八区 `2026-09-26T12:04:12.879551+08:00`——同一时刻，
/// 字符串前缀却差 8 小时。字符串比较会把卡片排到整轮之前（重定位到上一条回复）/ 被范围过滤剔除
/// （刷新即消失）。故一律解析成 `Date` 再比；解析失败才回退字符串比较（保持旧行为，不让比较崩）。
///
/// **时钟域**：卡片时间来自本机时钟，消息时间来自服务端时钟（两者可能差几十秒，见 docs/bugs/040）。
/// 所以这里只做「同域内比较」和「下界判定」，**不做上界**——跨时钟域的上界比较会把卡片直接剔掉。
enum LocalArtifactTime {
    /// 解析 ISO8601：兼容带/不带小数秒、`Z`/`+08:00` 各种偏移。
    static func parse(_ s: String) -> Date? {
        let withFraction = ISO8601DateFormatter()
        withFraction.formatOptions = [.withInternetDateTime, .withFractionalSeconds]
        if let d = withFraction.date(from: s) { return d }
        let plain = ISO8601DateFormatter()
        plain.formatOptions = [.withInternetDateTime]
        return plain.date(from: s)
    }

    /// a 是否早于 b（用于排序）；任一侧解析失败回退字符串比较。
    static func isOrdered(_ a: String, before b: String) -> Bool {
        if let da = parse(a), let db = parse(b) { return da < db }
        return a < b
    }

    /// 时间串是否不早于 `from`（加载页下界）。任一侧解析失败回退字符串比较。
    /// **只判下界**：卡片时间是客户端时钟、消息时间是服务端时钟，上界比较会把卡片误剔（见 docs/bugs/040）。
    static func isOnOrAfter(_ s: String, from: String) -> Bool {
        if let ds = parse(s), let df = parse(from) { return ds >= df }
        return s >= from
    }
}
