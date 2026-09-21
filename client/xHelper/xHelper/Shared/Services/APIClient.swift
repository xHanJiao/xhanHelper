import Foundation

class APIClient {
    let baseURL: String
    let token: String

    init(baseURL: String, token: String) {
        self.baseURL = baseURL
        self.token = token
    }

    // MARK: Conversations

    func listConversations(limit: Int = 50, offset: Int = 0, archived: Bool = false) async throws -> ConversationListResponse {
        var path = "/api/conversations?limit=\(limit)&offset=\(offset)"
        if archived { path += "&archived=true" }
        return try await get(path)
    }

    func getMessages(conversationId: String) async throws -> [Message] {
        return try await get("/api/conversations/\(conversationId)/messages")
    }

    func getMessagesCursor(conversationId: String, cursor: String = "", direction: String = "before", limit: Int = 7) async throws -> CursorMessages {
        var path = "/api/conversations/\(conversationId)/messages-v2?limit=\(limit)&direction=\(direction)"
        if !cursor.isEmpty { path += "&cursor=\(cursor)" }
        return try await get(path)
    }

    func deleteMessage(conversationId: String, messageId: String) async throws {
        try await delete("/api/conversations/\(conversationId)/messages/\(messageId)")
    }

    func renameConversation(id: String, title: String) async throws {
        try await patch("/api/conversations/\(id)", body: ["title": title])
    }

    /// 置顶/取消置顶会话（复用 PATCH /api/conversations/:id 的 pinned 字段）。
    func setConversationPinned(id: String, pinned: Bool) async throws {
        try await patch("/api/conversations/\(id)", body: ["pinned": pinned])
    }

    /// 创建新会话（空白 tab 点击智能体绑定时用），agentID 可选绑定。
    /// ephemeral=true 创建无痕会话（窗口结束后硬销毁）。
    func createConversation(agentID: String? = nil, ephemeral: Bool = false) async throws -> Conversation {
        var body: [String: Any] = [:]
        if let aid = agentID { body["agent_id"] = aid }
        if ephemeral { body["ephemeral"] = true }
        return try await post("/api/conversations", body: body)
    }

    /// 切换会话绑定的智能体（复用 PATCH /api/conversations/:id 的 agent_id 字段），
    /// 返回服务端确认的 agent_id / agent_name。
    func setConversationAgent(id: String, agentID: String) async throws -> [String: String] {
        try await patch("/api/conversations/\(id)", body: ["agent_id": agentID])
    }

    /// 切换会话模式（"work" | "plan" | "strict"）。模式只能由用户/客户端切换，LLM 无控制权。
    func updateConversationMode(id: String, mode: String) async throws {
        try await patch("/api/conversations/\(id)/mode", body: ["mode": mode])
    }

    /// 获取当前会话的执行计划（todo，丑版展示用）
    func getConversationPlan(conversationId: String) async throws -> ConversationPlan {
        try await get("/api/conversations/\(conversationId)/plans")
    }

    /// 获取会话的上下文 token 用量与消耗金额（上下文圆环 + hover 浮层用）
    func getConversationUsage(conversationId: String) async throws -> ConversationUsage {
        try await get("/api/conversations/\(conversationId)/usage")
    }

    func deleteConversation(id: String) async throws {
        try await delete("/api/conversations/\(id)")
    }

    /// 恢复已归档会话。
    func restoreConversation(id: String) async throws {
        try await post("/api/conversations/\(id)/restore", body: [:])
    }

    /// 显式终止运行中的 chat（ReAct 循环），返回是否找到运行中的会话
    func stopChat(conversationId: String) async throws -> Bool {
        struct StopResp: Decodable {
            let stopped: Bool
        }
        let resp: StopResp = try await post("/api/chat/stop", body: ["conversation_id": conversationId])
        return resp.stopped
    }

    // MARK: LLM Config

    func getLLMConfig(agentId: String? = nil) async throws -> LlmConfig {
        var path = "/api/llm-config"
        if let aid = agentId { path += "?agent_id=\(aid)" }
        return try await get(path)
    }

    // MARK: Bills (P3 使用)

