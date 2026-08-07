#if os(macOS)
import Foundation

final class FileSystemToolProvider: @unchecked Sendable {
    let namespace = "fs"

    let maxResults = 100
    let maxReadBytes = 50 * 1024
    let maxLineChars = 2000
    let defaultReadLimit = 2000
}

extension FileSystemToolProvider: LocalToolProvider {

    func toolSchemas() -> [LocalToolSchema] {
        [
            LocalToolSchema(
                name: "glob",
                description: """
                快速文件模式匹配工具。
                - 支持 glob 模式如 "**/*.go" 或 "src/**/*.ts"
                - 返回匹配的文件绝对路径列表，最多 100 条
                - 自动尊重 .gitignore
                - 当你需要按文件名模式查找文件时使用本工具
                - 你不确定文件确切路径时，先用 glob 找到文件，再用 read 读取
                - 你可以同时调用多个工具，批量 speculative 搜索通常更高效
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "pattern": [
                            "type": "string",
                            "description": "Glob 模式，如 **/*.go 或 src/**/*.ts",
                        ],
                        "path": [
                            "type": "string",
                            "description": "搜索起始目录，必须为绝对路径",
                        ],
                    ],
                    "required": ["pattern", "path"],
                ],
                isReadOnly: true
            ),
            LocalToolSchema(
                name: "grep",
                description: """
                文件内容正则搜索工具。
                - 搜索文件内容，支持完整正则语法（如 "log.*Error"、"func\\s+\\w+"）
                - 通过 include 参数过滤文件类型（如 "*.go"、"*.{ts,tsx}"）
                - 返回文件路径和行号，附带匹配行内容，最多 100 条
                - 当你需要在文件中定位特定内容时使用本工具——先用 grep 找到位置，再用 read 读上下文
                - 对于大文件或长行的文件，用 grep 定位比直接 read 更高效
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "pattern": [
                            "type": "string",
                            "description": "正则表达式，如 log.*Error 或 func\\s+\\w+",
                        ],
                        "path": [
                            "type": "string",
                            "description": "搜索起始目录，必须为绝对路径",
                        ],
                        "include": [
                            "type": "string",
                            "description": "文件类型过滤，如 *.go 或 *.{ts,tsx}。可选。",
                        ],
                    ],
                    "required": ["pattern", "path"],
                ],
                isReadOnly: true
            ),
            LocalToolSchema(
                name: "read",
                description: """
                读取本地文件系统中的文件或目录。路径不存在时返回错误。

                用法：
                - filePath 必须是绝对路径。
                - 默认从文件开头返回最多 2000 行。
                - offset 参数是起始行号（1-indexed）。
                - 读取后续内容时，用更大的 offset 再次调用。
                - 需要在大文件或长行文件中定位内容时，先用 grep 找到位置。
                - 不确定文件路径时，先用 glob 查找。
                - 文件内容返回格式：每行前加行号，如 "1: package main\\n2:\\n3: import (\\n"
                - 目录返回格式：每行一条条目（无行号），目录名末尾带 /
                - 单行超过 2000 字符会被截断。
                - 可以同时读取多个文件（并行调用）。
                - 避免小块切割读取（30 行一读）；需要更多上下文时就一次读更大的范围。
                - 可以读取图片（JPEG/PNG/GIF/WebP）和 PDF 并以 base64 格式返回。
                首次访问某个目录时会请求用户授权，授权后一小时内无需重复确认。
                """,
                inputSchema: [
                    "type": "object",
                    "properties": [
                        "filePath": [
                            "type": "string",
                            "description": "文件或目录的绝对路径",
                        ],
                        "offset": [
                            "type": "integer",
                            "description": "起始行号（1-indexed），默认 1",
                        ],
                        "limit": [
                            "type": "integer",
                            "description": "最大行数，默认 2000",
                        ],
                    ],
                    "required": ["filePath"],
                ],
                isReadOnly: true
            ),
        ]
    }

    static func extractPaths(flatName: String, arguments: [String: Any]) -> [String] {
        var paths: [String] = []
        if let p = arguments["path"] as? String, !p.isEmpty {
            paths.append(p)
        }
        if let fp = arguments["filePath"] as? String, !fp.isEmpty {
            paths.append(fp)
        }
        return paths.map { ($0 as NSString).expandingTildeInPath }
    }

    func execute(tool: String, arguments: [String: Any], context: ExecuteContext) async throws -> LocalToolResult {
        switch tool {
        case "glob":
            return try await handleGlob(arguments: arguments, context: context)
        case "grep":
            return try await handleGrep(arguments: arguments, context: context)
        case "read":
            return try await handleRead(arguments: arguments, context: context)
        default:
            return LocalToolResult(serverResult: nil, serverError: "未知函数: \(tool)", clientAction: nil)
        }
    }
}
#endif
