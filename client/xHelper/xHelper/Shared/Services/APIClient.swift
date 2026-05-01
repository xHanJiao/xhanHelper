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