    func getBills(limit: Int = 50, offset: Int = 0, categorys: [String] = [], startDate: String = "", endDate: String = "", keyword: String = "", owner: String = "") async throws -> BillListResponse {
        let q = billFilterQuery(categorys: categorys, startDate: startDate, endDate: endDate, keyword: keyword, owner: owner)
        return try await get("/api/bills?limit=\(limit)&offset=\(offset)" + (q.isEmpty ? "" : "&" + q))
    }

    func fetchBillStats(categorys: [String] = [], startDate: String = "", endDate: String = "", keyword: String = "", owner: String = "") async throws -> BillStats {
        let q = billFilterQuery(categorys: categorys, startDate: startDate, endDate: endDate, keyword: keyword, owner: owner)
        return try await get("/api/bills/stats" + (q.isEmpty ? "" : "?" + q))
    }

    private func billFilterQuery(categorys: [String] = [], startDate: String = "", endDate: String = "", keyword: String = "", owner: String = "") -> String {
        var parts: [String] = []
        for category in categorys {
            parts.append("category=\(category.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? category)")
        }
        if !startDate.isEmpty { parts.append("start_date=\(startDate)") }
        if !endDate.isEmpty { parts.append("end_date=\(endDate)") }
        if !keyword.isEmpty { parts.append("keyword=\(keyword.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? keyword)") }
        if !owner.isEmpty { parts.append("owner=\(owner.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? owner)") }
        return parts.joined(separator: "&")
    }

    func fetchBillOwners() async throws -> [String] {
        let resp: OwnersResponse = try await get("/api/bills/owners")
        return resp.owners
    }

    func fetchBillCategories() async throws -> [String] {
        let resp: CategoriesResponse = try await get("/api/bills/categories")
        return resp.category1List
    }

    func uploadBillsCSV(_ csvText: String) async throws -> ImportResult {
        let path = "/api/bills/import"
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("text/plain", forHTTPHeaderField: "Content-Type")
        req.httpBody = csvText.data(using: .utf8)
        return try await doRequest(req)
    }

    func exportBillsCSV(startDate: String, endDate: String, categorys: [String] = [], keyword: String = "", owner: String = "") async throws -> String {
		let q = billFilterQuery(categorys: categorys, keyword: keyword, owner: owner)
		let path = "/api/bills/export?start_date=\(startDate)&end_date=\(endDate)" + (q.isEmpty ? "" : "&" + q)
		var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
		req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
		let (data, _) = try await URLSession.shared.data(for: req)
		return String(data: data, encoding: .utf8) ?? ""
	}

	// MARK: Notes

	func listNotes(limit: Int = 50, offset: Int = 0, keyword: String = "") async throws -> NoteListResponse {
		var path = "/api/notes?limit=\(limit)&offset=\(offset)"
		if !keyword.isEmpty { path += "&keyword=\(keyword.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? keyword)" }
		return try await get(path)
	}

	func batchGetNotes(ids: [String]) async throws -> [Note] {
		return try await post("/api/notes/batch", body: ["ids": ids])
	}

	// MARK: Documents

	/// tags 为多标签**交集**筛选（文档须同时含全部标签，空数组=不过滤）；keyword 非空时 fields 决定检索字段。
	func listDocuments(tags: [String] = [], keyword: String? = nil, fields: Set<DocumentSearchField> = [], limit: Int = 24, offset: Int = 0) async throws -> DocumentListResponse {
		var path = "/api/documents?limit=\(limit)&offset=\(offset)"
		if !tags.isEmpty {
			let joined = tags.joined(separator: ",")
			path += "&tags=\(joined.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? joined)"
		}
		if let k = keyword, !k.isEmpty {
			path += "&keyword=\(k.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? k)"
			if !fields.isEmpty {
				path += "&fields=\(fields.map(\.rawValue).sorted().joined(separator: ","))"
			}
		}
		return try await get(path)
	}

	/// 交集篇数：limit=0 让服务端只出 total，不拉列表。
	func documentCount(tags: [String]) async throws -> Int {
		let resp = try await listDocuments(tags: tags, limit: 0)
		return resp.total
	}

