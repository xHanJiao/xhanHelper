import Foundation

/// PDFPageCleaner — 位置无关的页眉/页脚清洗（纯逻辑、进程无关，供 read/grep 共享）。
enum PDFPageCleaner {
    /// 组合清洗：先去行尾页码，再去跨页重复行。
    static func clean(pages: [String]) -> [String] {
        stripRepeatedLines(pages: stripTrailingPageNumbers(pages: pages))
    }

    /// 行尾连续数字 token 等于物理页码时删除该 token（含其前空白）。
    /// 数字前为 `.`/`,` 时视为数值一部分（`1.69`、`1,069`），保留。
    /// ponytail: 不处理 `- 69 -`、`第69页` 等包裹形式，也不处理印刷页码与物理页的偏移。
    static func stripTrailingPageNumbers(pages: [String]) -> [String] {
        pages.enumerated().map { idx, page in
            let pageNumber = idx + 1
            return page.components(separatedBy: "\n")
                .map { stripTrailingNumber($0, pageNumber: pageNumber) }
                .joined(separator: "\n")
        }
    }

    /// 归一化后出现在 `>= max(minPages, ceil(页数*threshold))` 个不同页的行，从所有页删除（不看位置）。
    /// 守卫：长度 < minChars 或不含字母/CJK 的行不参与统计，避免误删"单位：元""合计"等短重复行。
    /// ponytail: 阈值启发式——不足阈值页重复的页眉会漏，阈值内重复的正文行会被误删。
    static func stripRepeatedLines(
        pages: [String],
        threshold: Double = 0.8,
        minPages: Int = 3,
        minChars: Int = 8
    ) -> [String] {
        let total = pages.count
        guard total >= minPages else { return pages }
        let need = max(minPages, Int((Double(total) * threshold).rounded(.up)))

        var counts: [String: Int] = [:]
        var perPage: [[(norm: String?, raw: String)]] = []

        for page in pages {
            var seen = Set<String>()
            var entries: [(String?, String)] = []
            for line in page.components(separatedBy: "\n") {
                let norm = normalizedRepeatedCandidate(line, minChars: minChars)
                entries.append((norm, line))
                if let norm, !seen.contains(norm) {
                    seen.insert(norm)
                    counts[norm, default: 0] += 1
                }
            }
            perPage.append(entries)
        }

        let repeated = Set(counts.filter { $0.value >= need }.keys)
        guard !repeated.isEmpty else { return pages }
        let keys = Array(repeated)

        return perPage.map { entries in
            entries.compactMap { entry -> String? in
                let (norm, raw) = entry
                if let norm, repeated.contains(norm) { return nil } // 整行命中 → 删行
                guard norm != nil else { return raw }               // 非候选行不可能含 key
                return removeInline(raw, keys: keys)                // 行内内嵌 → 摘除子串
            }.joined(separator: "\n")
        }
    }

    // MARK: - Helpers

    private static func stripTrailingNumber(_ line: String, pageNumber: Int) -> String {
        var s = line
        while let last = s.last, last == " " || last == "\t" {
            s.removeLast()
        }
        guard !s.isEmpty else { return line }

        var start = s.endIndex
        while start > s.startIndex {
            let prev = s.index(before: start)
            let ch = s[prev]
            if ch.isASCII, ch.isNumber {
                start = prev
            } else {
                break
            }
        }
        guard start < s.endIndex, let value = Int(s[start...]), value == pageNumber else {
            return line
        }
        if start > s.startIndex {
            let before = s[s.index(before: start)]
            if before == "." || before == "," { return line }
        }

        var cut = start
        while cut > s.startIndex {
            let prev = s.index(before: cut)
            if s[prev] == " " || s[prev] == "\t" { cut = prev } else { break }
        }
        return String(s[..<cut])
    }

