import Foundation

/// 「测试连接」失败：`message` 是给眼睛看的人话（界面直出），`detail` 是 provider / 系统的原文（hover 才展开）。
///
/// 分两层是因为原始 body 泼在界面上全是噪音（一坨 JSON，还会被 `lineLimit` 切半截），
/// 但它对排查有用 —— 折进 tooltip，不占版面。
struct ProbeFailure: Equatable {
    let message: String
    let detail: String?
}

/// 「测试连接」的显式状态。
/// 替掉 `isTesting / didTest / testError` 三字段组合——那种写法存在非法中间态
/// （例：`didTest == true && testError == nil` 在探测**进行中**也成立 → 转圈和「连接正常」同屏）。
enum ConnectionTestState: Equatable {
    case idle
    case testing
    case ok
    case failed(ProbeFailure)
}

/// 「测试连接」：把密钥写进 Keychain / 拉起后端**之前**，各发一个最小请求验证。
///
/// 请求构造与错误归类都是纯函数（可 `swift test`）；网络部分只是薄封装。
/// 之所以在 Swift 侧直连 provider 而不是先起本地后端：密钥没配好后端根本起不来（chicken-and-egg）。
enum LocalConnectionProbe {

    enum Target: String {
        case llm = "DeepSeek LLM"
        case embedding = "Ark Embedding"
    }

    /// LLM 最小请求：`max_tokens=1` 的 chat/completions（验 key + base_url + model）。
    static func llmRequest(_ raw: LocalSecrets) throws -> URLRequest {
        let s = raw.normalized()
        let base = s.effectiveLLMBaseURL.hasSuffix("/")
            ? String(s.effectiveLLMBaseURL.dropLast())
            : s.effectiveLLMBaseURL
        guard let url = URL(string: base + "/chat/completions") else {
            throw ProbeError(target: .llm, message: "base_url 无效：\(base)")
        }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 15
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(s.llmKey)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "model": s.effectiveLLMModel,
            "messages": [["role": "user", "content": "ping"]],
            "max_tokens": 1,
        ] as [String: Any])
        return req
    }

    /// Embedding 最小请求（Ark multimodal，与后端 `llm/embedding.go` 一致）。
    static func embeddingRequest(_ raw: LocalSecrets) throws -> URLRequest {
        let s = raw.normalized()
        guard let url = URL(string: LocalSecrets.embeddingBaseURL + "/api/v3/embeddings/multimodal") else {
            throw ProbeError(target: .embedding, message: "embedding 地址无效")
        }
        var req = URLRequest(url: url)
        req.httpMethod = "POST"
        req.timeoutInterval = 15
        req.setValue("application/json", forHTTPHeaderField: "Content-Type")
        req.setValue("Bearer \(s.embeddingKey)", forHTTPHeaderField: "Authorization")
        req.httpBody = try JSONSerialization.data(withJSONObject: [
            "model": LocalSecrets.embeddingModel,
            "input": [["type": "text", "text": "ping"]],
            "dimensions": 1024,
        ] as [String: Any])
        return req
    }

    /// 依次测两个 key；返回第一条人话错误，全通过返回 nil。
    static func test(_ secrets: LocalSecrets) async -> ProbeFailure? {
        // 非 https 直接拒绝：key 会以明文经网络发出（自建 http 网关同理，不做例外）。
        let base = secrets.normalized().effectiveLLMBaseURL
        if URL(string: base)?.scheme?.lowercased() != "https" {
            return ProbeFailure(
                message: "Base URL 必须是 https（当前：\(base)）",
                detail: "http 会把密钥明文发到网络上；自建网关也请挂 TLS。"
            )
        }
        do {
            if let err = await probe(try llmRequest(secrets), target: .llm) { return err }
            if let err = await probe(try embeddingRequest(secrets), target: .embedding) { return err }
            return nil
        } catch {
            return ProbeFailure(message: error.localizedDescription, detail: nil)
        }
    }

    /// 跑一次探测并归一成 UI 可直接绑定的状态。调用方负责先置 `.testing`。
    static func testState(_ secrets: LocalSecrets) async -> ConnectionTestState {
        if let err = await test(secrets) { return .failed(err) }
        return .ok
    }

    private static func probe(_ req: URLRequest, target: Target) async -> ProbeFailure? {
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            guard (200..<300).contains(code) else {
                return failure(status: code, body: data, target: target)
            }
            return nil
        } catch {
            return networkFailure(error, target: target)
        }
    }

    // MARK: 错误归类（纯函数，单测覆盖）

    /// HTTP 状态码 → 人话 + provider 原文。
    static func failure(status: Int, body: Data, target: Target) -> ProbeFailure {
        let head: String
        switch status {
        case 400:
            head = "请求被拒（HTTP 400）——model 名或 base_url 可能不对"
        case 401, 403:
            head = "密钥无效或无权访问（HTTP \(status)）"
        case 402:
            head = "账户余额不足（HTTP 402）"
        case 404:
            head = "接口地址不存在（HTTP 404）——检查 base_url 是否多了路径"
        case 429:
            head = "请求被限流（HTTP 429），稍后再试"
        case 500...599:
            head = "provider 服务端错误（HTTP \(status)），稍后重试"
        default:
            head = "验证失败（HTTP \(status)）"
        }
        return ProbeFailure(message: "\(target.rawValue)：\(head)", detail: providerMessage(body))
    }

    /// 网络层错误 → 人话。`detail` 留系统原文（英文），够定位就行。
    static func networkFailure(_ error: Error, target: Target) -> ProbeFailure {
        let ns = error as NSError
        let head: String
        switch (ns.domain, ns.code) {
        case (NSURLErrorDomain, NSURLErrorTimedOut):
            head = "连接超时（15 秒）——网络不通或地址不可达"
        case (NSURLErrorDomain, NSURLErrorCannotFindHost),
             (NSURLErrorDomain, NSURLErrorDNSLookupFailed):
            head = "域名解析失败——检查 base_url 拼写"
        case (NSURLErrorDomain, NSURLErrorNotConnectedToInternet):
            head = "本机网络不可用"
        case (NSURLErrorDomain, NSURLErrorCannotConnectToHost),
             (NSURLErrorDomain, NSURLErrorNetworkConnectionLost):
            head = "连不上主机（连接被拒或中断）"
        case (NSURLErrorDomain, NSURLErrorSecureConnectionFailed),
             (NSURLErrorDomain, NSURLErrorServerCertificateUntrusted):
            head = "TLS 握手失败——证书不受信任"
        default:
            head = "连接失败"
        }
        return ProbeFailure(message: "\(target.rawValue)：\(head)", detail: ns.localizedDescription)
    }

    /// 只摘 provider 自己那一句（DeepSeek / Ark 都是 `error.message`），摘不到就 nil。
    /// **绝不把原始 body 泼到界面上**：非 JSON body（网关的 HTML 错误页）解析失败即视为「没有原文」，是有意降级。
    static func providerMessage(_ body: Data) -> String? {
        guard let obj = try? JSONSerialization.jsonObject(with: body) as? [String: Any] else { return nil }
        let msg = ((obj["error"] as? [String: Any])?["message"] as? String)
            ?? (obj["error"] as? String)
            ?? (obj["message"] as? String)
        guard let msg, !msg.isEmpty else { return nil }
        return msg.count > 300 ? String(msg.prefix(300)) + "…" : msg
    }

    struct ProbeError: LocalizedError {
        let target: Target
        let message: String
        var errorDescription: String? { "\(target.rawValue)：\(message)" }
    }
}
