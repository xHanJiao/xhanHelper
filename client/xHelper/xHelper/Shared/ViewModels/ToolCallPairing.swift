import Foundation

/// `tool_call` / `tool_result` 的配对规则（纯逻辑，镜像进 SwiftPM `ChatWireCore` 单测）。
///
/// 为什么需要 id：服务端先按调用顺序发**服务端工具**的 `tool_call`，客户端工具的 `tool_call`
/// 要等执行时才补发，而 `tool_result` 又按原始调用顺序发 —— 只靠 FIFO 必然错位；
/// 且 `tool_call` 是 best-effort 发送，丢一条会连锁错位整轮。
nonisolated enum ToolCallPairing {

    /// `tool_call` 事件里的 LLM 调用 id（服务端字段名 `id`）。
    /// 字段名本身就是两端契约，所以提到这里用真实事件 fixture 钉住 ——
    /// 以前它散在 `ChatViewModel.parseToolCall` 里，改名了单测也照样绿。
    static func callID(fromEventJSON data: String) -> String? {
        string("id", in: data)
    }

    /// `tool_result` 事件里的 LLM 调用 id（服务端字段名 `tool_call_id`，旧服务端没有）。
    static func resultID(fromEventJSON data: String) -> String? {
        string("tool_call_id", in: data)
    }

    /// 结果应消费的 pending 下标。
    /// - 结果带 `tool_call_id`：只在 id 相同的条目里找；找不到返回 nil
    ///   （对应的 `tool_call` 可能被丢了，不能顺手吞掉别人的 pending）。
    /// - 结果不带 id（旧服务端 / 非原生 FC）：回退队首，保持历史行为。
    static func index(ofResultID resultID: String?, in pendingIDs: [String?]) -> Int? {
        guard !pendingIDs.isEmpty else { return nil }
        let key = normalize(resultID)
        guard !key.isEmpty else { return 0 }
        return pendingIDs.firstIndex { normalize($0) == key }
    }

    private static func normalize(_ id: String?) -> String {
        (id ?? "").trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// 取字符串字段并去空白；缺失 / 非字符串 / 空串 → nil（调用方回退 FIFO）。
    private static func string(_ key: String, in data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let value = obj[key] as? String
        else { return nil }
        let trimmed = normalize(value)
        return trimmed.isEmpty ? nil : trimmed
    }
}
