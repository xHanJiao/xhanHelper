import Combine
import Foundation

@MainActor
class DeviceManager: ObservableObject {
    @Published var token: String?
    @Published var userId: String?
    @Published var username: String?
    @Published var isLoggedIn = false

    private let tokenKey = "xhan_user_token"
    private let userIdKey = "xhan_user_id"
    private let usernameKey = "xhan_username"

    #if DEBUG
    static let defaultBaseURL = "http://10.1.2.192:8080"
    #else
    static let defaultBaseURL = "https://helper.xhanx.cn"
    #endif

    var baseURL: String {
        UserDefaults.standard.string(forKey: "xhan_server_url") ?? Self.defaultBaseURL
    }

    init() {
        self.token = UserDefaults.standard.string(forKey: tokenKey)
        self.userId = UserDefaults.standard.string(forKey: userIdKey)
        self.username = UserDefaults.standard.string(forKey: usernameKey)
        self.isLoggedIn = token != nil
    }

    func logout() async {
        if let t = token {
            var req = URLRequest(url: URL(string: "\(baseURL)/api/auth/logout")!)
            req.httpMethod = "POST"
            req.setValue("Bearer \(t)", forHTTPHeaderField: "Authorization")
            _ = try? await URLSession.shared.data(for: req)
        }
        UserDefaults.standard.removeObject(forKey: tokenKey)
        UserDefaults.standard.removeObject(forKey: userIdKey)
        UserDefaults.standard.removeObject(forKey: usernameKey)
        token = nil
        userId = nil
        username = nil
        isLoggedIn = false
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
            let msg = String(data: respData, encoding: .utf8) ?? "unknown"
            throw NSError(domain: "Auth", code: 401, userInfo: [NSLocalizedDescriptionKey: msg])
        }
        try saveAuthResponse(respData, username: username)
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
            let msg = String(data: respData, encoding: .utf8) ?? "unknown"
            throw NSError(domain: "Auth", code: 409, userInfo: [NSLocalizedDescriptionKey: msg])
        }
        try saveAuthResponse(respData, username: username)
    }

    private func saveAuthResponse(_ data: Data, username: String) throws {
        let result = try JSONDecoder().decode(AuthResponse.self, from: data)
        self.token = result.token
        self.userId = result.userId
        self.username = username
        self.isLoggedIn = true
        UserDefaults.standard.set(result.token, forKey: tokenKey)
        UserDefaults.standard.set(result.userId, forKey: userIdKey)
        UserDefaults.standard.set(username, forKey: usernameKey)
    }
}

struct AuthResponse: Codable {
    let token: String
    let userId: String

    enum CodingKeys: String, CodingKey {
        case token
        case userId = "user_id"
    }
}