	func getDocument(id: String) async throws -> Document {
		return try await get("/api/documents/\(id)")
	}

	func listDocumentTags() async throws -> DocumentTagListResponse {
		return try await get("/api/documents/tags")
	}

    func deleteDocument(id: String) async throws {
        try await delete("/api/documents/\(id)")
    }

    /// 回收站列表（已软删的文档，按删除时间倒序）
    func listTrashDocuments(limit: Int = 24, offset: Int = 0) async throws -> DocumentListResponse {
        return try await get("/api/documents/trash?limit=\(limit)&offset=\(offset)")
    }

    /// 从回收站恢复
    func restoreDocument(id: String) async throws {
        try await post("/api/documents/\(id)/restore", body: [:])
    }

    /// 全部恢复回收站
    func restoreAllTrashDocuments() async throws {
        try await post("/api/documents/trash/restore", body: [:])
    }

    /// 清空回收站（永久删除）
    func purgeAllTrashDocuments() async throws {
        try await delete("/api/documents/trash")
    }

    // MARK: Annotations（文档划线批注）

    func listAnnotations(documentId: String, limit: Int = 200, offset: Int = 0) async throws -> DocumentAnnotationListResponse {
        return try await get("/api/documents/\(documentId)/annotations?limit=\(limit)&offset=\(offset)")
    }

    /// `quote` 是 DOM 选中文本，`prefix` / `suffix` 是选区前后的 DOM 纯文本（各 ≤60 字），
    /// 只在文中同一句出现多次时用于消歧 —— 服务端锚到第几处靠它。
    func createAnnotation(documentId: String, quote: String, note: String, prefix: String, suffix: String) async throws -> DocumentAnnotation {
        return try await post("/api/documents/\(documentId)/annotations",
                              body: ["quote": quote, "note": note, "prefix": prefix, "suffix": suffix])
    }

    func updateAnnotation(id: String, note: String) async throws -> AnnotationNoteUpdate {
        return try await patch("/api/annotations/\(id)", body: ["note": note])
    }

    func deleteAnnotation(id: String) async throws {
        try await delete("/api/annotations/\(id)")
    }

    /// 抽屉「重新定位」：把一条失活批注重挂到用户新选的一段文字上（返回更新后的整条）。
    func relocateAnnotation(id: String, quote: String, prefix: String, suffix: String) async throws -> DocumentAnnotation {
        return try await post("/api/annotations/\(id)/relocate",
                              body: ["quote": quote, "prefix": prefix, "suffix": suffix])
    }

	// MARK: Memories (P3)

	func listMemories(limit: Int = 50, offset: Int = 0, keyword: String = "", source: String = "") async throws -> MemoryListResponse {
		var path = "/api/memories?limit=\(limit)&offset=\(offset)"
		if !keyword.isEmpty { path += "&keyword=\(keyword.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? keyword)" }
		if !source.isEmpty { path += "&source=\(source)" }
		return try await get(path)
	}

	func deleteMemory(id: String) async throws {
		try await delete("/api/memories/\(id)")
	}

	func batchGetMemories(ids: [String]) async throws -> [UserMemory] {
		return try await post("/api/memories/batch", body: ["ids": ids])
	}

	func batchDeleteMemories(ids: [String]) async throws {
		let _: [String: Int] = try await post("/api/memories/batch-delete", body: ["ids": ids])
	}

	// MARK: Agents (P3)

	func listAgents(limit: Int = 50) async throws -> AgentListResponse {
		return try await get("/api/agents?limit=\(limit)")
	}

	/// 输入栏智能体选择器列表：首位为当前会话激活智能体（active=true），
	/// 其余按创建时间倒序分页（cursor 为 offset，每页 10 条）。
	func listAgentsForInputBar(convID: String?, cursor: Int = 0) async throws -> InputBarAgentListResponse {
		var path = "/api/agents-for-input-bar?cursor=\(cursor)"
		if let cid = convID, !cid.isEmpty { path += "&conv_id=\(cid)" }
		return try await get(path)
	}

