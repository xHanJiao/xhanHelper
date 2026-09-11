#if os(macOS)
import Foundation

extension FileSystemToolProvider {

    func handleGlob(arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard let pattern = arguments["pattern"] as? String, !pattern.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 pattern 参数", clientAction: nil)
        }
        guard let rawPath = arguments["path"] as? String, !rawPath.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 path 参数", clientAction: nil)
        }

        let resolvedPath = FileSystemToolProvider.resolvePath(rawPath)
        var isDir: ObjCBool = false
        guard FileManager.default.fileExists(atPath: resolvedPath, isDirectory: &isDir), isDir.boolValue else {
            return LocalToolResult(serverResult: nil, serverError: "路径不存在或不是目录: \(resolvedPath)", clientAction: nil)
        }

        if let blocked = SensitiveFileBlocker.check(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }

        // symlink 复检：真实路径须在授权目录内
        if !DirectoryAuthorizationManager.shared.isAuthorized(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: "路径不在已授权目录内（或 symlink 指向授权目录外）: \(resolvedPath)", clientAction: nil)
        }

        let budget = budgetCap(context.budgetBytes)
        let collect = await runGlob(pattern: pattern, cwd: resolvedPath)
        if collect.files.isEmpty && collect.omittedSensitive == 0 {
            let encoded = try? JSONSerialization.data(withJSONObject: ["files": [], "count": 0, "pattern": pattern])
            return LocalToolResult(serverResult: OutputSanitizer.sanitize(String(data: encoded ?? Data(), encoding: .utf8) ?? "").data(using: .utf8), serverError: nil, clientAction: nil)
        }

        var result = collect.files.joined(separator: "\n")
        if collect.truncated {
            result += "\n\n(Results truncated: showing first \(collect.files.count). Use a more specific pattern, or narrow the path.)"
        } else if collect.budgetCapped {
            result += "\n\n(Output budget reached at \(collect.files.count) entries. Narrow the glob pattern or path.)"
        }
        if collect.omittedSensitive > 0 {
            result += "\n(\(collect.omittedSensitive) 个敏感文件条目已省略。如需访问请在客户端设置中添加豁免。)"
        }
        let capped = cappedOutput(result, budget: budget, continueHint: "考虑缩小 glob 范围")
        return LocalToolResult(serverResult: OutputSanitizer.sanitize(capped.text).data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private struct GlobCollect {
        var files: [String] = []
        var omittedSensitive = 0
        var truncated = false   // 收满 maxResults
        var budgetCapped = false // 预算安全网掐断（未收满）
    }

    private func runGlob(pattern: String, cwd: String) async -> GlobCollect {
        if let rgPath = findRgBinary() {
            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: rgPath)
            // -j1 单线程：输出顺序确定（与 grep 同要求），预算为纯安全网
            proc.arguments = ["--no-config", "--files", "-j1", "--glob=\(pattern)", "--glob=!**/.git/**", "."]
            proc.currentDirectoryURL = URL(fileURLWithPath: cwd)

            var collect = GlobCollect()
            let capped = await runCapturedProcessLines(proc, budget: collectionSafetyBudget) { line in
                let cleaned = line
                    .trimmingCharacters(in: .whitespaces)
                    .replacingOccurrences(of: #"^\.\/+"#, with: "", options: NSString.CompareOptions.regularExpression)
                    .replacingOccurrences(of: "\\", with: "/")
                guard !cleaned.isEmpty else { return true }
                let absPath = "\(cwd)/\(cleaned)"
                if SensitiveFileBlocker.check(absPath) != nil {
                    collect.omittedSensitive += 1
                    return true
                }
                if collect.files.count >= self.maxResults {
                    collect.truncated = true
                    return false
                }
                collect.files.append("\(absPath)\t\(self.fileSize(absPath))")
                return true
            }
            if capped && !collect.truncated {
                collect.budgetCapped = true
            }
            return collect
        }

        return await runFindGlob(pattern: pattern, cwd: cwd)
    }

    private func runFindGlob(pattern: String, cwd: String) async -> GlobCollect {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/find")
        proc.arguments = [
            cwd, "-name", pattern,
            "-not", "-path", "*/.git/*",
            "-not", "-path", "*/node_modules/*",
            "-type", "f",
            "-maxdepth", "20",
        ]

        var collect = GlobCollect()
        let capped = await runCapturedProcessLines(proc, budget: collectionSafetyBudget) { line in
            let absPath = line.trimmingCharacters(in: .whitespaces)
            guard !absPath.isEmpty else { return true }
            if SensitiveFileBlocker.check(absPath) != nil {
                collect.omittedSensitive += 1
                return true
            }
            if collect.files.count >= self.maxResults {
                collect.truncated = true
                return false
            }
            collect.files.append("\(absPath)\t\(self.fileSize(absPath))")
            return true
        }
        if capped && !collect.truncated {
            collect.budgetCapped = true
        }
        return collect
    }

    /// 文件字节数；stat 失败记 0。
    private func fileSize(_ path: String) -> UInt64 {
        (try? FileManager.default.attributesOfItem(atPath: path)[.size]) as? UInt64 ?? 0
    }

    func findRgBinary() -> String? {
        let candidates = [
            "/opt/homebrew/bin/rg",
            "/usr/local/bin/rg",
            "/usr/bin/rg",
        ]
        for path in candidates {
            if FileManager.default.isExecutableFile(atPath: path) {
                return path
            }
        }
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/which")
        proc.arguments = ["rg"]
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = Pipe()
        do {
            try proc.run()
            proc.waitUntilExit()
            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            if let path = String(data: data, encoding: .utf8)?.trimmingCharacters(in: .whitespacesAndNewlines), !path.isEmpty {
                return path
            }
        } catch {}
        return nil
    }
}
#endif
