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
            token: "tok",
            secrets: LocalSecrets(llmKey: " lk ", embeddingKey: " ek ")
        )
        XCTAssertEqual(env["APP_ENV"], "local")
        XCTAssertEqual(env["XHAN_CONF_DIR"], "/app/conf")
        XCTAssertEqual(env["XHAN_LOCAL_TOKEN"], "tok")
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
            token: "t",
            secrets: LocalSecrets(llmKey: "a", embeddingKey: "b", llmModel: "deepseek-v4-pro", llmBaseURL: "http://x")
        )
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
        let text = try? XCTUnwrap(msg)
        XCTAssertNotNil(text)
        XCTAssertTrue(text?.contains("https") ?? false, "应提示必须是 https，实际：\(text ?? "nil")")
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
}
