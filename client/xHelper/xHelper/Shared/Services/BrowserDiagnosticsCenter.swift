import Combine
import Foundation
import SwiftUI

/// 浏览器异常诊断中心：浏览器导航回调把长尾异常（WebContent 进程回收/导航失败/加载超时）
/// 上报到这里 → 持久化到 LogBuffer（release 也落盘，供 LogDiagnosticView 查）+ 右上角浮动卡片
/// 弹窗携带结构化信息，可一键复制贴给开发。
@MainActor
final class BrowserDiagnosticsCenter: ObservableObject {
    static let shared = BrowserDiagnosticsCenter()

    enum EventType: String {
        case processTerminated = "WebContent 进程被系统回收"
        case navigationFailed = "导航失败"
        case loadTimeout = "页面加载超时"
    }

    struct DiagnosticEvent {
        let type: EventType
        let timestamp: Date
        let appVersion: String
        let host: String?
        let url: String?
        let tabId: String?
        let isAgentTab: Bool?
        let provCommitMs: Int?
        let sinceCommitMs: Int?
        let errorCode: Int?
        let errorDescription: String?

        var copyText: String {
            var lines: [String] = []
            lines.append("[\(type.rawValue)]")
            lines.append("时间: \(BrowserDiagnosticsCenter.timeFormatter.string(from: timestamp))")
            lines.append("版本: \(appVersion)")
            if let host { lines.append("Host: \(host)") }
            if let url { lines.append("URL: \(url)") }
            if let tabId {
                lines.append("Tab: \(tabId)\(isAgentTab == true ? " (agent)" : "")")
            }
            var timing: [String] = []
            if let provCommitMs { timing.append("prov→commit=\(provCommitMs)ms") }
            if let sinceCommitMs { timing.append("commit→now=\(sinceCommitMs)ms") }
            if !timing.isEmpty { lines.append("时序: \(timing.joined(separator: " | "))") }
            if let errorCode { lines.append("错误码: \(errorCode)") }
            if let errorDescription { lines.append("错误: \(errorDescription)") }
            return lines.joined(separator: "\n")
        }
    }

    /// 当前待展示的弹窗事件（nil = 无弹窗）
    @Published private(set) var current: DiagnosticEvent?

    /// 同类型弹窗限流（30s 内只弹一次，避免连环崩刷屏；日志不受限，全部落盘）
    private var lastPopupAt: [EventType: Date] = [:]
    private let rateLimitInterval: TimeInterval = 30

    private init() {}

    /// 上报事件：全部写 LogBuffer 持久化；弹窗按类型限流。
    func capture(_ event: DiagnosticEvent) {
        LogBuffer.shared.diag("[BrowserDiag] \(event.copyText)")
        if let last = lastPopupAt[event.type],
           Date().timeIntervalSince(last) < rateLimitInterval {
            return
        }
        lastPopupAt[event.type] = Date()
        current = event
    }

    func dismiss() {
        current = nil
    }

    static func appVersion() -> String {
        let ver = Bundle.main.object(forInfoDictionaryKey: "CFBundleShortVersionString") as? String ?? "?"
        let build = Bundle.main.object(forInfoDictionaryKey: "CFBundleVersion") as? String ?? "?"
        return "\(ver) (\(build))"
    }

    private static let timeFormatter: DateFormatter = {
        let f = DateFormatter()
        f.dateFormat = "yyyy-MM-dd HH:mm:ss"
        return f
    }()
}