	func createAgent(name: String, systemPrompt: String, model: String? = nil, thinking: Bool? = nil, temperature: Double? = nil, maxTokens: Int? = nil) async throws -> Agent {
		var cb: [String: Any] = ["name": name, "system_prompt": systemPrompt]
        if let m = model { cb["model"] = m }
        var p: [String: Any] = [:]
        if let t = thinking { p["thinking"] = t }
        if let temp = temperature { p["temperature"] = temp }
        if let mt = maxTokens { p["max_tokens"] = mt }
        if !p.isEmpty, let jsonData = try? JSONSerialization.data(withJSONObject: p),
           let jsonStr = String(data: jsonData, encoding: .utf8) {
            cb["params_json"] = jsonStr
        } else if !p.isEmpty {
            LogBuffer.shared.error("APIClient.createAgent: failed to serialize params_json")
        }
        return try await post("/api/agents", body: cb)
	}

	func updateAgent(id: String, name: String? = nil, systemPrompt: String? = nil, model: String? = nil, thinking: Bool? = nil, temperature: Double? = nil, maxTokens: Int? = nil) async throws -> Agent {
		var body: [String: Any] = [:]
		if let n = name { body["name"] = n }
		if let s = systemPrompt { body["system_prompt"] = s }
		if let m = model { body["model"] = m }
		var p: [String: Any] = [:]
		if let t = thinking { p["thinking"] = t }
		if let temp = temperature { p["temperature"] = temp }
		if let mt = maxTokens { p["max_tokens"] = mt }
        if !p.isEmpty, let jsonData = try? JSONSerialization.data(withJSONObject: p),
           let jsonStr = String(data: jsonData, encoding: .utf8) {
            body["params_json"] = jsonStr
        } else if !p.isEmpty {
            LogBuffer.shared.error("APIClient.updateAgent: failed to serialize params_json")
        }
		return try await patch("/api/agents/\(id)", body: body)
	}

	func deleteAgent(id: String) async throws {
		try await delete("/api/agents/\(id)")
	}

	// MARK: Prompts

	func listPrompts(type: String = "", status: String = "") async throws -> PromptListResponse {
		var parts: [String] = []
		if !type.isEmpty { parts.append("type=\(type)") }
		if !status.isEmpty { parts.append("status=\(status)") }
		let qs = parts.isEmpty ? "" : "?\(parts.joined(separator: "&"))"
		return try await get("/api/prompts\(qs)")
	}

	func createPrompt(promptType: String, systemPrompt: String) async throws -> UserPrompt {
		return try await post("/api/prompts", body: ["prompt_type": promptType, "system_prompt": systemPrompt])
	}

	func rollbackPrompt(id: String) async throws {
		let _: [String: Bool] = try await post("/api/prompts/\(id)/rollback", body: [:])
	}

	func deletePrompt(id: String) async throws {
		try await delete("/api/prompts/\(id)")
	}

	// MARK: Client Tools（本地 MCP 工具结果回传）

	// resultJSON: MCP tools/call 的 result 原始 JSON（对象）。
	// 服务端按 json.RawMessage 绑定，这里以原生 JSON 值上送，避免双重编码。
	func submitToolResult(callId: String, resultJSON: Data?, error: String? = nil) async throws {
		var body: [String: Any] = ["call_id": callId]
		if let d = resultJSON, !d.isEmpty {
			if let obj = try? JSONSerialization.jsonObject(with: d) {
				body["result"] = obj
			} else {
				body["result"] = String(data: d, encoding: .utf8) ?? ""
			}
		}
		if let e = error { body["error"] = e }
		let _: [String: String] = try await post("/api/chat/tool-result", body: body)
	}

    // MARK: Auth

