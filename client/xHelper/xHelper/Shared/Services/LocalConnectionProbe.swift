import Foundation

/// 「测试连接」的显式状态。
/// 替掉 `isTesting / didTest / testError` 三字段组合——那种写法存在非法中间态
/// （例：`didTest == true && testError == nil` 在探测**进行中**也成立 → 转圈和「连接正常」同屏）。
enum ConnectionTestState: Equatable {
    case idle
    case testing
    case ok
    case failed(String)
}

/// 「测试连接」：把密钥写进 Keychain / 拉起后端**之前**，各发一个最小请求验证。
///
/// 请求构造是纯函数（可 `swift test`）；网络部分是薄封装，只负责归类人话错误。
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
    static func test(_ secrets: LocalSecrets) async -> String? {
        // 非 https 直接拒绝：key 会以明文经网络发出（自建 http 网关同理，不做例外）。
        let base = secrets.normalized().effectiveLLMBaseURL
        if URL(string: base)?.scheme?.lowercased() != "https" {
            return "Base URL 必须是 https（当前：\(base)）。"
        }
        do {
            if let err = await probe(try llmRequest(secrets), target: .llm) { return err }
            if let err = await probe(try embeddingRequest(secrets), target: .embedding) { return err }
            return nil
        } catch {
            return error.localizedDescription
        }
    }

    /// 跑一次探测并归一成 UI 可直接绑定的状态。调用方负责先置 `.testing`。
    static func testState(_ secrets: LocalSecrets) async -> ConnectionTestState {
        if let err = await test(secrets) { return .failed(err) }
        return .ok
    }

    private static func probe(_ req: URLRequest, target: Target) async -> String? {
        do {
            let (data, resp) = try await URLSession.shared.data(for: req)
            let code = (resp as? HTTPURLResponse)?.statusCode ?? 0
            guard (200..<300).contains(code) else {
                return "\(target.rawValue) 验证失败（HTTP \(code)）：\(snippet(data))"
            }
            return nil
        } catch {
            return "\(target.rawValue) 连接失败：\(error.localizedDescription)"
        }
    }

    struct ProbeError: LocalizedError {
        let target: Target
        let message: String
        var errorDescription: String? { "\(target.rawValue)：\(message)" }
    }

    private static func snippet(_ data: Data, limit: Int = 200) -> String {
        let s = String(decoding: data, as: UTF8.self)
        return s.count > limit ? String(s.prefix(limit)) + "…" : s
    }
}
