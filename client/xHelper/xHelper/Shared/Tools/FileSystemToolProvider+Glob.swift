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

        let resolvedPath = resolvePath(rawPath)
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

        let files = runGlob(pattern: pattern, cwd: resolvedPath)
        if files.isEmpty {
            let encoded = try? JSONSerialization.data(withJSONObject: ["files": [], "count": 0, "pattern": pattern])
            return LocalToolResult(serverResult: OutputSanitizer.sanitize(String(data: encoded ?? Data(), encoding: .utf8) ?? "").data(using: .utf8), serverError: nil, clientAction: nil)
        }

        let output = files.joined(separator: "\n")
        let truncated = files.count >= maxResults
        var result = output
        if truncated {
            result += "\n\n(Results truncated: showing first \(maxResults). Consider using a more specific pattern.)"
        }
        let budget = budgetCap(context.budgetBytes)
        let capped = cappedOutput(result, budget: budget, continueHint: "考虑缩小 glob 范围")
        result = capped.text
        let encoded = try? JSONSerialization.data(withJSONObject: ["files": files, "count": files.count, "truncated": truncated, "pattern": pattern])
        return LocalToolResult(serverResult: OutputSanitizer.sanitize(result).data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private func runGlob(pattern: String, cwd: String) -> [String] {
        if let rgPath = findRgBinary() {
            let proc = Process()
            proc.executableURL = URL(fileURLWithPath: rgPath)
            proc.arguments = ["--no-config", "--files", "--glob=\(pattern)", "--glob=!**/.git/**", "."]
            proc.currentDirectoryURL = URL(fileURLWithPath: cwd)

            let pipe = Pipe()
            proc.standardOutput = pipe
            proc.standardError = Pipe()

            do {
                try proc.run()
                proc.waitUntilExit()

                let data = pipe.fileHandleForReading.readDataToEndOfFile()
                guard let text = String(data: data, encoding: .utf8) else { return [] }
                let lines = text.components(separatedBy: "\n")
                    .map { $0.trimmingCharacters(in: .whitespaces) }
                    .filter { !$0.isEmpty }
                    .prefix(maxResults)
                return lines.map { line in
                    let cleaned = line
                        .replacingOccurrences(of: #"^\.\/+"#, with: "", options: NSString.CompareOptions.regularExpression)
                        .replacingOccurrences(of: "\\", with: "/")
                    return "\(cwd)/\(cleaned)"
                }
            } catch {
                LogBuffer.shared.warning("fs__glob rg failed: \(error.localizedDescription)")
            }
        }

        return runFindGlob(pattern: pattern, cwd: cwd)
    }

    private func runFindGlob(pattern: String, cwd: String) -> [String] {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/find")
        proc.arguments = [
            cwd, "-name", pattern,
            "-not", "-path", "*/.git/*",
            "-not", "-path", "*/node_modules/*",
            "-type", "f",
            "-maxdepth", "20",
        ]
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = Pipe()

        do {
            try proc.run()
            proc.waitUntilExit()

            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            guard let text = String(data: data, encoding: .utf8) else { return [] }
            return text.components(separatedBy: "\n")
                .map { $0.trimmingCharacters(in: .whitespaces) }
                .filter { !$0.isEmpty }
                .prefix(maxResults)
                .map { $0 }
        } catch {
            LogBuffer.shared.warning("fs__glob find failed: \(error.localizedDescription)")
            return []
        }
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
