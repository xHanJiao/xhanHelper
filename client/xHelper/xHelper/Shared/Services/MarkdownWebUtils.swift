import Foundation

/// markdown → HTML 两侧共用的零件：拼 HTML 时的转义工具 + 打包的 marked.min.js。
/// 使用方：`DocumentPDFExporter`（打印/A4）与 `DocumentReaderView`（屏幕阅读）。
enum MarkdownWebUtils {

    /// HTML 文本转义。用于要直接拼进 HTML 的字符串（标题、标签）。
    static func escapeHTML(_ s: String) -> String {
        s.replacingOccurrences(of: "&", with: "&amp;")
            .replacingOccurrences(of: "<", with: "&lt;")
            .replacingOccurrences(of: ">", with: "&gt;")
            .replacingOccurrences(of: "\"", with: "&quot;")
            .replacingOccurrences(of: "'", with: "&#39;")
    }

    /// JS 字符串字面量。JSONEncoder 会转义 `/`（连带保障 `</script>` 不会提前闭合），
    /// 另外补 U+2028 / U+2029 —— 它们在 JS 里是合法换行，直接内联会断行。
    static func jsString(_ s: String) -> String {
        let data = (try? JSONEncoder().encode(s)) ?? Data()
        return String(decoding: data, as: UTF8.self)
            .replacingOccurrences(of: "\u{2028}", with: "\\u2028")
            .replacingOccurrences(of: "\u{2029}", with: "\\u2029")
    }

    /// 打包的 marked.min.js 源码；资源不在 bundle 里返回 nil。
    static func markedJS() -> String? {
        guard let url = Bundle.main.url(forResource: "marked.min", withExtension: "js") else {
            print("[MarkdownWeb] marked.min.js not found in bundle; resources: \(Bundle.main.resourceURL?.path ?? "?")")
            return nil
        }
        return try? String(contentsOf: url, encoding: .utf8)
    }
}
