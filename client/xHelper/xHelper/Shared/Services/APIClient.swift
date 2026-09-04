import Foundation

class APIClient {
    let baseURL: String
    let token: String

    init(baseURL: String, token: String) {
        self.baseURL = baseURL
        self.token = token
    }

    // MARK: Conversations

    func listConversations(limit: Int = 50, offset: Int = 0) async throws -> ConversationListResponse {
        return try await get("/api/conversations?limit=\(limit)&offset=\(offset)")
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

    /// 创建新会话（空白 tab 点击智能体绑定时用），agentID 可选绑定。
    func createConversation(agentID: String? = nil) async throws -> Conversation {
        var body: [String: Any] = [:]
        if let aid = agentID { body["agent_id"] = aid }
        return try await post("/api/conversations", body: body)
    }

    /// 切换会话绑定的智能体（复用 PATCH /api/conversations/:id 的 agent_id 字段），
    /// 返回服务端确认的 agent_id / agent_name。
    func setConversationAgent(id: String, agentID: String) async throws -> [String: String] {
        try await patch("/api/conversations/\(id)", body: ["agent_id": agentID])
    }

    /// 切换会话模式（"work" | "plan"）。模式只能由用户/客户端切换，LLM 无控制权。
    func updateConversationMode(id: String, mode: String) async throws {
        try await patch("/api/conversations/\(id)/mode", body: ["mode": mode])
    }

    /// 获取当前会话的执行计划（todo，丑版展示用）
    func getConversationPlan(conversationId: String) async throws -> ConversationPlan {
        try await get("/api/conversations/\(conversationId)/plans")
    }

    func deleteConversation(id: String) async throws {
        try await delete("/api/conversations/\(id)")
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

    func getBills(limit: Int = 50, offset: Int = 0, category: String = "", startDate: String = "", endDate: String = "", keyword: String = "") async throws -> BillListResponse {
        var path = "/api/bills?limit=\(limit)&offset=\(offset)"
        if !category.isEmpty { path += "&category=\(category.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? category)" }
        if !startDate.isEmpty { path += "&start_date=\(startDate)" }
        if !endDate.isEmpty { path += "&end_date=\(endDate)" }
        if !keyword.isEmpty { path += "&keyword=\(keyword.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? keyword)" }
        return try await get(path)
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

    func exportBillsCSV(startDate: String, endDate: String, category: String = "", keyword: String = "") async throws -> String {
		var path = "/api/bills/export?start_date=\(startDate)&end_date=\(endDate)"
		if !category.isEmpty { path += "&category=\(category)" }
		if !keyword.isEmpty { path += "&keyword=\(keyword.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? keyword)" }
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

	func listDocuments(tag: String? = nil, keyword: String? = nil, limit: Int = 24, offset: Int = 0) async throws -> DocumentListResponse {
		var path = "/api/documents?limit=\(limit)&offset=\(offset)"
		if let t = tag, !t.isEmpty { path += "&tag=\(t.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? t)" }
		if let k = keyword, !k.isEmpty { path += "&keyword=\(k.addingPercentEncoding(withAllowedCharacters: .urlQueryAllowed) ?? k)" }
		return try await get(path)
	}

	func getDocument(id: String) async throws -> Document {
		return try await get("/api/documents/\(id)")
	}

	func deleteDocument(id: String) async throws {
		try await delete("/api/documents/\(id)")
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
        _ = try await doRequest(req) as [String: String]
    }

    private func post<T: Decodable>(_ path: String, body: [String: Any]) async throws -> T {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONSerialization.data(withJSONObject: body)
        return try await doRequest(req)
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
