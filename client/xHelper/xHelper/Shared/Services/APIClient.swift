import Foundation

class APIClient {
    let baseURL: String
    let token: String

    init(baseURL: String, token: String) {
        self.baseURL = baseURL
        self.token = token
    }

    // MARK: Conversations

    func listConversations() async throws -> ConversationListResponse {
        return try await get("/api/conversations")
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

    func deleteConversation(id: String) async throws {
        try await delete("/api/conversations/\(id)")
    }

    // MARK: LLM Config

    func getLLMConfig(agentId: String? = nil) async throws -> LlmConfig {
        var path = "/api/llm-config"
        if let aid = agentId { path += "?agent_id=\(aid)" }
        return try await get(path)
    }

    // MARK: Bills (P3 使用)

    func getBills() async throws -> BillListResponse {
        return try await get("/api/bills")
    }

    func uploadBillsCSV(_ csvText: String) async throws -> ImportResult {
        let path = "/api/bills/import?extract_memories=true"
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "POST"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("text/plain", forHTTPHeaderField: "Content-Type")
        req.httpBody = csvText.data(using: .utf8)
        return try await doRequest(req)
    }

    func exportBillsCSV(startDate: String, endDate: String, category: String = "") async throws -> String {
		var path = "/api/bills/export?start_date=\(startDate)&end_date=\(endDate)"
		if !category.isEmpty { path += "&category=\(category)" }
		var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
		req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
		let (data, _) = try await URLSession.shared.data(for: req)
		return String(data: data, encoding: .utf8) ?? ""
	}

	// MARK: Notes

	func listNotes(limit: Int = 50, offset: Int = 0) async throws -> NoteListResponse {
		return try await get("/api/notes?limit=\(limit)&offset=\(offset)")
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

	func extractMemoriesFromConversation(id: String) async throws {
		let _: [String: String] = try await post("/api/conversations/\(id)/extract-memories", body: [:])
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
        _ = try await doRequest(req) as [String: Bool]
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

struct Document: Identifiable, Codable {
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
}
