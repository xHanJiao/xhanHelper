import Foundation

/// agent 操作过的文档引用（建/改），聊天里渲染成可点卡片 → 开右栏文档面板定位。
/// 由 `DocumentArtifactProducer` 从 `document` 工具结果 + 入参构造，可持久化（本地缓存）。
struct DocumentRef: Identifiable, Equatable, Codable, Hashable {
    let id: String
    var title: String
    var tags: [String]
    var preview: String?

    init(id: String, title: String, tags: [String] = [], preview: String? = nil) {
        self.id = id
        self.title = title
        self.tags = tags
        self.preview = preview
    }
}

/// 一次 `create_document` / `update_document` 调用给出的**增量**信息。
///
/// 字段用 Optional 区分「本次未提供」与「显式清空」：
/// - `title == nil`：本次没改标题，合并时保留旧值。
/// - `tags == nil`：本次没带 tags 参数，保留旧值；`tags == []`：传了空串，显式清空。
/// - `preview == nil`：本次没有可展示摘要（如局部 old_text/new_text 替换），保留旧值。
struct DocumentRefPatch: Equatable {
    let id: String
    var title: String?
    var tags: [String]?
    var preview: String?
}

extension DocumentRef {
    /// 本轮首次触达某文档：用 patch 建初始引用（缺省 title→「文档」、tags→[]）。
    static func make(from patch: DocumentRefPatch) -> DocumentRef {
        DocumentRef(id: patch.id, title: patch.title ?? "文档", tags: patch.tags ?? [], preview: patch.preview)
    }

    /// 本轮再次触达：patch 的非 nil 字段覆盖，nil 字段保留旧值。
    func merged(with patch: DocumentRefPatch) -> DocumentRef {
        DocumentRef(
            id: id,
            title: patch.title ?? title,
            tags: patch.tags ?? tags,
            preview: patch.preview ?? preview
        )
    }
}

