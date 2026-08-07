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

        let resolvedPath = resolvePath(rawPath)
        var isDir: ObjCBool = false
        let exists = FileManager.default.fileExists(atPath: resolvedPath, isDirectory: &isDir)

        guard exists else {
            return LocalToolResult(serverResult: nil, serverError: "路径不存在: \(resolvedPath)", clientAction: nil)
        }

        if let blocked = SensitiveFileBlocker.check(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }

        let include = arguments["include"] as? String

        let matches = runGrep(pattern: pattern, path: resolvedPath, include: include, isDir: isDir.boolValue)
        let output = formatGrepOutput(matches: matches, pattern: pattern)
        let sanitized = OutputSanitizer.sanitize(output)
        return LocalToolResult(serverResult: sanitized.data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private func runGrep(pattern: String, path: String, include: String?, isDir: Bool) -> [GrepMatch] {
        if let rgPath = findRgBinary() {
            return runRgGrep(rgPath: rgPath, pattern: pattern, path: path, include: include, isDir: isDir)
        }
        return runSystemGrep(pattern: pattern, path: path, include: include, isDir: isDir)
    }

    private func runRgGrep(rgPath: String, pattern: String, path: String, include: String?, isDir: Bool) -> [GrepMatch] {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: rgPath)

        var args = ["--no-config", "--json", "--hidden", "--no-messages", "--glob=!**/.git/**"]
        if let inc = include {
            args.append("--glob=\(inc)")
        }
        args.append("--")
        args.append(pattern)

        if isDir {
            args.append(path)
        } else {
            args.append(path)
        }

        proc.arguments = args

        let outPipe = Pipe()
        proc.standardOutput = outPipe
        proc.standardError = Pipe()

        do {
            try proc.run()
            proc.waitUntilExit()

            let data = outPipe.fileHandleForReading.readDataToEndOfFile()
            return parseRgJSON(data)
        } catch {
            LogBuffer.shared.warning("fs__grep rg failed: \(error.localizedDescription)")
            return []
        }
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

    private func runSystemGrep(pattern: String, path: String, include: String?, isDir: Bool) -> [GrepMatch] {
        let proc = Process()
        proc.executableURL = URL(fileURLWithPath: "/usr/bin/grep")
        var args = ["-rn", "--include=*"]
        if let inc = include {
            args = ["-rn", "--include=\(inc)"]
        }
        args.append(pattern)
        args.append(path)

        proc.arguments = args
        let pipe = Pipe()
        proc.standardOutput = pipe
        proc.standardError = Pipe()

        do {
            try proc.run()
            proc.waitUntilExit()

            let data = pipe.fileHandleForReading.readDataToEndOfFile()
            guard let text = String(data: data, encoding: .utf8) else { return [] }

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
        } catch {
            LogBuffer.shared.warning("fs__grep system grep failed: \(error.localizedDescription)")
            return []
        }
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
