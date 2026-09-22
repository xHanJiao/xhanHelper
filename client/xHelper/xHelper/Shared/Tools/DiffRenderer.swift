import Foundation

/// unified diff 文本 → 带行号的逐行结构（视图只做排版，不在 View 里解析文本）。
/// 行号单列口径：加行/上下文行取新文件行号，删行取旧文件行号（git 单列显示同款）。
enum DiffLineParser {
    struct Line {
        enum Kind {
            case meta, added, removed, context
        }
        let kind: Kind
        let number: Int?
        /// 已剥掉 `+` / `-` / 空格 前缀
        let text: String
    }

    /// 丢弃 `---`/`+++` 文件头与空行（卡片 header 已展示路径）。
    static func parse(_ diff: String) -> [Line] {
        var out: [Line] = []
        var oldNo = 1
        var newNo = 1

        for raw in diff.components(separatedBy: "\n") {
            if raw.isEmpty || raw.hasPrefix("--- ") || raw.hasPrefix("+++ ") { continue }
            if raw.hasPrefix("@@") {
                if let start = hunkStart(raw) { oldNo = start.old; newNo = start.new }
                out.append(Line(kind: .meta, number: nil, text: raw))
                continue
            }
            if raw.hasPrefix("\\") { // "\ No newline at end of file"
                out.append(Line(kind: .meta, number: nil, text: raw))
                continue
            }
            if raw.hasPrefix("-") {
                out.append(Line(kind: .removed, number: oldNo, text: String(raw.dropFirst())))
                oldNo += 1
                continue
            }
            if raw.hasPrefix("+") {
                out.append(Line(kind: .added, number: newNo, text: String(raw.dropFirst())))
                newNo += 1
                continue
            }
            if raw.hasPrefix(" ") {
                out.append(Line(kind: .context, number: newNo, text: String(raw.dropFirst())))
                oldNo += 1
                newNo += 1
                continue
            }
            // 非 unified 文本（degraded 摘要 / 纯文本 fallback）：按普通行编号
            out.append(Line(kind: .context, number: oldNo, text: raw))
            oldNo += 1
            newNo += 1
        }
        return out
    }

    /// `@@ -134,11 +134,13 @@` → (old: 134, new: 134)
    private static func hunkStart(_ line: String) -> (old: Int, new: Int)? {
        let parts = line.split(separator: " ")
        guard parts.count >= 3, parts[1].hasPrefix("-"), parts[2].hasPrefix("+") else { return nil }
        func number(_ token: Substring) -> Int? {
            Int(token.dropFirst().split(separator: ",")[0])
        }
        guard let old = number(parts[1]), let new = number(parts[2]) else { return nil }
        return (old, new)
    }
}

#if os(macOS)

/// 行级 diff 渲染：CollectionDifference → unified patch 文本 + 增减统计。
/// 供 fs 写工具 L3 确认条预览使用（只走 UI，不上传服务端）。
enum DiffRenderer {

    struct Result {
        let text: String
        let additions: Int?
        let deletions: Int?
        /// true = 文件过大退化为摘要，未生成行级 diff
        let degraded: Bool
    }

    /// 超过该行数不做行级 diff（CollectionDifference 大输入慢且预览无意义）
    static let maxDiffLines = 4000
    static let maxOutputChars = 40000
    private static let contextLines = 3

    private enum Op {
        case context(text: String, oldNo: Int, newNo: Int)
        case remove(text: String, oldNo: Int)
        case insert(text: String, newNo: Int)
    }