    func refreshToken() async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)/api/auth/refresh")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let _: [String: Bool] = try await doRequest(req)
    }

    // MARK: Share

    func createMessageShare(conversationId: String, userMessageIds: [String]) async throws -> CreateShareToken {
        return try await post("/api/conversations/\(conversationId)/share-messages", body: ["user_message_ids": userMessageIds])
    }

    func getDocumentShare(docID: String) async throws -> DocumentShareResult {
        return try await get("/api/documents/\(docID)/share")
    }

    func createDocumentShare(docID: String) async throws -> DocumentShareResult {
        return try await post("/api/documents/\(docID)/share", body: [:])
    }

	// MARK: Internal

    /// 上传一张图片到图床，返回服务端分配的 image_id（发送时作为 attachments 传回）。
    func uploadImage(_ data: Data, filename: String) async throws -> UploadedImage {
        let boundary = "----xhanBoundary\(UUID().uuidString)"
        var body = Data()
        body.append("--\(boundary)\r\n".data(using: .utf8)!)
        body.append("Content-Disposition: form-data; name=\"file\"; filename=\"\(filename)\"\r\n".data(using: .utf8)!)
        body.append("Content-Type: application/octet-stream\r\n\r\n".data(using: .utf8)!)
        body.append(data)
        body.append("\r\n--\(boundary)--\r\n".data(using: .utf8)!)

        var req = URLRequest(url: URL(string: "\(baseURL)/api/images/upload")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("multipart/form-data; boundary=\(boundary)", forHTTPHeaderField: "Content-Type")
        req.httpBody = body
        return try await doRequest(req)
    }

    /// 拉取私有图字节（需 Bearer 鉴权；私有图不能走 AsyncImage）
    func fetchImageData(id: String) async throws -> Data {
        var req = URLRequest(url: URL(string: "\(baseURL)/api/images/\(id)/file")!)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        let (data, resp) = try await URLSession.shared.data(for: req)
        let statusCode = (resp as? HTTPURLResponse)?.statusCode ?? 500
        guard statusCode == 200 else {
            throw NSError(domain: "APIClient", code: statusCode,
                userInfo: [NSLocalizedDescriptionKey: "load image failed (status \(statusCode))"])
        }
        return data
    }

    /// 图片库列表（管理页）：本人全部图，服务端按 created_at 倒序
    func listImages(limit: Int = 50, offset: Int = 0) async throws -> ImageListResponse {
        return try await get("/api/images?limit=\(limit)&offset=\(offset)")
    }

    /// 删除一张图（服务端删记录 + 文件；已被消息引用的图删后对话里会显示占位）
    func deleteImage(id: String) async throws {
        try await delete("/api/images/\(id)")
    }

    private func get<T: Decodable>(_ path: String) async throws -> T {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        return try await doRequest(req)
    }

    private func patch(_ path: String, body: [String: Any]) async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "PATCH"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        try await doRequestStatusOnly(req)
    }

    private func post<T: Decodable>(_ path: String, body: [String: Any]) async throws -> T {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        return try await doRequest(req)
    }

    /// POST 但只校验统一包装，不解析 data 结构。
    private func post(_ path: String, body: [String: Any]) async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        try await doRequestStatusOnly(req)
    }

    private func patch<T: Decodable>(_ path: String, body: [String: Any]) async throws -> T {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "PATCH"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        return try await doRequest(req)
    }

    private func delete(_ path: String) async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "DELETE"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        try await doRequestStatusOnly(req)
    }

    /// 只校验统一包装（HTTP 200 + status=="ok"），不解析 data 具体结构。
    /// 用于 DELETE 等 data 结构不确定、仅需确认成功的接口。
    private func doRequestStatusOnly(_ req: URLRequest) async throws {
        let (data, resp) = try await URLSession.shared.data(for: req)
        let statusCode = (resp as? HTTPURLResponse)?.statusCode ?? 500
        if statusCode == 401 {
            throw NSError(domain: "APIClient", code: 401,
                userInfo: [NSLocalizedDescriptionKey: "unauthorized"])
        }
        guard statusCode == 200 else {
            let msg = String(data: data, encoding: .utf8) ?? "unknown"
            throw NSError(domain: "APIClient", code: statusCode,
                userInfo: [NSLocalizedDescriptionKey: msg])
        }
        let wrapper = try JSONDecoder().decode(APIResponse<EmptyData>.self, from: data)
        guard wrapper.status == "ok" && wrapper.errorCode == 0 else {
            let msg = wrapper.errMsg.isEmpty ? "unknown error (code: \(wrapper.errorCode))" : wrapper.errMsg
            throw NSError(domain: "APIClient", code: wrapper.errorCode,
                userInfo: [NSLocalizedDescriptionKey: msg])
        }
    }

    @discardableResult
    private func doRequest<T: Decodable>(_ req: URLRequest) async throws -> T {
        let (data, resp) = try await URLSession.shared.data(for: req)
        let statusCode = (resp as? HTTPURLResponse)?.statusCode ?? 500
        if statusCode == 401 {
            throw NSError(domain: "APIClient", code: 401,
                userInfo: [NSLocalizedDescriptionKey: "unauthorized"])
        }
        guard statusCode == 200 else {
            let msg = String(data: data, encoding: .utf8) ?? "unknown"
            throw NSError(domain: "APIClient", code: statusCode,
                userInfo: [NSLocalizedDescriptionKey: msg])
        }
        let wrapper = try JSONDecoder().decode(APIResponse<T>.self, from: data)
        if wrapper.status == "error" || wrapper.errorCode != 0 {
            let msg = wrapper.errMsg.isEmpty ? "unknown error (code: \(wrapper.errorCode))" : wrapper.errMsg
            throw NSError(domain: "APIClient", code: wrapper.errorCode,
                userInfo: [NSLocalizedDescriptionKey: msg])
        }
        guard let value = wrapper.data else {
            throw NSError(domain: "APIClient", code: 500,
                userInfo: [NSLocalizedDescriptionKey: "empty response data"])
        }
        return value
    }
}

