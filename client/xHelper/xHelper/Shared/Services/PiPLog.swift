import Foundation

/// 双层诊断日志助手：DEBUG 直出 Xcode 控制台（print），release 落 LogBuffer（可被
/// LogDiagnosticView 查看、LogFileWriter 持久化）。所有 `[PiP]` / `[Companion]` 埋点统一走这里。
func pipLog(_ message: String, file: String = #file, line: Int = #line) {
    #if DEBUG
    print("[PiP] \(message)")
    #else
    LogBuffer.shared.diag("[PiP] \(message)", file: file, line: line)
    #endif
}

/// 未来 companion（上网伙伴）事件日志，与 pipLog 同策略。
func companionLog(_ message: String, file: String = #file, line: Int = #line) {
    #if DEBUG
    print("[Companion] \(message)")
    #else
    LogBuffer.shared.diag("[Companion] \(message)", file: file, line: line)
    #endif
}
