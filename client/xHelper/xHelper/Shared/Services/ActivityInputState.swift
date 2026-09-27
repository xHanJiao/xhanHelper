import Foundation

/// 输入强度状态机（纯逻辑，不依赖 AppKit；`NSEvent` 接线见 `ActivityInputMonitor`）。
///
/// 兑现设计篇第一节「只记有没有输入，不记输入了什么」：只累计三者——这段持续多久、字符量落哪一档、
/// 有没有删过；**不带任何按键序列 / 文本 / 光标**。形态是区间配对 `client.input.active` / `.ended`，
/// 停 `idleGapSeconds` 秒算一段结束，下次输入开新段（附表 A「在场与电源」）。
nonisolated struct ActivityInputState {
    /// 停这么久算一段输入结束（附表 A：停 5 秒算一段结束）。
    static let idleGapSeconds: Double = 5
    static let bucketSmallMax = 50
    static let bucketMediumMax = 200

    private(set) var activeSince: Date?
    private var lastInputAt: Date?
    private var pairId: String?
    private var chars = 0
    private var hadDelete = false

    /// 记一次输入。开新段时返回 `client.input.active`（区间起），续段返回 nil。
    mutating func keystroke(charCount: Int, isDelete: Bool, now: Date = Date()) -> ActivityEvent? {
        if activeSince == nil {
            let pair = UUID().uuidString
            activeSince = now
            lastInputAt = now
            pairId = pair
            chars = max(0, charCount)
            hadDelete = isDelete
            return ActivityEvent(type: "client.input.active", actor: "user", trigger: "manual", pairId: pair)
        }
        lastInputAt = now
        chars += max(0, charCount)
        hadDelete = hadDelete || isDelete
        return nil
    }

    /// 轮询：距最后一次输入已过 `idleGapSeconds` 即收段，返回 `client.input.ended`。
    mutating func tick(now: Date = Date()) -> ActivityEvent? {
        guard let since = activeSince, let last = lastInputAt,
              now.timeIntervalSince(last) >= Self.idleGapSeconds else { return nil }
        return close(since: since, until: last)
    }

    /// 明确收段（停止采集 / 进程退出）；段还没到 idle 间隔也收。
    mutating func end(now: Date = Date()) -> ActivityEvent? {
        guard let since = activeSince else { return nil }
        return close(since: since, until: lastInputAt ?? now)
    }

    /// 字符量分档：0-50 / 51-200 / 201+（标签不重叠，服务端按字符串消费时口径唯一）。
    static func bucket(_ chars: Int) -> String {
        if chars <= bucketSmallMax { return "0-50" }
        if chars <= bucketMediumMax { return "51-200" }
        return "201+"
    }

    private mutating func close(since: Date, until: Date) -> ActivityEvent {
        // 时长只算「真在打字」的那段（首键 → 末键），不含后面这 5 秒静默。
        let durationMs = (max(0, until.timeIntervalSince(since)) * 1000).rounded()
        let ev = ActivityEvent(
            type: "client.input.ended", actor: "user", trigger: "manual", pairId: pairId,
            attrs: [
                "duration_ms": .number(durationMs),
                "chars_bucket": .string(Self.bucket(chars)),
                "had_delete": .bool(hadDelete),
            ])
        activeSince = nil
        lastInputAt = nil
        pairId = nil
        chars = 0
        hadDelete = false
        return ev
    }
}
