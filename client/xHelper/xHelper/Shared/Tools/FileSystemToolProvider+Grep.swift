#if os(macOS)
import Foundation

extension FileSystemToolProvider {

    /// grep 溢出时的全量格式化预算（收集本身受 maxResults=100 限制，此处仅防失控）。
    static let grepOverflowBudget = 4 * 1024 * 1024

    /// 一次 grep 子进程的结果（含超时/诊断信息）。
    struct GrepRunOutcome {
        var result: GrepCollectResult
        var timedOut: Bool
        var stderrTail: String
    }

    func handleGrep(arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard let pattern = arguments["pattern"] as? String, !pattern.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 pattern 参数", clientAction: nil)
        }
        guard let rawPath = arguments["path"] as? String, !rawPath.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 path 参数", clientAction: nil)
        }

        let resolvedPath = FileSystemToolProvider.resolvePath(rawPath)
        guard FileManager.default.fileExists(atPath: resolvedPath) else {
            return LocalToolResult(serverResult: nil, serverError: "路径不存在: \(resolvedPath)", clientAction: nil)
        }

        if let blocked = SensitiveFileBlocker.check(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }

        // symlink 复检：真实路径须在授权目录内
        if !DirectoryAuthorizationManager.shared.isReadAuthorized(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: "路径不在已授权目录内（或 symlink 指向授权目录外）: \(resolvedPath)", clientAction: nil)
        }

        let include = arguments["include"] as? String
        let skip = (arguments["skip"] as? Int) ?? 0
        // 输出预算只裁文案（formatGrepOutput 按字节增量构建），收集走安全网预算
        let outputBudget = budgetCap(context.budgetBytes)
        let timeout = effectiveProcessTimeout(context)
        let started = Date()

        if Self.isPDFFile(path: resolvedPath) {
            return await runPDFGrep(
                pattern: pattern, path: resolvedPath, skip: skip,
                maxLineChars: maxLineChars, outputBudget: outputBudget)
        }

        let outcome = await runGrep(
            pattern: pattern, path: resolvedPath, include: include, skip: skip, timeout: timeout)
        let collector = outcome.result
        let elapsedMs = Int(Date().timeIntervalSince(started) * 1000)
        let formatted = GrepParser.formatGrepOutput(
            matches: collector.matches,
            omittedSensitive: collector.omittedSensitive,
            stop: collector.stop,
            pageSkip: skip,
            maxBytes: outputBudget)
        let sanitized = OutputSanitizer.sanitize(formatted.text)

        // 超时：返回已收集部分（落盘供续读）或可执行错误，绝不白等满服务端 240s
        if outcome.timedOut {
            let note = "grep 超时（\(Int(timeout))s），已扫描到 \(collector.matches.count) 条匹配后中止。请缩小 path（指定子文件/子目录）、加 include、或简化 pattern。"
            fsToolLog("fs__grep TIMEOUT path=\(resolvedPath) include=\(include ?? "-") pattern=\(pattern) elapsed=\(elapsedMs)ms matches=\(collector.matches.count) stderr=\(outcome.stderrTail)")
            if !collector.matches.isEmpty {
                let full = GrepParser.formatGrepOutput(
                    matches: collector.matches,
                    omittedSensitive: collector.omittedSensitive,
                    stop: collector.stop,
                    pageSkip: skip,
                    maxBytes: Self.grepOverflowBudget).text
                if let overflow = await FileSystemToolProvider.makeOverflow(
                    fullTextProvider: { full }, preview: sanitized, budget: outputBudget, note: note) {
                    return overflow
                }
            }
            return LocalToolResult(serverResult: nil, serverError: note, clientAction: nil)
        }

        if formatted.truncated {
            let full = GrepParser.formatGrepOutput(
                matches: collector.matches,
                omittedSensitive: collector.omittedSensitive,
                stop: collector.stop,
                pageSkip: skip,
                maxBytes: Self.grepOverflowBudget).text
            fsToolLog("fs__grep path=\(resolvedPath) include=\(include ?? "-") pattern=\(pattern) elapsed=\(elapsedMs)ms matches=\(collector.matches.count) overflow=true")
            if let overflow = await FileSystemToolProvider.makeOverflow(
                fullTextProvider: { full }, preview: sanitized, budget: outputBudget) {
                return overflow
            }
        }
        fsToolLog("fs__grep path=\(resolvedPath) include=\(include ?? "-") pattern=\(pattern) elapsed=\(elapsedMs)ms matches=\(collector.matches.count)")
        return LocalToolResult(serverResult: sanitized.data(using: .utf8), serverError: nil, clientAction: nil)
    }

    /// 是否为 PDF 文件（扩展名或魔数 %PDF）。仅单文件命中，目录仍走 rg。
    static func isPDFFile(path: String) -> Bool {
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: path, isDirectory: &isDir), !isDir.boolValue else {
            return false
        }
        if (path as NSString).pathExtension.lowercased() == "pdf" { return true }

        guard let fh = FileHandle(forReadingAtPath: path) else { return false }
        defer { try? fh.close() }
        guard let head = try? fh.read(upToCount: 4) else { return false }
        return head == Data([0x25, 0x50, 0x44, 0x46]) // %PDF
    }

    /// PDF 检索：提取文本层 → 按行正则匹配 → 复用 grep 输出协议（Page N + skip 翻页）。
    private func runPDFGrep(
        pattern: String, path: String, skip: Int, maxLineChars: Int, outputBudget: Int
    ) async -> LocalToolResult {
        let pages: [String]
        do {
            pages = try PDFTextExtractor.pageTexts(path: path)
        } catch let e as PDFExtractError {
            return LocalToolResult(serverResult: nil, serverError: e.errorDescription, clientAction: nil)
        } catch {
            return LocalToolResult(serverResult: nil, serverError: "PDF 提取失败: \(error.localizedDescription)", clientAction: nil)
        }

        let collector: GrepCollectResult
        do {
            collector = try PDFGrepMatcher.search(
                pageTexts: pages, pattern: pattern, file: path,
                maxLineChars: maxLineChars, maxResults: maxResults, skip: skip)
        } catch PDFGrepError.invalidPattern {
            return LocalToolResult(serverResult: nil, serverError: "无效正则表达式: \(pattern)", clientAction: nil)
        } catch {
            return LocalToolResult(serverResult: nil, serverError: "PDF 检索失败: \(error.localizedDescription)", clientAction: nil)
        }

        let formatted = GrepParser.formatGrepOutput(
            matches: collector.matches,
            omittedSensitive: collector.omittedSensitive,
            stop: collector.stop,
            pageSkip: skip,
            maxBytes: outputBudget)
        let sanitized = OutputSanitizer.sanitize(formatted.text)
        if formatted.truncated {
            let full = GrepParser.formatGrepOutput(
                matches: collector.matches,
                omittedSensitive: collector.omittedSensitive,
                stop: collector.stop,
                pageSkip: skip,
                maxBytes: Self.grepOverflowBudget).text
            if let overflow = await FileSystemToolProvider.makeOverflow(
                fullTextProvider: { full }, preview: sanitized, budget: outputBudget) {
                return overflow
            }
        }
        return LocalToolResult(serverResult: sanitized.data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private func runGrep(pattern: String, path: String, include: String?, skip: Int, timeout: TimeInterval) async -> GrepRunOutcome {
        if let rgPath = findRgBinary() {
            return await runRgGrep(rgPath: rgPath, pattern: pattern, path: path, include: include, skip: skip, timeout: timeout)
        }
        return await runSystemGrep(pattern: pattern, path: path, include: include, skip: skip, timeout: timeout)
    }

    /// rg --json 流式收集：-j1 单线程保证输出顺序确定；skip 跳过 → 敏感过滤 →
    /// 收满 maxResults 即停（预算为纯安全网，正常流程永不触发，保证结果确定性）。
    private func runRgGrep(rgPath: String, pattern: String, path: String, include: String?, skip: Int, timeout: TimeInterval) async -> GrepRunOutcome {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: rgPath)

        var args = ["--no-config", "--json", "--no-messages", "-j1",
                    "--max-filesize", "10M",
                    "--glob=!**/.git/**", "--glob=!**/node_modules/**"]
        if let inc = include {
            args.append("--glob=\(inc)")
        }
        args.append("--")
        args.append(pattern)
        args.append(path)

        proc.arguments = args

        var collector = GrepCollector(maxResults: maxResults, skip: skip) { file in
            SensitiveFileBlocker.check(file) != nil
        }
        let capture = await runCapturedProcessLines(proc, budget: collectionSafetyBudget, timeout: timeout) { line in
            collector.ingest(GrepParser.parseRgLine(line, maxLineChars: self.maxLineChars))
        }
        if capture.capped {
            collector.markBudgetCapped()
        }
        return GrepRunOutcome(result: collector.result, timedOut: capture.timedOut, stderrTail: capture.stderrTail)
    }

    private func runSystemGrep(pattern: String, path: String, include: String?, skip: Int, timeout: TimeInterval) async -> GrepRunOutcome {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/grep")
        var args = ["-rn", "-s", "--include=*"]
        if let inc = include {
            args = ["-rn", "-s", "--include=\(inc)"]
        }
        // 与 rg 的排除项对齐
        args += ["--exclude-dir=.git", "--exclude-dir=node_modules"]
        args.append(pattern)
        args.append(path)

        proc.arguments = args

        var collector = GrepCollector(maxResults: maxResults, skip: skip) { file in
            SensitiveFileBlocker.check(file) != nil
        }
        let capture = await runCapturedProcessLines(proc, budget: collectionSafetyBudget, timeout: timeout) { line in
            collector.ingest(GrepParser.parsePlainLine(line, maxLineChars: self.maxLineChars))
        }
        if capture.capped {
            collector.markBudgetCapped()
        }
        return GrepRunOutcome(result: collector.result, timedOut: capture.timedOut, stderrTail: capture.stderrTail)
    }
}
#endif
