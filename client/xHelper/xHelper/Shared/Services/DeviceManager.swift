import Combine
import Foundation

@MainActor
class DeviceManager: ObservableObject {
    @Published var token: String?
    @Published var userId: String?
    @Published var username: String?
    @Published var isLoggedIn = false
    @Published var isVerifying = false
    @Published var defaultConversation: ConversationData?

    private let tokenKey = "xhan_user_token"
    private let userIdKey = "xhan_user_id"
    private let usernameKey = "xhan_username"
    private let defaultConvKey = "xhan_default_conversation_id"
    private var refreshTimer: Timer?
    private let refreshInterval: TimeInterval = 3600

    #if DEBUG
    static let defaultBaseURL = "http://127.0.0.1:8080"
    #else
    static let defaultBaseURL = "https://helper.xhanx.cn"
    #endif

    var baseURL: String {
        #if DEBUG
        let url = UserDefaults.standard.string(forKey: "xhan_server_url") ?? Self.defaultBaseURL
        return url
        #else
        return Self.defaultBaseURL
        #endif
    }

    init() {
        self.token = UserDefaults.standard.string(forKey: tokenKey)
        self.userId = UserDefaults.standard.string(forKey: userIdKey)
        self.username = UserDefaults.standard.string(forKey: usernameKey)
        self.isLoggedIn = token != nil
        LogBuffer.shared.info("app launched, hasToken=\(self.isLoggedIn ? "yes" : "no")")
        if self.isLoggedIn {
            self.isVerifying = true
            Task { await verifyToken() }
        }
    }

    private func verifyToken() async {
        await performTokenRefresh()
        do {
            try await getDefaultConversationInfo()
            LogBuffer.shared.info("verifyToken: ok")
            startTokenRefresh()
        } catch {
            LogBuffer.shared.error("verifyToken failed: \(error)")
            clearAuthState()
        }
        isVerifying = false
    }

    private func clearAuthState() {
        stopTokenRefresh()
        UserDefaults.standard.removeObject(forKey: tokenKey)
        UserDefaults.standard.removeObject(forKey: userIdKey)
        UserDefaults.standard.removeObject(forKey: usernameKey)
        UserDefaults.standard.removeObject(forKey: defaultConvKey)
        token = nil
        userId = nil
        username = nil
        isLoggedIn = false
        defaultConversation = nil
    }

    func logout() async {
        if let t = token {
            var req = URLRequest(url: URL(string: "\(baseURL)/api/auth/logout")!)
            req.httpMethod = "POST"
            req.setValue("Bearer \(t)", forHTTPHeaderField: "Authorization")
            _ = try? await URLSession.shared.data(for: req)
        }
        clearAuthState()
    }

