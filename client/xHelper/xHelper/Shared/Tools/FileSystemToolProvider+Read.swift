#if os(macOS)
import Foundation
import AppKit
import PDFKit

extension FileSystemToolProvider {

    // MARK: - 入口

    func handleRead(arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        guard let rawPath = arguments["filePath"] as? String, !rawPath.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "缺少 filePath 参数", clientAction: nil)
        }

        let resolvedPath = FileSystemToolProvider.resolvePath(rawPath)

        if let blocked = SensitiveFileBlocker.check(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: blocked, clientAction: nil)
        }

        // symlink 复检：解析后真实路径可能逃逸授权目录（原始路径已在 L3 确认过，这里双保险）
        if !DirectoryAuthorizationManager.shared.isAuthorized(resolvedPath) {
            return LocalToolResult(serverResult: nil, serverError: "路径不在已授权目录内（或 symlink 指向授权目录外）: \(resolvedPath)", clientAction: nil)
        }

        let fm = FileManager.default

        if !fm.fileExists(atPath: resolvedPath) {
            let suggestion = suggestNearbyFiles(path: resolvedPath)
            return LocalToolResult(serverResult: nil, serverError: suggestion, clientAction: nil)
        }

        var isDir: ObjCBool = false
        fm.fileExists(atPath: resolvedPath, isDirectory: &isDir)

        if isDir.boolValue {
            let result = readDirectory(resolvedPath, arguments: arguments, budgetBytes: context.budgetBytes)
            if result.serverError == nil { markRead(resolvedPath) }
            return result
        }

        let result = readFile(resolvedPath, arguments: arguments, budgetBytes: context.budgetBytes)
        if result.serverError == nil { markRead(resolvedPath) }
        return result
    }

    // MARK: - 目录

    private func readDirectory(_ path: String, arguments: [String: Any], budgetBytes: Int?) -> LocalToolResult {
        let offset = (arguments["offset"] as? Int) ?? 1
        let limit = (arguments["limit"] as? Int) ?? defaultReadLimit
        let budget = budgetCap(budgetBytes)

        do {
            let contents = try FileManager.default.contentsOfDirectory(atPath: path)
                .sorted { $0.localizedCompare($1) == .orderedAscending }

            var entries: [String] = []
            var omittedSensitive = 0
            for name in contents {
                let full = "\(path)/\(name)"
                var isDir: ObjCBool = false
                let isDirectory = FileManager.default.fileExists(atPath: full, isDirectory: &isDir) && isDir.boolValue
                // 敏感条目（.env/.git 等）不进列表（与 grep/glob 安检一致）
                if SensitiveFileBlocker.check(full) != nil {
                    omittedSensitive += 1
                    continue
                }
                entries.append(isDirectory ? "\(name)/" : name)
            }

            let total = entries.count
            let start = max(0, offset - 1)

            guard start < total else {
                var output = "<path>\(path)</path>\n"
                output += "<type>directory</type>\n"
                output += "<entries>\n"
                output += "(0 entries shown of \(total). Offset \(offset) out of range.)\n"
                if omittedSensitive > 0 {
                    output += "(\(omittedSensitive) 个敏感条目已隐藏。)\n"
                }
                output += "</entries>"
                return LocalToolResult(serverResult: output.data(using: .utf8), serverError: nil, clientAction: nil)
            }

            let end = min(start + limit, total)
            var sliced = Array(entries[start..<end])

            // 字节预算：超出时截断条目列表
            var bytes = 0
            var byteCut = false
            for (i, e) in sliced.enumerated() {
                bytes += e.utf8.count + 1
                if bytes > budget {
                    sliced = Array(sliced[0..<i])
                    byteCut = true
                    break
                }
            }

            let truncated = end < total || byteCut

            var output = "<path>\(path)</path>\n"
            output += "<type>directory</type>\n"
            output += "<entries>\n"
            output += sliced.joined(separator: "\n")
            output += "\n"

            if byteCut {
                let next = start + sliced.count + 1
                output += "(Output capped at \((budget + 1023) / 1024) KB. Showing \(sliced.count) of \(total) entries. Use offset=\(next) to continue.)"
            } else if truncated {
                output += "(Showing \(sliced.count) of \(total) entries. Use offset=\(end + 1) to continue.)"
            } else {
                output += "(\(total) entries)"
            }
            if omittedSensitive > 0 {
                output += "\n(\(omittedSensitive) 个敏感条目已隐藏。如需访问请在客户端设置中添加豁免。)"
            }
            output += "\n</entries>"

            return LocalToolResult(serverResult: output.data(using: .utf8), serverError: nil, clientAction: nil)
        } catch {
            return LocalToolResult(serverResult: nil, serverError: "读取目录失败: \(error.localizedDescription)", clientAction: nil)
        }
    }

    // MARK: - 文件分发

    private func readFile(_ path: String, arguments: [String: Any], budgetBytes: Int?) -> LocalToolResult {
        let ext = (path as NSString).pathExtension.lowercased()

        if isBinaryExtension(ext) {
            return LocalToolResult(serverResult: nil, serverError: "Cannot read binary file: \(path)", clientAction: nil)
        }

        let fm = FileManager.default
        let attrs = try? fm.attributesOfItem(atPath: path)
        let fileSize = (attrs?[.size] as? UInt64) ?? 0

        if isImageOrPDF(path: path, ext: ext) {
            if ext == "pdf" {
                return readPDFText(path, arguments: arguments, budgetBytes: budgetBytes)
            }
            return readMediaFile(path, ext: ext, fileSize: fileSize)
        }

        let offset = (arguments["offset"] as? Int) ?? 1
        let limit = (arguments["limit"] as? Int) ?? defaultReadLimit
        let isHTML = ext == "html" || ext == "htm"

        return readTextFile(path, offset: offset, limit: limit, fileSize: fileSize, isHTML: isHTML, budgetBytes: budgetBytes)
    }

    // MARK: - 文本（UTF-8 流式 / 非 UTF-8 全量解码）

    private func readTextFile(_ path: String, offset: Int, limit: Int, fileSize: UInt64, isHTML: Bool, budgetBytes: Int?) -> LocalToolResult {
        guard offset >= 1 else {
            return LocalToolResult(serverResult: nil, serverError: "offset 必须 >= 1", clientAction: nil)
        }

        let budget = budgetCap(budgetBytes)

        // 编码检测：BOM / UTF-8 校验 / 非 UTF-8（GB18030）
        guard let fh = FileHandle(forReadingAtPath: path) else {
            return LocalToolResult(serverResult: nil, serverError: "无法打开文件: \(path)", clientAction: nil)
        }
        defer { try? fh.close() }

        let probe = (try? fh.read(upToCount: 64 * 1024)) ?? Data()

        let encoding = FileSystemToolProvider.detectEncoding(probe)
        try? fh.seek(toOffset: 0)

        switch encoding {
        case .utf8:
            return readUTF8Text(path, fileHandle: fh, offset: offset, limit: limit, isHTML: isHTML, budget: budget)
        case .nonUTF8:
            // 非 UTF-8（GB18030 等）：全量解码，需整文件读入
            if fileSize > UInt64(maxDecodeBytes) {
                return LocalToolResult(serverResult: nil, serverError: "文件非 UTF-8 编码且大于 \(maxDecodeBytes / 1024 / 1024)MB，无法解码。请转换为 UTF-8 后再读。", clientAction: nil)
            }
            guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else {
                return LocalToolResult(serverResult: nil, serverError: "无法读取文件: \(path)", clientAction: nil)
            }
            guard let decoded = FileSystemToolProvider.decodeNonUTF8(data) else {
                return LocalToolResult(serverResult: nil, serverError: "无法识别文件编码（非 UTF-8 且非 GB18030/UTF-16）: \(path)", clientAction: nil)
            }
            return windowText(decoded.text, offset: offset, limit: limit, isHTML: isHTML, budget: budget, encodingLabel: decoded.label)
        case .utf16LE, .utf16BE:
            if fileSize > UInt64(maxDecodeBytes) {
                return LocalToolResult(serverResult: nil, serverError: "文件为 UTF-16 编码且大于 \(maxDecodeBytes / 1024 / 1024)MB，无法解码。请转换为 UTF-8 后再读。", clientAction: nil)
            }
            guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)) else {
                return LocalToolResult(serverResult: nil, serverError: "无法读取文件: \(path)", clientAction: nil)
            }
            let enc: String.Encoding = (encoding == .utf16LE) ? .utf16LittleEndian : .utf16BigEndian
            guard let text = String(data: data, encoding: enc) else {
                return LocalToolResult(serverResult: nil, serverError: "UTF-16 解码失败: \(path)", clientAction: nil)
            }
            return windowText(text, offset: offset, limit: limit, isHTML: isHTML, budget: budget, encodingLabel: "UTF-16")
        }
    }

    enum DetectedEncoding {
        case utf8
        case nonUTF8
        case utf16LE
        case utf16BE
    }

    static func detectEncoding(_ probe: Data) -> DetectedEncoding {
        if probe.isEmpty { return .utf8 }

        let bytes = [UInt8](probe.prefix(4))
        if bytes.count >= 3, bytes[0] == 0xEF, bytes[1] == 0xBB, bytes[2] == 0xBF {
            return .utf8
        }
        if bytes.count >= 2, bytes[0] == 0xFF, bytes[1] == 0xFE {
            return .utf16LE
        }
        if bytes.count >= 2, bytes[0] == 0xFE, bytes[1] == 0xFF {
            return .utf16BE
        }

        // 无 BOM 的 UTF-16：null 字节位置启发（LE 的 ASCII 高字节在奇数位，BE 在偶数位）
        let sample = probe.prefix(64 * 1024)
        var nulls = 0
        var nullAtOdd = 0
        for (i, b) in sample.enumerated() {
            if b == 0 {
                nulls += 1
                if i % 2 == 1 { nullAtOdd += 1 }
            }
        }
        if nulls >= 8 {
            let ratio = Double(nullAtOdd) / Double(nulls)
            if ratio >= 0.8 { return .utf16LE }
            if ratio <= 0.2 { return .utf16BE }
        }

        // UTF-8：先去尾部 1-3 字节（64KB 截断可能切在多字节字符中间）
        var count = sample.count
        while count > 0 {
            if String(data: sample.prefix(count), encoding: .utf8) != nil {
                return .utf8
            }
            count -= 1
            if sample.count - count > 3 { break }
        }
        return .nonUTF8
    }

    static func decodeNonUTF8(_ data: Data) -> (text: String, label: String)? {
        // GB18030（含 GBK 超集）
        let gb18030 = CFStringEncoding(CFStringEncodings.GB_18030_2000.rawValue)
        let nsEncoding = CFStringConvertEncodingToNSStringEncoding(gb18030)
        let encoding = String.Encoding(rawValue: nsEncoding)
        if let text = String(data: data, encoding: encoding) {
            return (text, "GB18030")
        }
        if let text = String(data: data, encoding: .utf16LittleEndian) {
            return (text, "UTF-16")
        }
        if let text = String(data: data, encoding: .utf16BigEndian) {
            return (text, "UTF-16")
        }
        return nil
    }

    /// 全量解码后的文本按行窗口输出。
    private func windowText(_ text: String, offset: Int, limit: Int, isHTML: Bool, budget: Int, encodingLabel: String) -> LocalToolResult {
        var lines: [String] = []
        var inBlock = false
        var byteCount = 0
        var lineCount = 0
        var cut = false
        var more = false

        var source = text
        if source.hasPrefix("\u{FEFF}") {
            source.removeFirst()
        }
        var allLines = source.components(separatedBy: "\n")
        if source.hasSuffix("\n") {
            allLines.removeLast()
        }

        for rawLine in allLines {
            lineCount += 1
            if lineCount < offset { continue }
            if lines.count >= limit {
                more = true
                break
            }
            var line = rawLine
            if line.hasSuffix("\r") {
                line = String(line.dropLast())
            }
            if isHTML {
                line = stripScriptStyle(line, inBlock: &inBlock)
            }
            if line.utf8.count > maxLineChars {
                line = String(line.prefix(maxLineChars)) + "... (line truncated to \(maxLineChars) chars)"
            }
            let lineBytes = line.utf8.count + 1
            if byteCount + lineBytes > budget {
                cut = true
                more = true
                break
            }
            byteCount += lineBytes
            lines.append(line)
        }

        return renderWindow(lines: lines, offset: offset, lineCount: lineCount, cut: cut, more: more, budget: budget, encodingLabel: encodingLabel)
    }

    /// UTF-8 流式读取（兼容大文件）。
    private func readUTF8Text(_ path: String, fileHandle fh: FileHandle, offset: Int, limit: Int, isHTML: Bool, budget: Int) -> LocalToolResult {
        var lines: [String] = []
        var lineCount = 0
        var totalBytes = 0
        var cut = false
        var more = false
        var inBlock = false

        let chunkSize = 4096
        var trailing = Data()
        var lineFragment = ""
        var firstChunk = true

        while let chunk = try? fh.read(upToCount: chunkSize) {
            if chunk.isEmpty { break }

            var combined = trailing + chunk
            trailing = Data()

            if firstChunk {
                firstChunk = false
                if combined.count >= 3,
                   combined[0] == 0xEF, combined[1] == 0xBB, combined[2] == 0xBF {
                    combined = combined.dropFirst(3)
                }
            }

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
            var count = hasIncomplete ? parts.count - 1 : parts.count
            // 文件以 \n 结尾时 components 多出尾部空串，不计为一行
            if !hasIncomplete, parts.last == "" {
                count -= 1
            }
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
                    try? fh.close()
                    return LocalToolResult(serverResult: nil, serverError: "Cannot read binary file: \(path)", clientAction: nil)
                }

                if line.hasSuffix("\r") {
                    line = String(line.dropLast())
                }

                if isHTML {
                    line = stripScriptStyle(line, inBlock: &inBlock)
                }

                if line.utf8.count > maxLineChars {
                    line = String(line.prefix(maxLineChars)) + "... (line truncated to \(maxLineChars) chars)"
                }

                let lineBytes = line.utf8.count + 1
                if totalBytes + lineBytes > budget {
                    cut = true
                    more = true
                    break
                }

                totalBytes += lineBytes
                lines.append(line)
            }

            if totalBytes >= budget || more { break }

            if isBinaryChunk(chunk) {
                try? fh.close()
                return LocalToolResult(serverResult: nil, serverError: "Cannot read binary file: \(path)", clientAction: nil)
            }
        }

        if !lineFragment.isEmpty {
            lineCount += 1
            if lineCount >= offset, lines.count < limit, totalBytes < budget {
                var line = lineFragment
                if line.hasSuffix("\r") {
                    line = String(line.dropLast())
                }
                if isHTML {
                    line = stripScriptStyle(line, inBlock: &inBlock)
                }
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

        return renderWindow(lines: lines, offset: offset, lineCount: lineCount, cut: cut, more: more, budget: budget, encodingLabel: "UTF-8")
    }

    private func renderWindow(lines: [String], offset: Int, lineCount: Int, cut: Bool, more: Bool, budget: Int, encodingLabel: String) -> LocalToolResult {
        var output = ""
        for (i, line) in lines.enumerated() {
            output += "\(offset + i): \(line)\n"
        }

        if cut {
            let lastLine = offset + lines.count - 1
            let nextOffset = offset + lines.count
            output += "(Output capped at \((budget + 1023) / 1024) KB. Showing lines \(offset)-\(lastLine). Use offset=\(nextOffset) to continue.)"
        } else if more {
            let lastLine = offset + lines.count - 1
            let nextOffset = offset + lines.count
            output += "(Showing lines \(offset)-\(lastLine) of \(lineCount). Use offset=\(nextOffset) to continue.)"
        } else {
            output += "(End of file - total \(lineCount) lines)"
        }
        output += " (encoding: \(encodingLabel))"

        return LocalToolResult(serverResult: OutputSanitizer.sanitize(output).data(using: .utf8), serverError: nil, clientAction: nil)
    }

    // MARK: - HTML script/style 剥离（保行号）

    /// 逐行剥离 <script>/<style> 块内容，用占位标记替换。
    /// inBlock 跨行保持状态；块内每行只输出占位标记（行号不位移）。
    private func stripScriptStyle(_ line: String, inBlock: inout Bool) -> String {
        var result = ""
        var idx = line.startIndex
        var replacedOnce = false
        let marker = "<!-- [fs] stripped -->"

        func nextTag(from index: String.Index, tags: [String]) -> Range<String.Index>? {
            for tag in tags {
                if let r = line.range(of: tag, options: [.caseInsensitive], range: index..<line.endIndex) {
                    return r
                }
            }
            return nil
        }

        while idx < line.endIndex {
            if inBlock {
                if let close = nextTag(from: idx, tags: ["</script>", "</style>"]) {
                    if !replacedOnce {
                        result += marker
                        replacedOnce = true
                    }
                    idx = line.index(close.upperBound, offsetBy: 0)
                    inBlock = false
                } else {
                    if !replacedOnce {
                        result += marker
                        replacedOnce = true
                    }
                    idx = line.endIndex
                }
            } else {
                guard let open = nextTag(from: idx, tags: ["<script", "<style"]) else {
                    result += String(line[idx...])
                    idx = line.endIndex
                    continue
                }
                result += String(line[idx..<open.lowerBound])

                // 找到开标签的结束 >（可能有属性）
                var scan = open.lowerBound
                if let gt = line.range(of: ">", range: scan..<line.endIndex) {
                    scan = gt.upperBound
                } else {
                    // 开标签跨行未完：从下一行起算 inBlock
                    idx = line.endIndex
                    inBlock = true
                    if !replacedOnce {
                        result += marker
                        replacedOnce = true
                    }
                    continue
                }

                // 找同行的闭合标签
                let closeTags = ["</script>", "</style>"]
                if let close = nextTag(from: scan, tags: closeTags) {
                    if !replacedOnce {
                        result += marker
                        replacedOnce = true
                    }
                    idx = close.upperBound
                } else {
                    result += marker
                    replacedOnce = true
                    idx = line.endIndex
                    inBlock = true
                }
            }
        }

        return result
    }

    // MARK: - 媒体（图片 base64）/ PDF（文本提取）

    private func readMediaFile(_ path: String, ext: String, fileSize: UInt64) -> LocalToolResult {
        let maxMediaBytes: UInt64 = 20 * 1024 * 1024
        if fileSize > maxMediaBytes {
            return LocalToolResult(serverResult: nil, serverError: "图片文件过大 (\(fileSize / 1024 / 1024)MB > 20MB)", clientAction: nil)
        }

        guard let data = try? Data(contentsOf: URL(fileURLWithPath: path)),
              !data.isEmpty else {
            return LocalToolResult(serverResult: nil, serverError: "无法读取文件: \(path)", clientAction: nil)
        }

        let mime = imageMimeType(path: path, ext: ext, data: data)
        let base64 = data.base64EncodedString()

        return LocalToolResult(
            serverResult: "Image read successfully\n[data:\(mime);base64,\(base64)]".data(using: .utf8),
            serverError: nil,
            clientAction: nil
        )
    }

    private func readPDFText(_ path: String, arguments: [String: Any], budgetBytes: Int?) -> LocalToolResult {
        guard let doc = PDFDocument(url: URL(fileURLWithPath: path)) else {
            return LocalToolResult(serverResult: nil, serverError: "无法打开 PDF: \(path)", clientAction: nil)
        }
        if doc.isLocked {
            return LocalToolResult(serverResult: nil, serverError: "PDF 已加密，无法提取文本: \(path)", clientAction: nil)
        }

        let totalPages = doc.pageCount
        let pageOffset = (arguments["page_offset"] as? Int) ?? 1
        let pageLimit = (arguments["page_limit"] as? Int) ?? 10

        guard pageOffset >= 1 else {
            return LocalToolResult(serverResult: nil, serverError: "page_offset 必须 >= 1", clientAction: nil)
        }
        guard pageLimit >= 1 else {
            return LocalToolResult(serverResult: nil, serverError: "page_limit 必须 >= 1", clientAction: nil)
        }

        let start = pageOffset
        let end = min(start + pageLimit - 1, totalPages)
        let budget = budgetCap(budgetBytes)

        var output = ""
        var byteCount = 0
        var emptyPages = 0
        var cut = false

        if start > totalPages {
            return LocalToolResult(serverResult: nil, serverError: "page_offset \(start) out of range (total \(totalPages) pages)", clientAction: nil)
        }

        for page in start...end {
            let header = "[Page \(page) of \(totalPages)]"
            var pageText = doc.page(at: page - 1)?.string ?? ""
            if pageText.isEmpty { emptyPages += 1 }
            pageText = pageText.trimmingCharacters(in: .whitespacesAndNewlines)

            let block = header + "\n" + pageText + "\n"
            if byteCount + block.utf8.count > budget {
                cut = true
                break
            }
            byteCount += block.utf8.count
            output += block
        }

        var suffix = ""
        if cut {
            let nextPage = end + 1
            suffix = "(Output capped at \((budget + 1023) / 1024) KB. Use page_offset=\(nextPage) to continue.)"
        } else if end < totalPages {
            suffix = "(Showing pages \(start)-\(end) of \(totalPages). Use page_offset=\(end + 1) to continue.)"
        } else {
            suffix = "(End of PDF - total \(totalPages) pages)"
        }
        output += suffix

        if emptyPages * 2 >= (end - start + 1) {
            return LocalToolResult(serverResult: nil, serverError: "PDF 无文本层（可能是扫描件），无法提取文本: \(path)", clientAction: nil)
        }

        return LocalToolResult(serverResult: output.data(using: .utf8), serverError: nil, clientAction: nil)
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

    private func imageMimeType(path: String, ext: String, data: Data) -> String {
        let bytes = [UInt8](data.prefix(12))
        if bytes.starts(with: [0xFF, 0xD8, 0xFF]) { return "image/jpeg" }
        if bytes.starts(with: [0x89, 0x50, 0x4E, 0x47]) { return "image/png" }
        if bytes.starts(with: [0x47, 0x49, 0x46, 0x38]) { return "image/gif" }
        if bytes.count >= 12,
           bytes[0] == 0x52, bytes[1] == 0x49, bytes[2] == 0x46, bytes[3] == 0x46,
           bytes[8] == 0x57, bytes[9] == 0x45, bytes[10] == 0x42, bytes[11] == 0x50 { return "image/webp" }
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
}
#endif
