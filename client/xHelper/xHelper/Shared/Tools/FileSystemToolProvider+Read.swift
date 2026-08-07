#if os(macOS)
import Foundation
import AppKit

extension FileSystemToolProvider {

    func handleRead(arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard let rawPath = arguments["filePath"] as? String, !rawPath.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 filePath 参数", clientAction: nil)
        }

        let resolvedPath = resolvePath(rawPath)

        if let blocked = SensitiveFileBlocker.check(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }

        let fm = FileManager.default

        if !fm.fileExists(atPath: resolvedPath) {
            let suggestion = suggestNearbyFiles(path: resolvedPath)
            return LocalToolResult(serverResult: nil, serverError: suggestion, clientAction: nil)
        }

        var isDir: ObjCBool = false
        fm.fileExists(atPath: resolvedPath, isDirectory: &isDir)

        if isDir.boolValue {
            return readDirectory(resolvedPath, arguments: arguments)
        }

        return readFile(resolvedPath, arguments: arguments)
    }

    private func readDirectory(_ path: String, arguments: [String: Any]) -> LocalToolResult {
        let offset = (arguments["offset"] as? Int) ?? 1
        let limit = (arguments["limit"] as? Int) ?? defaultReadLimit

        do {
            let contents = try FileManager.default.contentsOfDirectory(atPath: path)
                .sorted { $0.localizedCompare($1) == .orderedAscending }

            var entries: [String] = []
            for name in contents {
                let full = "\(path)/\(name)"
                var isDir: ObjCBool = false
                let isDirectory = FileManager.default.fileExists(atPath: full, isDirectory: &isDir) && isDir.boolValue
                entries.append(isDirectory ? "\(name)/" : name)
            }

            let total = entries.count
            let start = max(0, offset - 1)
            let end = min(start + limit, total)
            let sliced = Array(entries[start..<end])
            let truncated = end < total

            var output = "<path>\(path)</path>\n"
            output += "<type>directory</type>\n"
            output += "<entries>\n"
            output += sliced.joined(separator: "\n")
            output += "\n"

            if truncated {
                output += "(Showing \(sliced.count) of \(total) entries. Use offset=\(end + 1) to continue.)"
            } else {
                output += "(\(total) entries)"
            }
            output += "\n</entries>"

            return LocalToolResult(serverResult: output.data(using: .utf8), serverError: nil, clientAction: nil)
        } catch {
            return LocalToolResult(serverResult: nil, serverError: "读取目录失败: \(error.localizedDescription)", clientAction: nil)
        }
    }

    private func readFile(_ path: String, arguments: [String: Any]) -> LocalToolResult {
        let offset = (arguments["offset"] as? Int) ?? 1
        let limit = (arguments["limit"] as? Int) ?? defaultReadLimit

        guard offset >= 1 else {
            return LocalToolResult(serverResult: nil, serverError: "offset 必须 >= 1", clientAction: nil)
        }

        let fm = FileManager.default
        let attrs = try? fm.attributesOfItem(atPath: path)
        let fileSize = (attrs?[.size] as? UInt64) ?? 0

        let ext = (path as NSString).pathExtension.lowercased()

        if isBinaryExtension(ext) {
            return LocalToolResult(serverResult: nil, serverError: "Cannot read binary file: \(path)", clientAction: nil)
        }

        if isImageOrPDF(path: path, ext: ext) {
            return readMediaFile(path, ext: ext, fileSize: fileSize)
        }

        return readTextFile(path, offset: offset, limit: limit, fileSize: fileSize)
    }

    private func readTextFile(_ path: String, offset: Int, limit: Int, fileSize: UInt64) -> LocalToolResult {
        guard let fileHandle = FileHandle(forReadingAtPath: path) else {
            return LocalToolResult(serverResult: nil, serverError: "无法打开文件: \(path)", clientAction: nil)
        }
        defer { try? fileHandle.close() }

        var lines: [String] = []
        var lineCount = 0
        var totalBytes = 0
        var cut = false
        var more = false

        let chunkSize = 4096
        var trailing = Data()
        var lineFragment = ""

        while let chunk = try? fileHandle.read(upToCount: chunkSize) {
            if chunk.isEmpty { break }

            var combined = trailing + chunk
            trailing = Data()

            var validLen = combined.count
            for i in 0...min(3, combined.count) {
                validLen = combined.count - i
                if String(data: combined.prefix(validLen), encoding: .utf8) != nil {
                    break
                }
            }
            trailing = combined.suffix(combined.count - validLen)
            let validData = combined.prefix(validLen)

            guard let text = String(data: validData, encoding: .utf8), !text.isEmpty else { continue }

            let raw = lineFragment + text
            let parts = raw.components(separatedBy: "\n")
            let hasIncomplete = !trailing.isEmpty || (validData.last != UInt8(ascii: "\n"))
            let count = hasIncomplete ? parts.count - 1 : parts.count
            lineFragment = hasIncomplete ? (parts.last ?? "") : ""

            for i in 0..<max(0, count) {
                lineCount += 1

                if lineCount < offset { continue }
                if lines.count >= limit {
                    more = true
                    break
                }

                var line = parts[i]
                let nullCheck = line.utf8
                if nullCheck.contains(0) {
                    try? fileHandle.close()
                    return LocalToolResult(serverResult: nil, serverError: "Cannot read binary file: \(path)", clientAction: nil)
                }

                if line.utf8.count > maxLineChars {
                    line = String(line.prefix(maxLineChars)) + "... (line truncated to \(maxLineChars) chars)"
                }

                let lineBytes = line.utf8.count + 1
                if totalBytes + lineBytes > maxReadBytes {
                    cut = true
                    more = true
                    break
                }

                totalBytes += lineBytes
                lines.append(line)
            }

            if totalBytes >= maxReadBytes || more { break }

            if isBinaryChunk(chunk) {
                try? fileHandle.close()
                return LocalToolResult(serverResult: nil, serverError: "Cannot read binary file: \(path)", clientAction: nil)
            }
        }

        if !lineFragment.isEmpty {
            lineCount += 1
            if lineCount >= offset, lines.count < limit, totalBytes < maxReadBytes {
                var line = lineFragment
                if line.utf8.count > maxLineChars {
                    line = String(line.prefix(maxLineChars)) + "... (line truncated to \(maxLineChars) chars)"
                }
                totalBytes += line.utf8.count + 1
                lines.append(line)
            }
        }

        if lines.isEmpty, lineCount == 0, offset == 1 {
            var output = ""
            output += "1: \n"
            output += "(End of file - total 1 lines)"
            return LocalToolResult(serverResult: OutputSanitizer.sanitize(output).data(using: .utf8), serverError: nil, clientAction: nil)
        }

        if lines.isEmpty {
            return LocalToolResult(serverResult: nil, serverError: "Offset \(offset) is out of range for this file (\(lineCount) lines)", clientAction: nil)
        }

        var output = ""
        for (i, line) in lines.enumerated() {
            output += "\(offset + i): \(line)\n"
        }

        if cut {
            let lastLine = offset + lines.count - 1
            let nextOffset = offset + lines.count
            output += "(Output capped at 50 KB. Showing lines \(offset)-\(lastLine). Use offset=\(nextOffset) to continue.)"
        } else if more {
            let lastLine = offset + lines.count - 1
            let nextOffset = offset + lines.count
            output += "(Showing lines \(offset)-\(lastLine) of \(lineCount). Use offset=\(nextOffset) to continue.)"
        } else {
            output += "(End of file - total \(lineCount) lines)"
        }

        return LocalToolResult(serverResult: OutputSanitizer.sanitize(output).data(using: .utf8), serverError: nil, clientAction: nil)
    }

    private func readMediaFile(_ path: String, ext: String, fileSize: UInt64) -> LocalToolResult {
        let maxMediaBytes: UInt64 = 20 * 1024 * 1024
        if fileSize > maxMediaBytes {
            return LocalToolResult(serverResult: nil, serverError: "媒体文件过大 (\(fileSize / 1024 / 1024)MB > 20MB)", clientAction: nil)
        }

        guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)),
              !data.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "无法读取文件: \(path)", clientAction: nil)
        }

        let mime = mimeType(path: path, ext: ext, data: data)
        let base64 = data.base64EncodedString()
        let msg = mime.hasPrefix("image/") ? "Image read successfully" : "PDF read successfully"

        return LocalToolResult(
            serverResult: "\(msg)\n[data:\(mime);base64,\(base64)]".data(using: .utf8),
            serverError: nil,
            clientAction: nil
        )
    }

    private func isImageOrPDF(path: String, ext: String) -> Bool {
        guard let fh = FileHandle(forReadingAtPath: path) else { return false }
        defer { try? fh.close() }
        guard let sample = try? fh.read(upToCount: 4096), sample.count >= 4 else { return false }

        let bytes = [UInt8](sample.prefix(12))

        if bytes.starts(with: [0xFF, 0xD8, 0xFF]) { return true }
        if bytes.starts(with: [0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A]) { return true }
        if bytes.starts(with: [0x47, 0x49, 0x46, 0x38]) { return true }
        if bytes.count >= 12,
           bytes[0] == 0x52, bytes[1] == 0x49, bytes[2] == 0x46, bytes[3] == 0x46,
           bytes[8] == 0x57, bytes[9] == 0x45, bytes[10] == 0x42, bytes[11] == 0x50 { return true }
        if bytes.starts(with: [0x25, 0x50, 0x44, 0x46]) { return true }

        return false
    }

    private func mimeType(path: String, ext: String, data: Data) -> String {
        let bytes = [UInt8](data.prefix(12))
        if bytes.starts(with: [0xFF, 0xD8, 0xFF]) { return "image/jpeg" }
        if bytes.starts(with: [0x89, 0x50, 0x4E, 0x47]) { return "image/png" }
        if bytes.starts(with: [0x47, 0x49, 0x46, 0x38]) { return "image/gif" }
        if bytes.count >= 12,
           bytes[0] == 0x52, bytes[1] == 0x49, bytes[2] == 0x46, bytes[3] == 0x46,
           bytes[8] == 0x57, bytes[9] == 0x45, bytes[10] == 0x42, bytes[11] == 0x50 { return "image/webp" }
        if bytes.starts(with: [0x25, 0x50, 0x44, 0x46]) { return "application/pdf" }
        return "application/octet-stream"
    }

    private func isBinaryExtension(_ ext: String) -> Bool {
        let blocked: Set<String> = [
            "zip", "tar", "gz", "xz", "bz2", "7z", "rar",
            "exe", "dll", "so", "dylib", "class", "jar", "war",
            "doc", "docx", "xls", "xlsx", "ppt", "pptx",
            "odt", "ods", "odp", "bin", "dat", "obj", "o", "a", "lib",
            "wasm", "pyc", "pyo",
        ]
        return blocked.contains(ext)
    }

    private func isBinaryChunk(_ chunk: Data) -> Bool {
        let sample = chunk.prefix(4096)
        guard !sample.isEmpty else { return false }
        var nonPrintable = 0
        for byte in sample {
            if byte == 0 { return true }
            if byte < 9 || (byte > 13 && byte < 32) {
                nonPrintable += 1
            }
        }
        return Double(nonPrintable) / Double(sample.count) > 0.3
    }

    private func suggestNearbyFiles(path: String) -> String {
        let dir = (path as NSString).deletingLastPathComponent
        let base = (path as NSString).lastPathComponent.lowercased()

        var suggestions: [String] = []
        if let entries = try? FileManager.default.contentsOfDirectory(atPath: dir) {
            for entry in entries {
                let lower = entry.lowercased()
                if lower.contains(base) || base.contains(lower) {
                    suggestions.append("\(dir)/\(entry)")
                }
            }
        }

        let prefix = "File not found: \(path)"
        if suggestions.isEmpty {
            return prefix
        }
        return "\(prefix)\n\nDid you mean one of these?\n\(suggestions.prefix(3).joined(separator: "\n"))"
    }

    func resolvePath(_ path: String) -> String {
        var resolved = (path as NSString).expandingTildeInPath
        resolved = (resolved as NSString).standardizingPath
        return resolved
    }
}
#endif
