import Foundation

/// GrepMatch — grep 单条匹配结果。
struct GrepMatch {
    let file: String
    let line: Int
    let text: String
}

/// GrepStop — 收集停止原因（影响输出提示语）。
enum GrepStop: Equatable {
    case moreAvailable // 已收满 maxResults，第 maxResults+1 条触发停止
    case budgetCapped  // 进程被输出预算掐断（安全网）
    case none          // 自然收集完（可能恰满 100 或不足）
}

/// GrepCollectResult — 一次 grep/glob 收集的汇总。
struct GrepCollectResult {
    var matches: [GrepMatch] = []
    var omittedSensitive = 0 // 被敏感文件锁拦下的匹配条数
    var stop: GrepStop = .none

    var truncated: Bool { stop != .none }
}

/// GrepCollector — 流式收集器：skip 跳过 → 敏感过滤 → 收满 maxResults+1 停。
/// 纯逻辑、进程无关，供真实执行与单测共用。
struct GrepCollector {
    let maxResults: Int
    let skip: Int
    let sensitiveFilter: (String) -> Bool // 返回 true = 该文件被敏感锁拦截
    private(set) var result = GrepCollectResult()
    private var skipped = 0

    init(maxResults: Int = 100, skip: Int = 0, sensitiveFilter: @escaping (String) -> Bool = { _ in false }) {
        self.maxResults = maxResults
        self.skip = max(0, skip)
        self.sensitiveFilter = sensitiveFilter
    }

    /// 喂入一条解析结果（nil = 非匹配行，忽略）。返回 false = 应停止进程。
    /// 顺序：敏感过滤（被拦的匹配不占用 skip 预算）→ skip 跳过 → 收满 maxResults+1 停。
    mutating func ingest(_ match: GrepMatch?) -> Bool {
        guard let m = match else { return true }
        if sensitiveFilter(m.file) {
            result.omittedSensitive += 1
            return true
        }
        if skipped < skip {
            skipped += 1
            return true
        }
        if result.matches.count >= maxResults {
            result.stop = .moreAvailable
            return false
        }
        result.matches.append(m)
        return true
    }

    /// 预算掐断标记（进程被安全网终止）。仅当尚未因 match 上限停止时生效，
    /// 避免覆盖更精确的 moreAvailable 语义。
    mutating func markBudgetCapped() {
        if result.stop == .none {
            result.stop = .budgetCapped
        }
    }
}

/// GrepParser — grep 行解析与输出格式化（纯逻辑，进程无关）。
enum GrepParser {
    /// 解析 rg --json 单行。仅 type == "match" 的记录被接受，其余（begin/end/summary 等）返回 nil。
    static func parseRgLine(_ line: String, maxLineChars: Int) -> GrepMatch? {
        guard let d = line.data(using: .utf8),
              let obj = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any],
              let type = obj["type"] as? String, type == "match",
              let dataObj = obj["data"] as? [String: Any],
              let pathObj = dataObj["path"] as? [String: Any],
              let filePath = pathObj["text"] as? String,
              let linesObj = dataObj["lines"] as? [String: Any],
              let lineText = linesObj["text"] as? String,
              let lineNum = dataObj["line_number"] as? Int else {
            return nil
        }
        let display = lineText.count > maxLineChars ? String(lineText.prefix(maxLineChars)) + "..." : lineText
        let cleanPath = filePath.replacingOccurrences(of: "\\", with: "/")
        return GrepMatch(file: cleanPath, line: lineNum, text: display)
    }

    /// 解析 system grep -rn 单行（"path:line:content"）。
    static func parsePlainLine(_ line: String, maxLineChars: Int) -> GrepMatch? {
        let parts = line.components(separatedBy: ":")
        guard parts.count >= 3, let lineNum = Int(parts[1]) else { return nil }
        let filePath = parts[0]
        let content = parts[2...].joined(separator: ":")
        let display = content.count > maxLineChars ? String(content.prefix(maxLineChars)) + "..." : content
        return GrepMatch(file: filePath, line: lineNum, text: display)
    }

    /// 汇总输出：文件分组排序 + 按字节预算增量构建。
    /// - count 语义：shownCount = 实际显示的匹配条数（预算装得下几条显示几条）
    /// - 截断提示：预算裁切（shownCount < matches.count）或 stop != .none 都出
    ///   "Use skip=N to continue"（N = pageSkip + shownCount，累计偏移，翻页可覆盖全量）
    /// - 返回 (text, shownCount, truncated)；truncated = 还有更多未显示
    static func formatGrepOutput(
        matches: [GrepMatch],
        omittedSensitive: Int,
        stop: GrepStop,
        pageSkip: Int,
        maxBytes: Int
    ) -> (text: String, shownCount: Int, truncated: Bool) {
        if matches.isEmpty {
            var out = "No files found"
            if omittedSensitive > 0 {
                out += " (\(omittedSensitive) 条匹配来自敏感文件已省略)"
            }
            return (out, 0, false)
        }

        // 按文件分组排序，切成"段"（文件头 + 逐条匹配），每段带字节数
        var grouped: [String: [GrepMatch]] = [:]
        for m in matches {
            grouped[m.file, default: []].append(m)
        }
        var segments: [(text: String, bytes: Int)] = []
        for file in grouped.keys.sorted() {
            let header = "\n\(file):\n"
            segments.append((header, header.utf8.count))
            for m in grouped[file] ?? [] {
                let line = "  Line \(m.line): \(m.text)\n"
                segments.append((line, line.utf8.count))
            }
        }

        // 贪心装入：保证至少装下 1 条匹配（单条上限 2000 字符 ≪ 预算）
        var included: [String] = []
        var total = 0
        var shown = 0
        for seg in segments {
            if seg.text.hasPrefix("\n") {
                // 文件头：仅在已有内容或这是第一段时装入（避免只装文件头）
                if shown > 0 || included.isEmpty {
                    if total + seg.bytes > maxBytes { break }
                    included.append(seg.text)
                    total += seg.bytes
                }
                continue
            }
            if total + seg.bytes > maxBytes { break }
            included.append(seg.text)
            total += seg.bytes
            shown += 1
        }

        let truncated = shown < matches.count || stop != .none
        var out = "Found \(shown) matches"
        if truncated {
            out += " (more matches available. Use skip=\(pageSkip + shown) to continue.)"
        }
        out += "\n"
        out += included.joined()
        if omittedSensitive > 0 {
            out += "\n(\(omittedSensitive) 条匹配来自敏感文件已省略。如需访问请在客户端设置中添加豁免。)"
        }
        return (out, shown, truncated)
    }
}