    static func render(oldText: String, newText: String, filePath: String) -> Result {
        var oldLines = oldText.components(separatedBy: "\n")
        if oldLines.last == "" { oldLines.removeLast() }
        var newLines = newText.components(separatedBy: "\n")
        if newLines.last == "" { newLines.removeLast() }

        if oldLines.count > maxDiffLines || newLines.count > maxDiffLines {
            return Result(
                text: "⚠️ 文件较大（\(oldLines.count) 行 → \(newLines.count) 行），未生成行级 diff 预览。",
                additions: nil,
                deletions: nil,
                degraded: true
            )
        }

        // 收集变更并排序（difference 的返回顺序不可靠：清空时倒序、replace 时 insert 在前）
        let changes = newLines.difference(from: oldLines)
        var removals: [(offset: Int, text: String)] = []
        var insertions: [(offset: Int, text: String)] = []
        for change in changes {
            switch change {
            case .remove(let offset, let element, _): removals.append((offset, element))
            case .insert(let offset, let element, _): insertions.append((offset, element))
            }
        }
        removals.sort { $0.offset < $1.offset }
        insertions.sort { $0.offset < $1.offset }

        // 双游标对齐构造编辑脚本
        var ops: [Op] = []
        var oldIdx = 0
        var newIdx = 0
        var ri = 0
        var ii = 0
        var additions = 0
        var deletions = 0

        while oldIdx < oldLines.count || newIdx < newLines.count || ri < removals.count || ii < insertions.count {
            while ii < insertions.count, insertions[ii].offset == newIdx {
                ops.append(.insert(text: insertions[ii].text, newNo: newIdx + 1))
                additions += 1
                newIdx += 1
                ii += 1
            }
            if ri < removals.count, removals[ri].offset == oldIdx {
                ops.append(.remove(text: removals[ri].text, oldNo: oldIdx + 1))
                deletions += 1
                oldIdx += 1
                ri += 1
                continue
            }
            if oldIdx < oldLines.count && newIdx < newLines.count {
                ops.append(.context(text: oldLines[oldIdx], oldNo: oldIdx + 1, newNo: newIdx + 1))
                oldIdx += 1
                newIdx += 1
            } else if oldIdx < oldLines.count {
                ops.append(.remove(text: oldLines[oldIdx], oldNo: oldIdx + 1))
                deletions += 1
                oldIdx += 1
            } else if newIdx < newLines.count {
                ops.append(.insert(text: newLines[newIdx], newNo: newIdx + 1))
                additions += 1
                newIdx += 1
            } else {
                break
            }
        }

        guard additions > 0 || deletions > 0 else {
            return Result(text: "（无内容变更）", additions: 0, deletions: 0, degraded: false)
        }

        // git 惯例重排：连续变更段内先 remove 后 insert（红上绿下），上下文行不动
        var orderedOps: [Op] = []
        var run: [Op] = []
        func flushRun() {
            guard !run.isEmpty else { return }
            let removes = run.compactMap { op -> Op? in
                if case .remove = op { return op }
                return nil
            }
            let inserts = run.compactMap { op -> Op? in
                if case .insert = op { return op }
                return nil
            }
            orderedOps.append(contentsOf: removes)
            orderedOps.append(contentsOf: inserts)
            run.removeAll()
        }
        for op in ops {
            switch op {
            case .context:
                flushRun()
                orderedOps.append(op)
            case .remove, .insert:
                run.append(op)
            }
        }
        flushRun()
        ops = orderedOps

        // 变更簇分组 → hunk 区间（含上下文）
        let changeIdxs = ops.indices.filter {
            switch ops[$0] {
            case .context: return false
            case .remove, .insert: return true
            }
        }
        var hunks: [(start: Int, end: Int)] = []
        var hunkStart = changeIdxs[0]
        var hunkEnd = changeIdxs[0]
        for idx in changeIdxs.dropFirst() {
            if idx - hunkEnd - 1 <= contextLines * 2 {
                hunkEnd = idx
            } else {
                hunks.append((max(0, hunkStart - contextLines), min(ops.count, hunkEnd + contextLines + 1)))
                hunkStart = idx
                hunkEnd = idx
            }
        }
        hunks.append((max(0, hunkStart - contextLines), min(ops.count, hunkEnd + contextLines + 1)))

        var out = "--- \(filePath)\n+++ \(filePath)\n"
        for hunk in hunks {
            let slice = ops[hunk.start..<hunk.end]
            let first = slice[slice.startIndex]
            let oldStart: Int
            let newStart: Int
            switch first {
            case .context(_, let o, let n): oldStart = o; newStart = n
            case .remove(_, let o): oldStart = o; newStart = o
            case .insert(_, let n): oldStart = n; newStart = n
            }
            var oldCount = 0
            var newCount = 0
            for op in slice {
                switch op {
                case .context: oldCount += 1; newCount += 1
                case .remove: oldCount += 1
                case .insert: newCount += 1
                }
            }
            out += "@@ -\(oldStart),\(oldCount) +\(newStart),\(newCount) @@\n"
            for op in slice {
                switch op {
                case .context(let t, _, _): out += " \(t)\n"
                case .remove(let t, _): out += "-\(t)\n"
                case .insert(let t, _): out += "+\(t)\n"
                }
            }
            if out.count > maxOutputChars {
                out = String(out.prefix(maxOutputChars)) + "\n...(diff 过长已截断)\n"
                break
            }
        }

        return Result(text: out, additions: additions, deletions: deletions, degraded: false)
    }
}
#endif
