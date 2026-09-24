import XCTest
@testable import LocalBackendCore

final class LocalBackendDiagnosticsTests: XCTestCase {

    func testMissingEnv() {
        XCTAssertEqual(
            LocalBackendDiagnostics.missingEnv([:]).sorted(),
            ["EMBEDDING_API_KEY", "LLM_API_KEY"]
        )
        XCTAssertEqual(
            LocalBackendDiagnostics.missingEnv(["LLM_API_KEY": "x", "EMBEDDING_API_KEY": ""]),
            ["EMBEDDING_API_KEY"]
        )
        XCTAssertTrue(
            LocalBackendDiagnostics.missingEnv(["LLM_API_KEY": "x", "EMBEDDING_API_KEY": "y"]).isEmpty
        )
    }

    func testChildEnvironmentOverridesLocalKeys() {
        let env = LocalBackendDiagnostics.childEnvironment(
            base: ["PATH": "/bin", "APP_ENV": "prod"],
            confDir: "/app/conf",
            token: "tok"
        )
        XCTAssertEqual(env["APP_ENV"], "local")
        XCTAssertEqual(env["XHAN_CONF_DIR"], "/app/conf")
        XCTAssertEqual(env["XHAN_LOCAL_TOKEN"], "tok")
        XCTAssertEqual(env["PATH"], "/bin")
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
