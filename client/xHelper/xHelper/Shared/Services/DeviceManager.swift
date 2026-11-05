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
    @Published var knownUsernames: [String] = []

    #if os(macOS)
    /// 本地模式：后端已 ready 但载入默认会话失败时的错误（供启动门展示 + 重试）。
    @Published var localBootstrapError: String?
    /// 防止 `.task` 与 `.onChange` 两条路径并发重入 `onLocalBackendReady()`。
    private var isLoadingLocalBootstrap = false
    #endif

    private let tokenKey = "xhan_user_token"
    private let userIdKey = "xhan_user_id"
    private let usernameKey = "xhan_username"
    private let knownUsernamesKey = "xhan_known_usernames"
    private let maxKnownUsernames = 10
    private let defaultConvKey = "xhan_default_conversation_id"
    private var refreshTimer: Timer?
    private let refreshInterval: TimeInterval = 3600

    #if DEBUG
    static let defaultBaseURL = "http://localhost:8080"
    #else
    static let defaultBaseURL = "https://helper.xhanx.cn"
    #endif

    var baseURL: String {
        #if os(macOS)
        // 本地模式：连 app 自拉起的内嵌后端。
        if LocalBackendManager.isActive {
            return LocalBackendManager.shared.baseURL
        }
        // 云端模式：显式设了 xhan_server_url 就改用它（逃生口），否则连默认云端地址。
        // 注意：逃生口填错环境（dev 填 prod 端口）会被后端按 token 前缀 403 拒绝。
        if let override = LocalPaths.serverURLOverride {
            return override
        }
        return Self.defaultBaseURL
        #elseif DEBUG
        return LocalPaths.serverURLOverride ?? Self.defaultBaseURL
        #else
        return Self.defaultBaseURL
        #endif
    }

    /// 网页链接（分享页等前端页面）的源地址。
    /// 本地开发：前端跑在 5173（Vite）、后端 8080，两者不同源，分享链接必须指向前端；
    /// 线上：前端与 API 同源（helper.xhanx.cn）。
    #if DEBUG
    static let defaultWebBaseURL = "http://localhost:5173"
    #else
    static let defaultWebBaseURL = "https://helper.xhanx.cn"
    #endif

    static var webBaseURL: String {
        #if os(macOS)
        if let override = LocalPaths.webURLOverride {
            return override
        }
        // 逃生口（服务地址指向远端/自定义后端）时前端不在本地端口上——回退默认
        // （Debug=Vite 5173 / Release=helper.xhanx.cn）。不能跟着本地端口走，否则
        // 分享页 / 浏览·调试面板会指到没人监听的 127.0.0.1，白屏且无提示。
        if !LocalBackendManager.isActive {
            return Self.defaultWebBaseURL
        }
        // 本地模式：前端由内嵌 Go 托管，与 API 同源。
        return LocalBackendManager.shared.baseURL
        #elseif DEBUG
        return LocalPaths.webURLOverride ?? Self.defaultWebBaseURL
        #else
        return Self.defaultWebBaseURL
        #endif
    }

    init() {
        #if os(macOS)
        if LocalBackendManager.isActive {
            // 本地模式：无登录流程，token 由 app 生成、与后端共享；等后端 ready 再载默认会话。
            self.token = LocalBackendManager.shared.token
            self.userId = nil
            self.username = nil
            self.knownUsernames = []
            self.isLoggedIn = false
            LogBuffer.shared.info("app launched in local backend mode")
            updateActivityRecorder(token: self.token)
            return
        }
        #endif
        #if os(macOS)
        // 非本地模式的启动日志：便于确认当前连的是哪个后端（Debug=localhost:8080 / Release=云端）。
        LogBuffer.shared.info("app launched in server mode (server=\(baseURL))")
        #endif
        self.token = UserDefaults.standard.string(forKey: tokenKey)
        self.userId = UserDefaults.standard.string(forKey: userIdKey)
        self.username = UserDefaults.standard.string(forKey: usernameKey)
        self.knownUsernames = UserDefaults.standard.stringArray(forKey: knownUsernamesKey) ?? []
        if let current = self.username, !self.knownUsernames.contains(current) {
            self.knownUsernames.insert(current, at: 0)
        }
        self.isLoggedIn = token != nil
        LogBuffer.shared.info("app launched, hasToken=\(self.isLoggedIn ? "yes" : "no")")
        updateActivityRecorder(token: self.token)
        if self.isLoggedIn {
            self.isVerifying = true
            Task { await verifyToken() }
        }
    }

    // MARK: Activity（活动埋点）

    /// 本地模式必采；云端按服务端 `/api/health` 能力探测结果（iOS 不采集，monitors 为 macOS-only）。
    private var activityCollectionActive: Bool {
        #if os(macOS)
        return ActivityClient.isEnabled
        #else
        return false
        #endif
    }

    #if os(macOS)
    /// 探测云端是否开启活动埋点，并据此启停搬运。本地模式直接返回（isEnabled 恒真）。
    /// 令牌就绪后由 `verifyToken` / `login` / `register` 调用；探测结果存进 `ActivityCapability`。
    private func refreshActivityCapability() async {
        guard !LocalBackendManager.isActive else { return }
        let enabled = await ActivityCapability.probe(baseURL: baseURL)
        ActivityCapability.remoteEnabled = enabled
        // enabled=false 时传 nil → 停掉可能已在跑的搬运；本地模式已被上面的 guard 挡住。
        updateActivityRecorder(token: enabled ? token : nil)
    }
    #endif

    /// token 就绪 → 注入传输并启动周期搬运；token 清空 → 停止。
    /// 后端未就绪时空转（P9 尚无事件生产者；P10 起由在场/电源事件填充）。
    ///
    /// transport 快照了当时的 token/baseURL：本地模式 token 固定、`onLocalBackendReady` 不启 refresh
    /// （`startTokenRefresh` 只在 verifyToken/login/register 调），故快照安全；**若将来本地也要轮换 token，
    /// 必须在这里重新 configure**，否则旧 token 会一直 401。
    /// configure/start 与 stop 各走独立的非结构化 `Task`，理论可乱序；本地无登出/轮换路径，暂不可达。
    private func updateActivityRecorder(token: String?) {
        // 先处理「清空 token = 停止」：与采集开关无关，登出/能力关闭都要停。
        guard let token, !token.isEmpty else {
            Task {
                await ActivityRecorder.shared.stop()
                #if os(macOS)
                ActivityPresenceMonitor.shared.stop()
                ActivityInputMonitor.shared.stop()
                #endif
            }
            return
        }
        guard activityCollectionActive else { return }
        let baseURL = self.baseURL
        let transport: ActivityTransport = { segmentID, events in
            let api = APIClient(baseURL: baseURL, token: token)
            do {
                return try await api.uploadActivityEvents(segmentId: segmentID, events: events)
            } catch let error as NSError {
                // 401（token 过期）当可重试：刷新后自然成功；400/413 等段本身有问题 → 隔离。
                if error.domain == "APIClient", (400..<500).contains(error.code), error.code != 401 {
                    throw ActivityUploadError.client(error)
                }
                throw ActivityUploadError.retryable(error)
            }
        }
        Task {
            await ActivityRecorder.shared.configure(transport: transport)
            await ActivityRecorder.shared.start()
            #if os(macOS)
            ActivityPresenceMonitor.shared.start()
            ActivityInputMonitor.shared.start()
            #endif
        }
    }

    private func verifyToken() async {
        await performTokenRefresh()
        #if os(macOS)
        // 令牌有效后探测云端埋点能力，决定是否启动搬运（探测失败=不采）。
        await refreshActivityCapability()
        #endif
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

    #if os(macOS)
    /// 本地后端就绪后调用：接管本地 token 并加载默认会话（本地没有登录流程）。
    /// 幂等：`.task` 与 `.onChange` 都可能触发，已在跑 / 已登录就直接返回。
    func onLocalBackendReady() async {
        guard LocalBackendManager.isActive else { return }
        guard !isLoggedIn, !isLoadingLocalBootstrap else { return }
        isLoadingLocalBootstrap = true
        localBootstrapError = nil
        token = LocalBackendManager.shared.token
        updateActivityRecorder(token: token)
        isVerifying = true
        defer {
            isVerifying = false
            isLoadingLocalBootstrap = false
        }
        do {
            try await getDefaultConversationInfo()
            isLoggedIn = true
            LogBuffer.shared.info("local backend ready: default conversation loaded")
        } catch {
            // 不静默：暴露给启动门展示错误态 + 重试，别让 UI 永久停在 spinner。
            localBootstrapError = "本地服务已就绪，但载入会话失败：\(error.localizedDescription)"
            LogBuffer.shared.error("local: 加载默认会话失败: \(error)")
        }
    }
    #endif

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
        updateActivityRecorder(token: nil)
    }

    func logout() async {
        #if os(macOS)
        if LocalBackendManager.isActive {
            // 本地模式没有登出概念；访问门由 Touch ID 管（2B-4）。
            return
        }
        #endif
        if let t = token {
            do {
                var req = URLRequest(url: try requestURL("/api/auth/logout"))
                req.httpMethod = "POST"
                req.setValue("Bearer \(t)", forHTTPHeaderField: "Authorization")
                _ = try await URLSession.shared.data(for: req)
            } catch {
                // 登出是尽力而为：清本地态优先，失败只记录
                LogBuffer.shared.warning("logout request failed: \(error.localizedDescription)")
            }
        }
        clearAuthState()
    }

    /// 构造请求 URL；服务地址不合法时抛可见错误，不用 `!` 崩掉 app。
    private func requestURL(_ path: String) throws -> URL {
        guard let url = URL(string: "\(baseURL)\(path)") else {
            throw NSError(domain: "APIClient", code: -1000,
                userInfo: [NSLocalizedDescriptionKey: "服务地址无效：\(baseURL)"])
        }
        return url
    }

    func login(username: String, password: String) async throws {
        let body = ["username": username, "password": password]
        let data = try JSONEncoder().encode(body)
        var req = URLRequest(url: try requestURL("/api/auth/login"))
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
        var req = URLRequest(url: try requestURL("/api/auth/register"))
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
        #if os(macOS)
        // 先探测云端埋点能力再启停搬运（refreshActivityCapability 内部按 token 启停）。
        Task { await refreshActivityCapability() }
        #else
        updateActivityRecorder(token: result.token)
        #endif
        UserDefaults.standard.set(result.token, forKey: tokenKey)
        UserDefaults.standard.set(result.userId, forKey: userIdKey)
        UserDefaults.standard.set(username, forKey: usernameKey)
        rememberUsername(username)
    }

    private func rememberUsername(_ name: String) {
        guard !name.isEmpty else { return }
        var list = knownUsernames.filter { $0 != name }
        list.insert(name, at: 0)
        if list.count > maxKnownUsernames {
            list = Array(list.prefix(maxKnownUsernames))
        }
        knownUsernames = list
        UserDefaults.standard.set(list, forKey: knownUsernamesKey)
    }
    
    private func getDefaultConversationInfo() async throws {
        var req = URLRequest(url: try requestURL("/api/conversations/default"))
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
    let mode: String?

    enum CodingKeys: String, CodingKey {
        case agentId = "agent_id"
        case agentName = "agent_name"
        case createdAt = "created_at"
        case id
        case isDefault = "is_default"
        case title
        case updatedAt = "updated_at"
        case mode
    }
}
