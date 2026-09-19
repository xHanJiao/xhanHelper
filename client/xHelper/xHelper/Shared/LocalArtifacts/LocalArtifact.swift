import Foundation

/// 由工具结果派生、需要「特殊展示 + 本地缓存」的本地产物。
///
/// 扩展方式：加一个 `case` + 对应的 Codable 载荷类型，编译器会强制补齐
/// `init?(type:payload:)` / `type` / `payload()` 三处 switch——不会漏。
/// 渲染分发在 `MessageListView.localArtifactView`（同为 switch 穷尽）。
enum LocalArtifact: Equatable {
    case chart(ChartData)
    case docRefs([DocumentRef])

    /// 从持久化的 (type, payload) 解码；载荷损坏返回 nil（调用方跳过该条）。
    init?(type: LocalToolResultType, payload: Data) {
        switch type {
        case .chart:
            guard let v = try? JSONDecoder().decode(ChartData.self, from: payload) else { return nil }
            self = .chart(v)
        case .docRefs:
            guard let v = try? JSONDecoder().decode([DocumentRef].self, from: payload) else { return nil }
            self = .docRefs(v)
        }
    }

    var type: LocalToolResultType {
        switch self {
        case .chart: return .chart
        case .docRefs: return .docRefs
        }
    }

    /// 持久化载荷。简单 Codable 结构正常不会失败；真失败由调用方记录并跳过，不静默吞。
    func payload() throws -> Data {
        switch self {
        case .chart(let v): return try JSONEncoder().encode(v)
        case .docRefs(let v): return try JSONEncoder().encode(v)
        }
    }
}