private struct APIResponse<T: Decodable>: Decodable {
    let status: String
    let errorCode: Int
    let errMsg: String
    let data: T?
}

/// 用于只关心包装层校验、忽略 data 具体结构的请求
private struct EmptyData: Decodable {}

// MARK: List Response models

struct ConversationListResponse: Codable {
    let conversations: [Conversation]
    let total: Int
}

struct BillListResponse: Codable {
    let bills: [Bill]
    let total: Int
}

struct BillStats: Codable {
    let count: Int
    let sum: Double
}

struct OwnersResponse: Codable {
    let owners: [String]
}

struct CategoriesResponse: Codable {
    let category1List: [String]

    enum CodingKeys: String, CodingKey {
        case category1List = "category1_list"
    }
}

struct NoteListResponse: Codable {
    let notes: [Note]
    let total: Int
}

struct MemoryListResponse: Codable {
    let memories: [UserMemory]
    let total: Int
}

struct AgentListResponse: Codable {
    let agents: [Agent]
    let total: Int
}

/// 输入栏智能体选择器条目：服务端 InputBarAgentDTO（Agent 全字段 + active）。
struct InputBarAgent: Identifiable, Codable {
    let id: String
    let userId: String
    let name: String
    let systemPrompt: String
    let summary: String?
    let model: String
    let paramsJson: String?
    let createdAt: String
    let active: Bool

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case name
        case systemPrompt = "system_prompt"
        case summary, model
        case paramsJson = "params_json"
        case createdAt = "created_at"
        case active
    }
}

struct InputBarAgentListResponse: Decodable {
    let agents: [InputBarAgent]
    /// 服务端分页大小（cursor 按此步进；旧服务端未下发时默认 10）
    let pageSize: Int

    enum CodingKeys: String, CodingKey {
        case agents
        case pageSize = "page_size"
    }

    init(from decoder: Decoder) throws {
        let c = try decoder.container(keyedBy: CodingKeys.self)
        agents = try c.decode([InputBarAgent].self, forKey: .agents)
        pageSize = try c.decodeIfPresent(Int.self, forKey: .pageSize) ?? 10
    }
}

struct PromptListResponse: Codable {
    let prompts: [UserPrompt]
    let total: Int
}

struct LlmConfig: Codable {
    let temperature: Double?
    let maxTokens: Int?
    let thinking: Bool?

    enum CodingKeys: String, CodingKey {
        case temperature, thinking
        case maxTokens = "max_tokens"
    }
}

// MARK: Bill model (P3)

