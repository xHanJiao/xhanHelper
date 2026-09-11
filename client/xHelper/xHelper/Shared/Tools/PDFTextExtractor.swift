#if os(macOS)
import Foundation
import PDFKit

/// PDFTextExtractor — PDF 文本层按页提取（app target，PDFKit 依赖）。
/// 纯匹配逻辑在 FsCore 的 PDFGrepMatcher，此处只负责取文本。
enum PDFTextExtractor {
    /// 返回每页文本；页码 = 数组下标 + 1。
    /// - raw=true 时跳过页眉/页脚清洗，返回 `page.string` 原样（诊断用）。
    /// - 无法打开 → cannotOpen；加密 → encrypted；无文本层（扫描件）→ noTextLayer。
    static func pageTexts(path: String, raw: Bool = false) throws -> [String] {
        guard let doc = PDFDocument(url: URL(fileURLWithPath: path)) else {
            throw PDFExtractError.cannotOpen(path)
        }
        if doc.isLocked {
            throw PDFExtractError.encrypted(path)
        }

        let total = doc.pageCount
        var pages: [String] = []
        pages.reserveCapacity(total)
        var nonEmpty = 0
        for i in 0..<total {
            let text = doc.page(at: i)?.string ?? ""
            if !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty {
                nonEmpty += 1
            }
            pages.append(text)
        }
        if total == 0 || nonEmpty == 0 {
            throw PDFExtractError.noTextLayer(path)
        }
        if raw { return pages }
        // 清洗页眉/页脚（先按原始文本判 noTextLayer，避免整页仅页码时误判）
        return PDFPageCleaner.clean(pages: pages)
    }
}

/// PDFExtractError — PDF 文本提取错误（errorDescription 直接作为 serverError）。
enum PDFExtractError: LocalizedError {
    case cannotOpen(String)
    case encrypted(String)
    case noTextLayer(String)

    var errorDescription: String? {
        switch self {
        case .cannotOpen(let p): return "无法打开 PDF: \(p)"
        case .encrypted(let p): return "PDF 已加密，无法提取文本: \(p)"
        case .noTextLayer(let p): return "PDF 无文本层（可能是扫描件），无法检索: \(p)"
        }
    }
}
#endif
