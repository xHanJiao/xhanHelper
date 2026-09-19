import Foundation

/// 服务端 `document` 工具结果 → `DocumentRefPatch` 的纯逻辑（只 create/update 出卡片）。
///
/// 为什么只认 create/update 不认 get：卡片是「agent 操作过的文档」的入口。若把读也算进来，
/// 同轮「先 get（读到改前旧值）再 update（改后新值）」会让卡片刻在过期快照上。去掉 get 后，
/// 每次写操作都带出它当时的权威新状态（title/tags 在入参里）。
///
/// 字段以 Optional 表达「未提供 vs 显式清空」，由 `DocumentRef.merged(with:)` 合并。
///
/// 未来同类「tool_result JSON → 特殊展示」直接在这里加一个 producer 函数。
enum DocumentArtifactProducer {
    /// preview 截断长度（字），够卡片一行展示。
    static let previewMaxLen = 60

    /// `create_document` / `update_document` 的结果 + 入参 → 增量 patch。
    /// query / list / get / delete 等一律返回 nil（不出卡片）。
    static func patch(function: String, args: [String: Any], data: [String: Any]?) -> DocumentRefPatch? {
        guard let id = data?["id"] as? String, !id.isEmpty else { return nil }

        switch function {
        case "create_document":
            // 新建文档：tags 无参数即空[]；preview 取全文 content 摘要。
            return DocumentRefPatch(
                id: id,
                title: (data?["title"] as? String) ?? (args["title"] as? String),
                tags: parseTags(args["tags"] as? String),
                preview: makePreview(args["content"] as? String)
            )
        case "update_document":
            // tags 只在入参出现时给出：nil=未改（保留旧值），""=显式清空。
            // preview 只用全文重写的 content；old_text/new_text 是片段，不作摘要（否则会冲掉旧摘要）。
            return DocumentRefPatch(
                id: id,
                title: args["title"] as? String,
                tags: args["tags"] != nil ? parseTags(args["tags"] as? String) : nil,
                preview: makePreview(args["content"] as? String)
            )
        default:
            return nil
        }
    }

    /// 半角/全角逗号切分、去空格、去重（对齐服务端 `service.NormalizeTags`）。
    static func parseTags(_ raw: String?) -> [String] {
        guard let raw, !raw.isEmpty else { return [] }
        let parts = raw.split(whereSeparator: { $0 == "," || $0 == "，" })
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        return Array(dedupe(parts))
    }

    /// 折叠所有空白（含换行）为单空格、trim，再截断 `previewMaxLen` 字；空串返回 nil。
    static func makePreview(_ raw: String?) -> String? {
        guard let raw else { return nil }
        let collapsed = raw
            .split(whereSeparator: { $0.isWhitespace })
            .joined(separator: " ")
        guard !collapsed.isEmpty else { return nil }
        if collapsed.count <= previewMaxLen { return collapsed }
        return String(collapsed.prefix(previewMaxLen))
    }

    private static func dedupe(_ items: [String]) -> [String] {
        var seen = Set<String>()
        return items.filter { seen.insert($0).inserted }
    }
}

