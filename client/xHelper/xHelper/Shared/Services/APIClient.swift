import Foundation

class APIClient {
    let baseURL: String
    let token: String

    init(baseURL: String, token: String) {
        self.baseURL = baseURL
        self.token = token
    }

    // MARK: Conversations

    func listConversations() async throws -> [Conversation] {
        return try await get("/api/conversations")
    }

    func getMessages(conversationId: String) async throws -> [Message] {
        return try await get("/api/conversations/\(conversationId)/messages")
    }

    func renameConversation(id: String, title: String) async throws {
        try await patch("/api/conversations/\(id)", body: ["title": title])
    }

    func deleteConversation(id: String) async throws {
        try await delete("/api/conversations/\(id)")
    }

    // MARK: Bills (P3 使用)

    func getBills() async throws -> [Bill] {
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

    // MARK: Memories (P3)

	func listMemories(limit: Int = 50, offset: Int = 0, keyword: String = "", source: String = "") async throws -> [UserMemory] {
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

	// MARK: Agents (P3)

	func listAgents(limit: Int = 50) async throws -> [Agent] {
		return try await get("/api/agents?limit=\(limit)")
	}

	func createAgent(name: String, systemPrompt: String) async throws -> Agent {
		return try await post("/api/agents", body: ["name": name, "system_prompt": systemPrompt])
	}

	func updateAgent(id: String, name: String? = nil, systemPrompt: String? = nil) async throws -> Agent {
		var body: [String: String] = [:]
		if let n = name { body["name"] = n }
		if let s = systemPrompt { body["system_prompt"] = s }
		return try await patch("/api/agents/\(id)", body: body)
	}

	func deleteAgent(id: String) async throws {
		try await delete("/api/agents/\(id)")
	}

	// MARK: Internal

    private func get<T: Decodable>(_ path: String) async throws -> T {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        return try await doRequest(req)
    }

    private func patch(_ path: String, body: [String: String]) async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "PATCH"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
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

    private func patch<T: Decodable>(_ path: String, body: [String: String]) async throws -> T {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "PATCH"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = try JSONEncoder().encode(body)
        return try await doRequest(req)
    }

    private func delete(_ path: String) async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)\(path)")!)
        req.httpMethod = "DELETE"
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        _ = try await doRequest(req) as [String: String]
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
        return try JSONDecoder().decode(T.self, from: data)
    }
}

// MARK: Bill model (P3)

struct Bill: Identifiable, Codable {
    let id: String
    let item: String
    let amount: Double
    let category1: String
    let category2: String
    let date: String
    let status: Int
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
    let memoryKey: String
    let memoryValue: String
    let source: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case memoryKey = "memory_key"
        case memoryValue = "memory_value"
        case source
        case createdAt = "created_at"
    }
}

// MARK: Agent model

struct Agent: Identifiable, Codable {
    let id: String
    let userId: String
    let name: String
    let systemPrompt: String
    let model: String
    let createdAt: String

    enum CodingKeys: String, CodingKey {
        case id
        case userId = "user_id"
        case name
        case systemPrompt = "system_prompt"
        case model
        case createdAt = "created_at"
    }
}