    /// 忽略空白差异，从行内删除所有 repeated 串（映射回原下标，连同串内空白一起删）。
    /// 未命中任何 key 时原样返回（不做 trim）。ponytail: 行内剔除更激进，
    /// 靠"key 本就是 ≥80% 页的整行重复"兜底；若某正文行恰好内嵌完整页眉串，也会被摘除。
    private static func removeInline(_ line: String, keys: [String]) -> String {
        var current = line
        var removedAny = false
        var guardCount = 0

        while guardCount < 8 {
            guardCount += 1

            var compact = ""
            var map: [String.Index] = []
            map.reserveCapacity(current.count)
            for idx in current.indices {
                let ch = current[idx]
                if ch.isWhitespace { continue }
                compact.append(ch)
                map.append(idx)
            }

            var matched = false
            for key in keys {
                guard let range = compact.range(of: key) else { continue }
                let startOff = compact.distance(from: compact.startIndex, to: range.lowerBound)
                let endOff = compact.distance(from: compact.startIndex, to: range.upperBound) - 1
                let startOrig = map[startOff]
                let endOrig = current.index(after: map[endOff])
                current.removeSubrange(startOrig..<endOrig)
                removedAny = true
                matched = true
                break
            }
            if !matched { break }
        }
        return removedAny ? current.trimmingCharacters(in: .whitespaces) : line
    }

    /// 归一化候选：去掉所有空白；长度 < minChars 或无字母/CJK → nil（不参与重复统计）。
    /// 去空白（而非折叠）是为了容忍 PDFKit 在字词间插入的多余空格。
    private static func normalizedRepeatedCandidate(_ line: String, minChars: Int) -> String? {
        let normalized = String(line.filter { !$0.isWhitespace })
        guard normalized.count >= minChars, normalized.contains(where: { $0.isLetter }) else { return nil }
        return normalized
    }
}

/// PDFPageWindower — 按页 / 页内字符偏移 + 字节预算切窗（纯逻辑、进程无关）。
/// 输出为纯页文本（页间以换行分隔，不注入页码标记），返回精确续读点
/// （页 + 页内字符偏移），保证单页超预算时至少返回该页一部分，绝不空输出或跳页。
enum PDFPageWindower {
    struct Window: Equatable {
        var text: String
        var truncated: Bool
        var nextPage: Int?      // 1-indexed；nil = 已到末尾
        var nextCharOffset: Int // 续读点（页内字符偏移）
    }

    static func window(
        pages: [String],
        startPage: Int,
        charOffset: Int,
        pageLimit: Int,
        budgetBytes: Int
    ) -> Window {
        let total = pages.count
        guard total > 0, startPage >= 1, startPage <= total else {
            return Window(text: "", truncated: false, nextPage: nil, nextCharOffset: 0)
        }
        let start = startPage
        let end = min(start + max(1, pageLimit) - 1, total)

        var out = ""
        var bytes = 0

        for page in start...end {
            var body = pages[page - 1]
            let sliceStart = (page == start) ? min(max(0, charOffset), body.count) : 0
            if sliceStart > 0 {
                body = String(body.dropFirst(sliceStart))
            }

            let prefix = out.isEmpty ? "" : "\n"
            let need = prefix.utf8.count + body.utf8.count
            if bytes + need <= budgetBytes {
                out += prefix + body
                bytes += need
                continue
            }

            // 页间分隔符都放不下：从本页当前位置续读
            let remaining = budgetBytes - bytes - prefix.utf8.count
            if remaining <= 0 {
                return Window(text: out, truncated: true, nextPage: page, nextCharOffset: sliceStart)
            }
            let fitted = prefixByBytes(body, maxBytes: remaining)
            out += prefix + fitted
            return Window(
                text: out,
                truncated: true,
                nextPage: page,
                nextCharOffset: sliceStart + fitted.count)
        }

        if end < total {
            return Window(text: out, truncated: false, nextPage: end + 1, nextCharOffset: 0)
        }
        return Window(text: out, truncated: false, nextPage: nil, nextCharOffset: 0)
    }

    /// 取 utf8 字节数不超过 maxBytes 的最长字符前缀（不劈多字节字符）。
    private static func prefixByBytes(_ s: String, maxBytes: Int) -> String {
        guard maxBytes > 0 else { return "" }
        var count = 0
        var used = 0
        for ch in s {
            let n = String(ch).utf8.count
            if used + n > maxBytes { break }
            used += n
            count += 1
        }
        return String(s.prefix(count))
    }
}
