import Foundation

/// 由工具结果派生、需要「特殊展示 + 本地缓存」的本地产物。
///
/// 扩展方式：加一个 `case` + 对应的 Codable 载荷类型，编译器会强制补齐
/// `init?(type:payload:)` / `type` / `payload()` 三处 switch——不会漏。
/// 渲染分发在 `MessageListView.localArtifactView`（同为 switch 穷尽）。
enum LocalArtifact: Equatable {
    case chart(ChartData)
    /// 文档引用。**当前恒为单元素**（`upsertRoundDocumentRef` 每篇文档各落一条 `[merged]`）——
    /// 条子与「＋N」都按「一个产物 = 一张卡」算；真要一条装多张，得先把条子改成按卡片数展平。
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

    /// 是不是「卡片类」产物：同一轮里的几张会在聊天里合成一条**横向卡片槽**（2026-10-29 起按轮次归组）。
    /// 非卡片的（如 chart，250pt 全宽图表）按原样单独渲染，不进卡片槽。
    /// 渲染侧对应 `MessageListView.stripCard` / `singleCard` 的穷尽 switch。
    var isCardArtifact: Bool {
        switch self {
        case .docRefs: return true
        case .chart: return false
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
