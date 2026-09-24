import XCTest
@testable import LocalBackendCore

final class LocalBackendDiagnosticsTests: XCTestCase {

    func testMissingSecrets() {
        XCTAssertEqual(
            LocalBackendDiagnostics.missingSecrets(LocalSecrets()).sorted(),
            ["Ark Embedding Key", "DeepSeek LLM Key"]
        )
        XCTAssertEqual(
            LocalBackendDiagnostics.missingSecrets(LocalSecrets(llmKey: "x", embeddingKey: "  ")),
            ["Ark Embedding Key"]
        )
        XCTAssertTrue(
            LocalBackendDiagnostics.missingSecrets(LocalSecrets(llmKey: "x", embeddingKey: "y")).isEmpty
        )
    }

    func testChildEnvironmentOverridesLocalKeys() {
        let env = LocalBackendDiagnostics.childEnvironment(
            base: ["PATH": "/bin", "APP_ENV": "prod", "LLM_API_KEY": "stale"],
            confDir: "/app/conf",
            appEnv: "local.dev",
            token: "dev:tok",
            secrets: LocalSecrets(llmKey: " lk ", embeddingKey: " ek ")
        )
        XCTAssertEqual(env["APP_ENV"], "local.dev", "应把构建对应的 APP_ENV 传给子进程")
        XCTAssertEqual(env["XHAN_CONF_DIR"], "/app/conf")
        XCTAssertEqual(env["XHAN_LOCAL_TOKEN"], "dev:tok")
        XCTAssertEqual(env["LLM_API_KEY"], "lk", "应以 Keychain 密钥覆盖环境里残留的旧值，并去空白")
        XCTAssertEqual(env["EMBEDDING_API_KEY"], "ek")
        XCTAssertEqual(env["PATH"], "/bin")
        XCTAssertNil(env["XHAN_LLM_MODEL"], "可选覆盖项为空时不传，让后端用默认")
        XCTAssertNil(env["XHAN_LLM_BASE_URL"])
    }

    func testChildEnvironmentPassesOptionalOverrides() {
        let env = LocalBackendDiagnostics.childEnvironment(
            base: [:],
            confDir: "/c",
            appEnv: "local",
            token: "prod:t",
            secrets: LocalSecrets(llmKey: "a", embeddingKey: "b", llmModel: "deepseek-v4-pro", llmBaseURL: "http://x")
        )
        XCTAssertEqual(env["APP_ENV"], "local")
        XCTAssertEqual(env["XHAN_LLM_MODEL"], "deepseek-v4-pro")
        XCTAssertEqual(env["XHAN_LLM_BASE_URL"], "http://x")
    }

    func testSecretsDefaults() {
        let s = LocalSecrets(llmKey: "a", embeddingKey: "b")
        XCTAssertEqual(s.effectiveLLMModel, LocalSecrets.defaultLLMModel)
        XCTAssertEqual(s.effectiveLLMBaseURL, LocalSecrets.defaultLLMBaseURL)
        XCTAssertTrue(s.isComplete)

        let n = LocalSecrets(llmKey: "  a ", embeddingKey: "b", llmModel: " m ").normalized()
        XCTAssertEqual(n.llmKey, "a")
        XCTAssertEqual(n.llmModel, "m")
        XCTAssertFalse(LocalSecrets(llmKey: "", embeddingKey: "b").isComplete)
    }

    func testProbeRequestBuilding() throws {
        let llmReq = try LocalConnectionProbe.llmRequest(
            LocalSecrets(llmKey: "k", embeddingKey: "e", llmModel: "m", llmBaseURL: "https://api.deepseek.com/")
        )
        XCTAssertEqual(llmReq.url?.absoluteString, "https://api.deepseek.com/chat/completions")
        XCTAssertEqual(llmReq.value(forHTTPHeaderField: "Authorization"), "Bearer k")
        let llmBody = try XCTUnwrap(llmReq.httpBody)
        let llmJSON = try XCTUnwrap(try JSONSerialization.jsonObject(with: llmBody) as? [String: Any])
        XCTAssertEqual(llmJSON["model"] as? String, "m")
        XCTAssertEqual(llmJSON["max_tokens"] as? Int, 1)

        let embReq = try LocalConnectionProbe.embeddingRequest(LocalSecrets(llmKey: "k", embeddingKey: "e"))
        XCTAssertEqual(
            embReq.url?.absoluteString,
            LocalSecrets.embeddingBaseURL + "/api/v3/embeddings/multimodal"
        )
        XCTAssertEqual(embReq.value(forHTTPHeaderField: "Authorization"), "Bearer e")
    }

    func testProbeRejectsNonHTTPSBaseURL() async {
        // 非 https 直接拒绝（且发生在发请求之前，所以这条测试不触网）。
        let msg = await LocalConnectionProbe.test(
            LocalSecrets(llmKey: "k", embeddingKey: "e", llmBaseURL: "http://gateway.local")
        )
        XCTAssertTrue(
            msg?.message.contains("https") ?? false,
            "应提示必须是 https，实际：\(msg?.message ?? "nil")"
        )
    }

    func testHintClassification() {
        XCTAssertNotNil(LocalBackendDiagnostics.hint(
            logTail: "listen tcp 127.0.0.1:18923: bind: address already in use"
        ))
        XCTAssertNotNil(LocalBackendDiagnostics.hint(
            logTail: "missing required LLM_API_KEY"
        ))
        XCTAssertNotNil(LocalBackendDiagnostics.hint(
            logTail: "unable to open database file"
        ))
        XCTAssertNil(LocalBackendDiagnostics.hint(
            logTail: "server starting on 127.0.0.1:18923 (mode: debug)"
        ))
    }

    func testHTTPFailureIsHumanReadableAndKeepsDetailForHover() {
        let body = Data(#"{"error":{"message":"Authentication Fails, Your api key: ****ee72 is invalid"}}"#.utf8)
        let f = LocalConnectionProbe.failure(status: 401, body: body, target: .llm)
        XCTAssertTrue(f.message.contains("密钥无效"), f.message)
        XCTAssertFalse(f.message.contains("Authentication Fails"), "人话里不该泼原始 JSON")
        XCTAssertEqual(f.detail, "Authentication Fails, Your api key: ****ee72 is invalid")

        XCTAssertTrue(
            LocalConnectionProbe.failure(status: 404, body: Data(), target: .llm).message.contains("base_url"),
            "404 应提示 base_url 多了路径"
        )
        XCTAssertNil(
            LocalConnectionProbe.failure(status: 502, body: Data("<html>bad gateway</html>".utf8), target: .embedding).detail,
            "非 JSON body 不硬凑原文"
        )
    }

    func testNetworkFailureIsHumanReadable() {
        let timeout = LocalConnectionProbe.networkFailure(
            NSError(domain: NSURLErrorDomain, code: NSURLErrorTimedOut),
            target: .embedding
        )
        XCTAssertTrue(timeout.message.contains("超时"), timeout.message)
    }
}