    func login(username: String, password: String) async throws {
        let body = ["username": username, "password": password]
        let data = try JSONEncoder().encode(body)
        var req = URLRequest(url: URL(string: "\(baseURL)/api/auth/login")!)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = data

        let (respData, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else {
            let raw = String(data: respData, encoding: .utf8) ?? "unknown"
            let msg = parseAuthError(raw)
            throw NSError(domain: "Auth", code: 401, userInfo: [NSLocalizedDescriptionKey: msg])
        }
        isVerifying = true
        defer { isVerifying = false }
        try saveAuthResponse(respData, username: username)
        LogBuffer.shared.info("login: token saved, user=\(username)")
        startTokenRefresh()
        try await getDefaultConversationInfo()
        LogBuffer.shared.debug("login: default conv loaded, id=\(self.defaultConversation?.id ?? "nil")")
    }

    func register(username: String, password: String) async throws {
        let body = ["username": username, "password": password]
        let data = try JSONEncoder().encode(body)
        var req = URLRequest(url: URL(string: "\(baseURL)/api/auth/register")!)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = data

        let (respData, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else {
            let raw = String(data: respData, encoding: .utf8) ?? "unknown"
            let msg = parseAuthError(raw)
            throw NSError(domain: "Auth", code: 409, userInfo: [NSLocalizedDescriptionKey: msg])
        }
        isVerifying = true
        defer { isVerifying = false }
        try saveAuthResponse(respData, username: username)
        LogBuffer.shared.info("register: token saved, user=\(username)")
        startTokenRefresh()
        try await getDefaultConversationInfo()
        LogBuffer.shared.debug("register: default conv loaded, id=\(self.defaultConversation?.id ?? "nil")")
    }

    private func saveAuthResponse(_ data: Data, username: String) throws {
        let wrapper = try JSONDecoder().decode(APIResponse<AuthResponse>.self, from: data)
        if wrapper.status == "error" || wrapper.errorCode != 0 {
            let msg = wrapper.errMsg.isEmpty ? "unknown error" : wrapper.errMsg
            throw NSError(domain: "Auth", code: wrapper.errorCode, userInfo: [NSLocalizedDescriptionKey: msg])
        }
        guard let result = wrapper.data else {
            throw NSError(domain: "Auth", code: 500, userInfo: [NSLocalizedDescriptionKey: "empty response"])
        }
        self.token = result.token
        self.userId = result.userId
        self.username = username
        self.isLoggedIn = true
        UserDefaults.standard.set(result.token, forKey: tokenKey)
        UserDefaults.standard.set(result.userId, forKey: userIdKey)
        UserDefaults.standard.set(username, forKey: usernameKey)
    }
    
    private func getDefaultConversationInfo() async throws {
        var req = URLRequest(url: URL(string: "\(baseURL)/api/conversations/default")!)
        guard let token = token else {
            throw URLError(.userAuthenticationRequired)
        }
        req.setValue("Bearer \(token)", forHTTPHeaderField: "Authorization")
        req.httpMethod = "GET"
        
        let (respData, resp) = try await URLSession.shared.data(for: req)
        let statusCode = (resp as? HTTPURLResponse)?.statusCode ?? 0
        guard statusCode == 200 else {
            let body = String(data: respData, encoding: .utf8) ?? ""
            LogBuffer.shared.error("getDefaultConversationInfo failed: status=\(statusCode), body=\(body.prefix(200))")
            throw URLError(.badServerResponse)
        }
        
        let conversation = try saveDefaultConversationInfo(respData)
        self.defaultConversation = conversation
        UserDefaults.standard.set(conversation.id, forKey: defaultConvKey)
    }
    
    private func saveDefaultConversationInfo(_ respData: Data) throws -> ConversationData {
        let response = try JSONDecoder().decode(APIResponse<ConversationData>.self, from: respData)
        guard let data = response.data else {
            throw URLError(.cannotParseResponse)
        }
        return data
    }

    // MARK: Token Refresh

    private func startTokenRefresh() {
        stopTokenRefresh()
        refreshTimer = Timer.scheduledTimer(withTimeInterval: refreshInterval, repeats: true) { [weak self] _ in
            guard let self else { return }
            Task { await self.performTokenRefresh() }
        }
    }

    private func stopTokenRefresh() {
        refreshTimer?.invalidate()
        refreshTimer = nil
    }

    private func performTokenRefresh() async {
        guard let token = token else { return }
        let api = APIClient(baseURL: baseURL, token: token)
        do {
            try await api.refreshToken()
            LogBuffer.shared.info("token refreshed ok")
        } catch let error as NSError where error.code == 401 {
            LogBuffer.shared.error("token refresh rejected (401), logging out")
            clearAuthState()
        } catch {
            LogBuffer.shared.error("token refresh failed: \(error)")
        }
    }
}

private func parseAuthError(_ raw: String) -> String {
    guard let data = raw.data(using: .utf8),
          let obj = try? JSONDecoder().decode([String: String].self, from: data),
          let err = obj["error"]
    else { return raw }
    return err
}

struct AuthResponse: Codable {
    let token: String
    let userId: String

    enum CodingKeys: String, CodingKey {
        case token
        case userId = "user_id"
    }
}

private struct APIResponse<T: Decodable>: Decodable {
    let status: String
    let errorCode: Int
    let errMsg: String
    let data: T?
}

struct ConversationData: Codable {
    let agentId: String?
    let agentName: String?
    let createdAt: String
    let id: String
    let isDefault: Bool
    let title: String
    let updatedAt: String

    enum CodingKeys: String, CodingKey {
        case agentId = "agent_id"
        case agentName = "agent_name"
        case createdAt = "created_at"
        case id
        case isDefault = "is_default"
        case title
        case updatedAt = "updated_at"
    }
}
