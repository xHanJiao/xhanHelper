#if os(macOS)
import Foundation

extension FileSystemToolProvider {

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
        if !DirectoryAuthorizationManager.shared.isAuthorized(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: "路径不在已授权目录内（或 symlink 指向授权目录外）: \(resolvedPath)", clientAction: nil)
        }

        let include = arguments["include"] as? String
        let skip = (arguments["skip"] as? Int) ?? 0
        // 输出预算只裁文案（formatGrepOutput 按字节增量构建），收集走安全网预算
        let outputBudget = budgetCap(context.budgetBytes)

        if Self.isPDFFile(path: resolvedPath) {
            return runPDFGrep(
                pattern: pattern, path: resolvedPath, skip: skip,
                maxLineChars: maxLineChars, outputBudget: outputBudget)
        }

        let collector = await runGrep(
            pattern: pattern, path: resolvedPath, include: include, skip: skip)
        let formatted = GrepParser.formatGrepOutput(
            matches: collector.matches,
            omittedSensitive: collector.omittedSensitive,
            stop: collector.stop,
            pageSkip: skip,
            maxBytes: outputBudget)
        let sanitized = OutputSanitizer.sanitize(formatted.text)
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
    ) -> LocalToolResult {
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
        return LocalToolResult(serverResult: sanitized.data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private func runGrep(pattern: String, path: String, include: String?, skip: Int) async -> GrepCollectResult {
        if let rgPath = findRgBinary() {
            return await runRgGrep(rgPath: rgPath, pattern: pattern, path: path, include: include, skip: skip)
        }
        return await runSystemGrep(pattern: pattern, path: path, include: include, skip: skip)
    }

    /// rg --json 流式收集：-j1 单线程保证输出顺序确定；skip 跳过 → 敏感过滤 →
    /// 收满 maxResults 即停（预算为纯安全网，正常流程永不触发，保证结果确定性）。
    private func runRgGrep(rgPath: String, pattern: String, path: String, include: String?, skip: Int) async -> GrepCollectResult {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: rgPath)

        var args = ["--no-config", "--json", "--hidden", "--no-messages", "-j1", "--glob=!**/.git/**"]
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
        let capped = await runCapturedProcessLines(proc, budget: collectionSafetyBudget) { line in
            collector.ingest(GrepParser.parseRgLine(line, maxLineChars: self.maxLineChars))
        }
        if capped {
            collector.markBudgetCapped()
        }
        return collector.result
    }

    private func runSystemGrep(pattern: String, path: String, include: String?, skip: Int) async -> GrepCollectResult {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/grep")
        var args = ["-rn", "--include=*"]
        if let inc = include {
            args = ["-rn", "--include=\(inc)"]
        }
        // 与 rg 的 --glob=!**/.git/** 对齐
        args += ["--exclude-dir=.git"]
        args.append(pattern)
        args.append(path)

        proc.arguments = args

        var collector = GrepCollector(maxResults: maxResults, skip: skip) { file in
            SensitiveFileBlocker.check(file) != nil
        }
        let capped = await runCapturedProcessLines(proc, budget: collectionSafetyBudget) { line in
            collector.ingest(GrepParser.parsePlainLine(line, maxLineChars: self.maxLineChars))
        }
        if capped {
            collector.markBudgetCapped()
        }
        return collector.result
    }
}
#endif
