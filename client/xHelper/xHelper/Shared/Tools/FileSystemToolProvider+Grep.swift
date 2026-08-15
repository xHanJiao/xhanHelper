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
