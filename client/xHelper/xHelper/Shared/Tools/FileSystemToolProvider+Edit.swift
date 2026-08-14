#if os(macOS)
import Foundation

// MARK: - Edit 预览（类型见 FileSystemToolProvider+Write.swift 的 FSPreview）

// MARK: - Edit 实现

extension FileSystemToolProvider {

    // MARK: 预览（L3 确认前计算 diff，不落盘）

    /// 依据参数预演编辑并生成 diff 文本，供确认条展示。
    /// 参数非法 / 匹配失败 / 多匹配时返回 nil（执行时会给出明确错误）。
    static func previewEdit(arguments: [String: Any]) -> FSPreview? {
        guard let rawPath = arguments["path"] as? String, !rawPath.isEmpty,
              let oldString = arguments["old_string"] as? String,
              let newString = arguments["new_string"] as? String else {
            return nil
        }
        let path = resolvePath(rawPath)
        let replaceAll = (arguments["replace_all"] as? Bool) ?? false

        let fm = FileManager.default
        guard fm.fileExists(atPath: path), SensitiveFileBlocker.check(path) == nil else {
            return nil
        }
        guard let oldText = FileSystemToolProvider.readFullText(path) else { return nil }
        guard let applied = try? applyEdit(oldText, oldString: oldString, newString: newString, replaceAll: replaceAll) else {
            return nil
        }
        let result = DiffRenderer.render(oldText: oldText, newText: applied.text, filePath: path)
        return FSPreview(
            filePath: path,
            op: "编辑",
            diffText: result.text,
            additions: result.additions,
            deletions: result.deletions,
            degraded: result.degraded
        )
    }

    // MARK: 执行