struct Bill: Identifiable, Codable {
    let id: String
    let item: String
    let amount: Double
    let category1: String
    let category2: String
    let consumeType: String?
    let payType: String?
    let owner: String?
    let remark: String?
    let date: String
    let status: Int

    enum CodingKeys: String, CodingKey {
        case id, item, amount, category1, category2
        case consumeType = "consume_type"
        case payType = "pay_type"
        case owner, remark, date, status
    }
}

struct ImportResult: Codable {
    let imported: Int
    let skipped: Int
    let total: Int
    let errors: [String]?
}

// MARK: Memory model

struct UserMemory: Identifiable, Codable {
    let id: String
    let userId: String
    let memoryValue: String
    let topic: String?
    let tags: String?
    let facts: String?
    let source: String
    let confirmCount: Int?
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case memoryValue = "memory_value"
        case topic, tags, facts, source
        case confirmCount = "confirm_count"
        case createdAt = "created_at"
    }
}

// MARK: Agent model

struct Note: Identifiable, Codable {
    let id: String
    let userId: String
    let topic: String
    let content: String
    let tags: String?
    let variables: String?
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case topic
        case content
        case tags
        case variables
        case createdAt = "created_at"
    }
}

struct Document: Identifiable, Codable, Equatable {
    let id: String
    let userId: String
    let title: String
    let content: String
    let tags: String?
    let conversationId: String?
    let createdAt: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case title
        case content
        case tags
        case conversationId = "conversation_id"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
    }
}

struct DocumentListResponse: Codable {
    let documents: [Document]
    let total: Int
}

struct DocumentTag: Identifiable, Codable, Equatable {
    let name: String
    let count: Int

    var id: String { name }
}

/// 两标签在同一篇文档里共现（词云多选联动高亮用）。
struct TagEdge: Codable, Equatable {
    let a: String
    let b: String
    let count: Int
}

struct DocumentTagListResponse: Codable {
    let tags: [DocumentTag]
    let edges: [TagEdge]
}

struct Agent: Identifiable, Codable {
    let id: String
    let userId: String
    let name: String
    let systemPrompt: String
    let summary: String?
    let model: String
    let paramsJson: String?
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case name
        case systemPrompt = "system_prompt"
        case summary, model
        case paramsJson = "params_json"
        case createdAt = "created_at"
    }
}

// MARK: UserPrompt model

/// 执行计划（todo）—— 服务端 GET /api/conversations/:id/plans
struct ConversationPlan: Codable {
    let goal: String
    let steps: [PlanStep]
}

struct PlanStep: Identifiable, Codable {
    let content: String
    let status: String
    var id: String { content + status }
}

struct CursorMessages: Codable {
    let messages: [Message]
    let hasMoreBefore: Bool
    let hasMoreAfter: Bool
    let oldestCursor: String
    let newestCursor: String

    enum CodingKeys: String, CodingKey {
        case messages
        case hasMoreBefore = "has_more_before"
        case hasMoreAfter = "has_more_after"
        case oldestCursor = "oldest_cursor"
        case newestCursor = "newest_cursor"
    }
}

/// 会话上下文用量与消耗金额 —— 服务端 GET /api/conversations/:id/usage
struct ConversationUsage: Codable {
    /// 当前上下文 token 数（最新一轮 prompt_tokens）
    let contextTokens: Int
    /// 上下文窗口上限（按模型档位，0=未配置）
    let contextWindow: Int
    /// 当前窗口消耗（元，主对话当前 session 部分）
    let windowCost: Double
    /// 会话消耗（元，主对话+后处理，不含子智能体）
    let conversationCost: Double
    /// 子智能体消耗（元，全会话累计）
    let subagentCost: Double
    /// 子智能体消耗（元，当前窗口内）
    let subagentWindowCost: Double
    /// 子智能体任务数
    let subagentCount: Int
    /// 会话合计 = conversationCost + subagentCost
    let totalCost: Double

    enum CodingKeys: String, CodingKey {
        case contextTokens = "context_tokens"
        case contextWindow = "context_window"
        case windowCost = "window_cost"
        case conversationCost = "conversation_cost"
        case subagentCost = "subagent_cost"
        case subagentWindowCost = "subagent_window_cost"
        case subagentCount = "subagent_count"
        case totalCost = "total_cost"
    }
}

