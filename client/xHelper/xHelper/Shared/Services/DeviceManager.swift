import Combine
import Foundation

@MainActor
class DeviceManager: ObservableObject {
    @Published var token: String?
    @Published var deviceId: String?
    @Published var isRegistered = false

    private let tokenKey = "xhan_device_token"
    private let deviceIdKey = "xhan_device_id"

    // 与服务器 REGISTRATION_SECRET 一致
    static let registrationSecret = "dev-secret"

    var baseURL: String {
        UserDefaults.standard.string(forKey: "xhan_server_url") ?? "http://localhost:8080"
    }

    init() {
        self.token = UserDefaults.standard.string(forKey: tokenKey)
        self.deviceId = UserDefaults.standard.string(forKey: deviceIdKey)
        self.isRegistered = token != nil
    }

    func clearToken() {
        UserDefaults.standard.removeObject(forKey: tokenKey)
        UserDefaults.standard.removeObject(forKey: deviceIdKey)
        token = nil
        deviceId = nil
        isRegistered = false
    }

    // isRecovery: true 表示 token 过期正在恢复，false 表示首次注册
    var isRecovery: Bool {
        // 如果之前有 token 但现在被清了，说明是恢复场景
        UserDefaults.standard.string(forKey: tokenKey) == nil && UserDefaults.standard.string(forKey: deviceIdKey) == nil
    }

    func register(name: String, platform: String) async throws {
        let body: [String: String] = [
            "device_name": name,
            "platform": platform,
            "secret": Self.registrationSecret,
        ]
        let data = try JSONEncoder().encode(body)

        var req = URLRequest(url: URL(string: "\(baseURL)/api/devices/register")!)
        req.httpMethod = "POST"
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.httpBody = data

        let (respData, resp) = try await URLSession.shared.data(for: req)
        guard (resp as? HTTPURLResponse)?.statusCode == 200 else {
            let msg = String(data: respData, encoding: .utf8) ?? "unknown"
            throw NSError(domain: "DeviceManager", code: 403, userInfo: [NSLocalizedDescriptionKey: msg])
        }

        let result = try JSONDecoder().decode(RegisterResponse.self, from: respData)
        self.token = result.deviceToken
        self.deviceId = result.deviceId
        self.isRegistered = true

        UserDefaults.standard.set(result.deviceToken, forKey: tokenKey)
        UserDefaults.standard.set(result.deviceId, forKey: deviceIdKey)
    }
}

struct RegisterResponse: Codable {
    let deviceToken: String
    let deviceId: String

    enum CodingKeys: String, CodingKey {
        case deviceToken = "device_token"
        case deviceId = "device_id"
    }
}