    func handleEdit(arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard let rawPath = arguments["path"] as? String, !rawPath.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 path 参数", clientAction: nil)
        }
        guard let oldString = arguments["old_string"] as? String else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 old_string 参数", clientAction: nil)
        }
        guard let newString = arguments["new_string"] as? String else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 new_string 参数", clientAction: nil)
        }
        let replaceAll = (arguments["replace_all"] as? Bool) ?? false

        let path = FileSystemToolProvider.resolvePath(rawPath)

        if let blocked = SensitiveFileBlocker.check(path) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }
        if !DirectoryAuthorizationManager.shared.isAuthorized(path) {
            return LocalToolResult(serverResult: nil, serverError: "路径不在已授权目录内（或 symlink 指向授权目录外）: \(path)", clientAction: nil)
        }

        let fm = FileManager.default
        var isDir: ObjCBool = false
        guard fm.fileExists(atPath: path, isDirectory: &isDir) else {
            return LocalToolResult(serverResult: nil, serverError: "文件不存在: \(path)", clientAction: nil)
        }
        if isDir.boolValue {
            return LocalToolResult(serverResult: nil, serverError: "路径是目录，不是文件: \(path)", clientAction: nil)
        }

        // 写前必读：编辑已有文件需本会话 read 过（防盲改）
        if !hasRead(path) {
            return LocalToolResult(serverResult: nil, serverError: "请先用 read 读取该文件后再编辑: \(path)", clientAction: nil)
        }

        guard let oldText = FileSystemToolProvider.readFullText(path) else {
            return LocalToolResult(serverResult: nil, serverError: "无法读取文件: \(path)", clientAction: nil)
        }

        let applied: (text: String, count: Int, matchLines: [Int])
        do {
            applied = try Self.applyEdit(oldText, oldString: oldString, newString: newString, replaceAll: replaceAll)
        } catch let err as FSEditError {
            return LocalToolResult(serverResult: nil, serverError: err.message, clientAction: nil)
        } catch {
            return LocalToolResult(serverResult: nil, serverError: "编辑失败: \(error.localizedDescription)", clientAction: nil)
        }

        // 原子写（保留原 BOM + 权限）
        let original = (try? Data(contentsOf: URL(fileURLWithPath: path))) ?? Data()
        let data = bomPrefixed(applied.text, originalData: original)
        do {
            try atomicReplaceFile(data: data, targetPath: path, fileExists: true)
        } catch {
            return LocalToolResult(serverResult: nil, serverError: "写入失败: \(error.localizedDescription)", clientAction: nil)
        }

        markRead(path)

        let lineDesc = applied.matchLines.map(String.init).joined(separator: ", ")
        return LocalToolResult(
            serverResult: "Replaced \(applied.count) occurrence(s) at line(s) \(lineDesc) — 替换成功: \(path)".data(using: .utf8),
            serverError: nil,
            clientAction: nil
        )
    }

    // MARK: 匹配核心

    struct FSEditError: Error {
        let message: String
    }

    /// 在 oldText 中查找并替换 oldString → newString。
    /// 三级匹配：exact → line-trimmed → whitespace-normalized；行尾风格随原文保留。
    static func applyEdit(
        _ oldText: String,
        oldString: String,
        newString: String,
        replaceAll: Bool
    ) throws -> (text: String, count: Int, matchLines: [Int]) {
        if oldString.isEmpty {
            throw FSEditError(message: "old_string 不能为空：新建文件请用 write，编辑请提供精确匹配文本")
        }
        if oldString == newString {
            throw FSEditError(message: "old_string 与 new_string 相同，没有可应用的变更")
        }

        // 行尾归一化：内部按 \n 匹配，写回恢复原风格
        let ending: String = oldText.contains("\r\n") ? "\r\n" : "\n"
        let content = oldText.replacingOccurrences(of: "\r\n", with: "\n")
        let find = oldString.replacingOccurrences(of: "\r\n", with: "\n")
        let replace = newString.replacingOccurrences(of: "\r\n", with: "\n")

        var candidates: [String] = [find]
        for cand in lineTrimmedCandidates(content: content, find: find) where !candidates.contains(cand) {
            candidates.append(cand)
        }
        for cand in whitespaceNormalizedCandidates(content: content, find: find) where !candidates.contains(cand) {
            candidates.append(cand)
        }

        for cand in candidates {
            var ranges: [Range<String.Index>] = []
            var searchFrom = content.startIndex
            while let r = content.range(of: cand, range: searchFrom..<content.endIndex) {
                ranges.append(r)
                searchFrom = r.upperBound
            }
            if ranges.isEmpty { continue }

            if isDisproportionateMatch(candidate: cand, oldString: find) {
                throw FSEditError(message: "拒绝替换：匹配到的跨度远大于 old_string。请重读文件，提供完整精确的 old_string（含空白与缩进）")
            }

            // 换行计数（split 默认省略空行，会导致行号偏小）
            let matchLines = ranges.map { content[..<$0.lowerBound].filter { $0 == "\n" }.count + 1 }
            let newContent: String
            let count: Int
            if replaceAll {
                newContent = content.replacingOccurrences(of: cand, with: replace)
                count = ranges.count
            } else {
                if ranges.count > 1 {
                    throw FSEditError(message: "old_string 匹配到 \(ranges.count) 处（行号 \(matchLines.map(String.init).joined(separator: ", "))）。请提供更多上下文使其唯一，或使用 replace_all=true 全部替换")
                }
                let r = ranges[0]
                newContent = String(content[..<r.lowerBound]) + replace + String(content[r.upperBound...])
                count = 1
            }
            let restored = ending == "\r\n" ? newContent.replacingOccurrences(of: "\n", with: "\r\n") : newContent
            return (restored, count, matchLines)
        }

        throw FSEditError(message: "未找到 old_string。请重读文件确认内容与空白/缩进完全一致，再提供精确的 old_string")
    }

    /// line-trimmed：逐行去首尾空白后比较，命中则返回原文块（保留原空白）。
    private static func lineTrimmedCandidates(content: String, find: String) -> [String] {
        var result: [String] = []
        let contentLines = content.components(separatedBy: "\n")
        var findLines = find.components(separatedBy: "\n")
        if findLines.last == "" { findLines.removeLast() }
        if findLines.isEmpty || findLines.count > contentLines.count { return [] }

        for i in 0...(contentLines.count - findLines.count) {
            var allMatch = true
            for j in 0..<findLines.count {
                if contentLines[i + j].trimmingCharacters(in: .whitespaces) != findLines[j].trimmingCharacters(in: .whitespaces) {
                    allMatch = false
                    break
                }
            }
            if allMatch {
                let block = contentLines[i..<(i + findLines.count)].joined(separator: "\n")
                if !result.contains(block) { result.append(block) }
            }
        }
        return result
    }

    /// whitespace-normalized：空白序列归一为单空格后比较（仅单行场景）。
    private static func whitespaceNormalizedCandidates(content: String, find: String) -> [String] {
        guard !find.contains("\n") else { return [] }
        let normFind = normalizeWhitespace(find)
        var result: [String] = []
        for line in content.components(separatedBy: "\n") {
            if normalizeWhitespace(line) == normFind {
                if !result.contains(line) { result.append(line) }
            }
        }
        return result
    }

    private static func normalizeWhitespace(_ s: String) -> String {
        s.split(whereSeparator: { $0 == " " || $0 == "\t" })
            .joined(separator: " ")
            .trimmingCharacters(in: .whitespaces)
    }

    /// 守卫：匹配跨度远大于 old_string 时拒绝（防误匹配到超大块）。
    private static func isDisproportionateMatch(candidate: String, oldString: String) -> Bool {
        let oldLines = oldString.split(separator: "\n").count
        let searchLines = candidate.split(separator: "\n").count
        if searchLines >= max(oldLines + 3, oldLines * 2) { return true }
        if oldLines == 1 { return false }
        let candLen = candidate.trimmingCharacters(in: .whitespacesAndNewlines).count
        let findLen = oldString.trimmingCharacters(in: .whitespacesAndNewlines).count
        return candLen > max(findLen + 500, findLen * 4)
    }
}
#endif