struct UserPrompt: Identifiable, Codable {
    let id: String
    let userId: String
    let promptType: String
    let systemPrompt: String
    let status: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case promptType = "prompt_type"
        case systemPrompt = "system_prompt"
        case status
        case createdAt = "created_at"
    }
}

// MARK: Share

struct CreateShareToken: Codable {
    let token: String
    let expiresAt: String

    enum CodingKeys: String, CodingKey {
        case token
        case expiresAt = "expires_at"
    }
}

struct DocumentShareResult: Codable {
    let id: String
    let token: String
    let url: String
    let expiresAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case token
        case url
        case expiresAt = "expires_at"
    }
}

// MARK: - 图床

struct UploadedImage: Codable {
    let imageId: String
    let url: String
    let mime: String
    let bytes: Int

    enum CodingKeys: String, CodingKey {
        case imageId = "image_id"
        case url, mime, bytes
    }
}

/// 图片库条目（`GET /api/images` 的 items 元素）
struct ImageAssetSummary: Identifiable, Codable {
    let id: String
    let url: String
    let mime: String
    let bytes: Int64
    /// chat | doc | local | web（聊天选图 / 文档配图 / agent 本地发现 / agent 网页发现）
    let source: String
    /// 是否曾被消息引用过：决定保留期（15 天 / 3 天），也决定删除会不会让对话裂图
    let referenced: Bool
    /// 是否已上传 DeepSeek Files（多模态输入用；本地副本始终保留）
    let hasFileId: Bool
    let lastUsedAt: String?
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id, url, mime, bytes, source, referenced
        case hasFileId = "has_file_id"
        case lastUsedAt = "last_used_at"
        case createdAt = "created_at"
    }
}

struct ImageListResponse: Codable {
    let items: [ImageAssetSummary]
    let total: Int
}

// MARK: - Reminders

extension APIClient {

    func listReminders(status: String? = nil, limit: Int = 50, offset: Int = 0) async throws -> ReminderListResponse {
        var path = "/api/reminders?limit=\(limit)&offset=\(offset)"
        if let s = status { path += "&status=\(s)" }
        return try await get(path)
    }

    func ackReminder(id: String) async throws -> AckReminderResponse {
        return try await post("/api/reminders/\(id)/ack", body: [:])
    }

	func cancelReminder(id: String) async throws -> Reminder {
		return try await post("/api/reminders/\(id)/cancel", body: [:])
	}

	// MARK: Themes

	func listThemes(limit: Int = 50, offset: Int = 0) async throws -> AppThemeListResponse {
		return try await get("/api/themes?limit=\(limit)&offset=\(offset)")
	}

	// MARK: Facts

	func listFacts(limit: Int = 50, offset: Int = 0) async throws -> AppFactListResponse {
		return try await get("/api/facts?limit=\(limit)&offset=\(offset)")
	}
}

// MARK: AppFact model

/// 单条长期事实（facts 表）。只读：写入由后端决策段负责。
struct AppFact: Identifiable, Codable {
	let id: String
	let userId: String
	let subject: String
	/// "state" | "event"
	let kind: String
	let content: String
	let createdAt: String
	let updatedAt: String

	enum CodingKeys: String, CodingKey {
		case id
		case userId = "user_id"
		case subject, kind, content
		case createdAt = "created_at"
		case updatedAt = "updated_at"
	}
}

struct AppFactListResponse: Codable {
	let items: [AppFact]
	let total: Int
}

// MARK: AppTheme model

struct AppTheme: Identifiable, Codable {
    let id: String
    let userId: String
    let name: String
    let description: String
    let status: String
    let stateText: String
    let createdAt: String
    let updatedAt: String
    let lastRunAt: String?

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case name, description, status
        case stateText = "state_text"
        case createdAt = "created_at"
        case updatedAt = "updated_at"
        case lastRunAt = "last_run_at"
    }
}

struct AppThemeListResponse: Codable {
    let items: [AppTheme]
    let total: Int
}
