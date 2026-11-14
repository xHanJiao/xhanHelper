#if os(macOS)
import Foundation

/// 书的事件（导入 / 阅读区间）。纯逻辑，不碰 UI；采集开关仍由 `ActivityClient.isEnabled` 决定。
///
/// 形态口径见 `docs/design/epub-reader/design.md` §八 与附表 A：区间事件共享 `pair_id`，
/// 失败只写 `outcome` / `error_type`，不新增 type。
nonisolated enum BookActivity {

    /// 一次导入的区间（started / ended 共享 `pair_id`）。
    struct ImportTrace {
        let pairId = UUID().uuidString
        let bytes: Int

        init(fileURL: URL) {
            let size = (try? FileManager.default.attributesOfItem(atPath: fileURL.path))?[.size] as? NSNumber
            if size == nil {
                // 读不到大小不挡导入（BookStore 里还会再读一次），但事件里的 bytes 是 0 要说清
                NSLog("[BookActivity] 读不到文件大小：\(fileURL.lastPathComponent)")
            }
            bytes = size?.intValue ?? 0
        }

        func started() -> ActivityEvent {
            ActivityEvent(
                type: "book.import.started", actor: "user", trigger: "manual",
                pairId: pairId,
                attrs: ["bytes": .number(Double(bytes)), "format": .string("epub")])
        }

        /// `chapters` 只有成功时才有意义；失败送 0。
        func ended(outcome: String, error: BookImportError?, bookID: String?, chapters: Int) -> ActivityEvent {
            var attrs: [String: JSONValue] = [
                "chapters": .number(Double(chapters)),
                "format": .string("epub"),
            ]
            if let bookID { attrs["book_id"] = .string(bookID) }
            return ActivityEvent(
                type: "book.import.ended", actor: "user", trigger: "manual",
                outcome: outcome,
                errorType: error?.errorType,
                errorMsg: error.map { String($0.reason.prefix(200)) },
                pairId: pairId,
                attrs: attrs)
        }
    }

    /// 一次「在阅读页里」的区间（离开页面 / 切面板 / 失焦即收）。
    struct ReadTrace {
        let pairId = UUID().uuidString
        let bookID: String

        func started(chapter: Int, progressPct: Double) -> ActivityEvent {
            ActivityEvent(
                type: "book.read.started", actor: "user", trigger: "manual",
                pairId: pairId,
                attrs: [
                    "book_id": .string(bookID),
                    "chapter": .number(Double(chapter)),
                    "progress_pct": .number(progressPct),
                ])
        }

        func ended(outcome: String, chapter: Int, progressPct: Double) -> ActivityEvent {
            ActivityEvent(
                type: "book.read.ended", actor: "user", trigger: "manual",
                outcome: outcome,
                pairId: pairId,
                attrs: [
                    "book_id": .string(bookID),
                    "chapter": .number(Double(chapter)),
                    "progress_pct": .number(progressPct),
                ])
        }
    }

    /// 划线/批注的增改删复用 `user.action.saved/deleted`（附表 A 约定，不新增 type）
    static func annotationSaved(bookID: String, annotationID: String) -> ActivityEvent {
        ActivityEvent(
            type: "user.action.saved", actor: "user", trigger: "manual",
            attrs: [
                "target_kind": .string("book_annotation"),
                "target_id": .string(annotationID),
                "book_id": .string(bookID),
            ])
    }

    static func annotationDeleted(bookID: String, annotationID: String) -> ActivityEvent {
        ActivityEvent(
            type: "user.action.deleted", actor: "user", trigger: "manual",
            attrs: [
                "target_kind": .string("book_annotation"),
                "target_id": .string(annotationID),
                "book_id": .string(bookID),
            ])
    }
}
#endif
