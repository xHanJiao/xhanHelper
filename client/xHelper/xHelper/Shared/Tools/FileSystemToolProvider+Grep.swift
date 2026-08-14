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
        var isDir: ObjCBool = false
        let exists = FileManager.default.fileExists(atPath: resolvedPath, isDirectory: &isDir)

        guard exists else {
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
        let budget = budgetCap(context.budgetBytes)

        let matches = await runGrep(pattern: pattern, path: resolvedPath, include: include, isDir: isDir.boolValue, budget: budget)
        let output = formatGrepOutput(matches: matches, pattern: pattern)
        let capped = cappedOutput(output, budget: budget, continueHint: "考虑缩小搜索范围或改用更具体的 pattern")
        let sanitized = OutputSanitizer.sanitize(capped.text)
        return LocalToolResult(serverResult: sanitized.data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private func runGrep(pattern: String, path: String, include: String?, isDir: Bool, budget: Int) async -> [GrepMatch] {
        if let rgPath = findRgBinary() {
            return await runRgGrep(rgPath: rgPath, pattern: pattern, path: path, include: include, isDir: isDir, budget: budget)
        }
        return await runSystemGrep(pattern: pattern, path: path, include: include, isDir: isDir, budget: budget)
    }

    /// 在后台执行进程：流式读取 stdout，超预算即 terminate，避免大输出死锁 + 主线程阻塞。
    func runCapturedProcess(
        _ proc: Process,
        budget: Int
    ) async -> (data: Data, capped: Bool) {
        await withCheckedContinuation { continuation in
            Task.detached(priority: .userInitiated) {
                let pipe = Pipe()
                proc.standardOutput = pipe
                proc.standardError = Pipe()
                let fh = pipe.fileHandleForReading

                do {
                    try proc.run()
                } catch {
                    try? fh.close()
                    continuation.resume(returning: (Data(), false))
                    return
                }

                var data = Data()
                var capped = false
                while true {
                    let chunk = fh.readData(ofLength: 8192)
                    if chunk.isEmpty { break }
                    data.append(chunk)
                    if data.count > budget {
                        capped = true
                        proc.terminate()
                        // 排空剩余避免死锁，然后退出
                        while true {
                            let rest = fh.readData(ofLength: 8192)
                            if rest.isEmpty { break }
                        }
                        break
                    }
                }
                proc.waitUntilExit()
                try? fh.close()
                continuation.resume(returning: (data, capped))
            }
        }
    }

    private func runRgGrep(rgPath: String, pattern: String, path: String, include: String?, isDir: Bool, budget: Int) async -> [GrepMatch] {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: rgPath)

        var args = ["--no-config", "--json", "--hidden", "--no-messages", "--glob=!**/.git/**"]
        if let inc = include {
            args.append("--glob=\(inc)")
        }
        args.append("--")
        args.append(pattern)
        args.append(path)

        proc.arguments = args

        let (data, _) = await runCapturedProcess(proc, budget: budget)
        guard !data.isEmpty else { return [] }
        return parseRgJSON(data)
    }

    private func parseRgJSON(_ data: Data) -> [GrepMatch] {
        guard let text = String(data: data, encoding: .utf8) else { return [] }
        var matches: [GrepMatch] = []
        for line in text.components(separatedBy: "\n") {
            guard let d = line.data(using: .utf8),
                  let obj = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any],
                  let type = obj["type"] as? String, type == "match",
                  let dataObj = obj["data"] as? [String: Any],
                  let pathObj = dataObj["path"] as? [String: Any],
                  let filePath = pathObj["text"] as? String,
                  let linesObj = dataObj["lines"] as? [String: Any],
                  let lineText = linesObj["text"] as? String,
                  let lineNum = dataObj["line_number"] as? Int else {
                continue
            }
            if matches.count >= maxResults { break }
            let display = lineText.count > maxLineChars ? String(lineText.prefix(maxLineChars)) + "..." : lineText
            let cleanPath = filePath.replacingOccurrences(of: "\\", with: "/")
            matches.append(GrepMatch(file: cleanPath, line: lineNum, text: display))
        }
        return matches
    }

    private func runSystemGrep(pattern: String, path: String, include: String?, isDir: Bool, budget: Int) async -> [GrepMatch] {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/grep")
        var args = ["-rn", "--include=*"]
        if let inc = include {
            args = ["-rn", "--include=\(inc)"]
        }
        args.append(pattern)
        args.append(path)

        proc.arguments = args

        let (data, _) = await runCapturedProcess(proc, budget: budget)
        guard let text = String(data: data, encoding: .utf8), !text.isEmpty else { return [] }

        var matches: [GrepMatch] = []
        for line in text.components(separatedBy: "\n") {
            if matches.count >= maxResults { break }
            let parts = line.components(separatedBy: ":")
            guard parts.count >= 3, let lineNum = Int(parts[1]) else { continue }
            let filePath = parts[0]
            let content = parts[2...].joined(separator: ":")
            let display = content.count > maxLineChars ? String(content.prefix(maxLineChars)) + "..." : content
            matches.append(GrepMatch(file: filePath, line: lineNum, text: display))
        }
        return matches
    }

    private func formatGrepOutput(matches: [GrepMatch], pattern: String) -> String {
        if matches.isEmpty {
            return "No files found"
        }

        var output = "Found \(matches.count) matches"
        if matches.count >= maxResults {
            output += " (more matches available)"
        }
        output += "\n"

        var grouped: [String: [GrepMatch]] = [:]
        for m in matches {
            grouped[m.file, default: []].append(m)
        }

        var first = true
        for file in grouped.keys.sorted() {
            if !first { output += "\n" }
            first = false
            output += "\n\(file):\n"
            for m in grouped[file] ?? [] {
                output += "  Line \(m.line): \(m.text)\n"
            }
        }

        if matches.count >= maxResults {
            output += "\n(Results truncated. Consider using a more specific path or pattern.)"
        }
        return output
    }
}

struct GrepMatch {
    let file: String
    let line: Int
    let text: String
}
#endif
