import Foundation

// MARK: - SubagentResultParser — 子 agent 最终 XML 结果 → 可读 Markdown
//
// 子 agent（web_explore/file_explore/user_context）按固定 XML 结构输出结论，落库为子会话
// 最后一条 assistant 消息。面板直接展示原始 XML 很丑，本解析器把它转成「中文小节 + Markdown」。
// 纯逻辑（无 SwiftUI），与 app 共享源文件，由 FsCore 包单测自验。

struct SubagentResult {
    let title: String    // 结果类型标题（网页调研/文件检索/用户信息检索）
    let markdown: String // 渲染用 Markdown
}

enum SubagentResultParser {
    // 根标签 → 标题
    private static let roots: [(tag: String, title: String)] = [
        ("research_result", "网页调研"),
        ("file_explore_result", "文件检索"),
        ("user_context_result", "用户信息检索"),
    ]

    // 子标签 → (中文小节, 是否列表字段)
    private static let sections: [(tag: String, label: String, isList: Bool)] = [
        ("summary", "结论", false),
        ("points", "要点", true),
        ("findings", "发现", true),
        ("sources", "来源", true),
        ("files_read", "已读文件", true),
        ("failed_attempts", "失败尝试", true),
        ("open_questions", "未解决问题", true),
    ]

    /// 解析子 agent 最终 XML。无已知根标签或无可渲染小节 → nil（调用方回退原始列表）。
    static func parse(_ text: String) -> SubagentResult? {
        guard let root = roots.first(where: { hasRoot(text, $0.tag) }) else { return nil }

        var parts: [String] = []
        for s in sections {
            guard let raw = extract(text, tag: s.tag) else { continue }
            let body = raw.trimmingCharacters(in: .whitespacesAndNewlines)
            if body.isEmpty || isNone(body) { continue }
            if s.isList {
                let items = splitList(body)
                guard !items.isEmpty else { continue }
                parts.append("### \(s.label)\n\n" + items.map { "- \($0)" }.joined(separator: "\n"))
            } else {
                parts.append("### \(s.label)\n\n\(body)")
            }
        }
        guard !parts.isEmpty else { return nil }
        return SubagentResult(title: root.title, markdown: parts.joined(separator: "\n\n"))
    }

    /// 清洗折叠行摘要：优先取 `<summary>` 内容；取不到（如 200 字截断的半截 XML）则剥掉所有标签。
    static func cleanSummary(_ text: String) -> String {
        if let s = extract(text, tag: "summary") {
            let t = s.trimmingCharacters(in: .whitespacesAndNewlines)
            if !t.isEmpty { return t }
        }
        return stripTags(text).trimmingCharacters(in: .whitespacesAndNewlines)
    }

    // MARK: - 私有

    private static func hasRoot(_ text: String, _ tag: String) -> Bool {
        text.range(of: "<\(tag)[\\s>]", options: .regularExpression) != nil
    }

    /// 提取 `<tag>…</tag>` 内容（非贪婪、跨行）。闭合标签缺失（截断）→ nil。
    private static func extract(_ text: String, tag: String) -> String? {
        let pattern = "<\(tag)>(.*?)</\(tag)>"
        guard let re = try? NSRegularExpression(pattern: pattern, options: [.dotMatchesLineSeparators]) else { return nil }
        let range = NSRange(text.startIndex..., in: text)
        guard let m = re.firstMatch(in: text, range: range), m.numberOfRanges > 1,
              let r = Range(m.range(at: 1), in: text) else { return nil }
        return String(text[r])
    }

    /// 剥掉全部 XML 标签，含末尾被截断的半个标签（`…<points` → `…`）。
    private static func stripTags(_ text: String) -> String {
        var s = text
        s = s.replacingOccurrences(of: "<[^>\\n]*>", with: "", options: .regularExpression)
        s = s.replacingOccurrences(of: "<[^>\\n]*$", with: "", options: .regularExpression)
        return s
    }

    /// 列表字段拆分：按换行 / 中文分号 / 英文分号切分，逐条去空白、去空项。
    private static func splitList(_ body: String) -> [String] {
        body.split(whereSeparator: { $0 == "\n" || $0 == "；" || $0 == ";" })
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
    }

    private static func isNone(_ body: String) -> Bool {
        let normalized = body.trimmingCharacters(in: .whitespacesAndNewlines)
            .trimmingCharacters(in: CharacterSet(charactersIn: "。."))
            .lowercased()
        return ["无", "none", "n/a", "na", "-"].contains(normalized)
    }
}
