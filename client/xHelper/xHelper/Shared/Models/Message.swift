import Foundation

struct Message: Identifiable, Codable {
    // id / content / toolCallsJSON 必须是 var：调用方要能对消息做"原地改"。
    // 不要用 memberwise init 手工重建消息——新增字段会被静默丢掉，附件图
    // （imageIdsJSON/localImages）就是这样在流结束后消失过。
    var id: String
    let conversationId: String
    let role: String         // user | assistant | tool
    var content: String
    var toolCallsJSON: String?
    let reasoningContent: String? = nil
    let memoryIds: [String]? = nil
    let toolCallId: String? = nil
    let createdAt: String
    /// 附件图片 id。服务端下发的是 JSON 数组字符串，用 `imageIds` 读展开结果。
    /// 注意必须是 var 带默认值：let 带初值不会被 Codable 解码。
    var imageIdsJSON: String? = nil

    var localToolResult: LocalToolResultData? = nil
    /// 本地产物的解码结果（chart / docRefs…）。瞬态：持久化的是 `localToolResult.payload`，
    /// 由 `mergeLocalToolResults` 在加载/插入时解码。
    var localArtifact: LocalArtifact? = nil
    /// 本条消息产出的 HTML 产物引用（服务端 `artifacts` 字段）。正文不在这里。
    /// 必须 Optional：服务端 `omitempty` 会把空数组整个省掉，而合成的 `Decodable`
    /// **不会**用属性默认值兜缺 key（会抛 keyNotFound，整条消息列表解码失败）。
    var artifacts: [ArtifactRef]?

    /// 读用这个（nil 归一成空数组）。
    var artifactRefs: [ArtifactRef] { artifacts ?? [] }
    /// 刚发出、还没从服务端拉回时的内存缩略图（仅本地展示，不入库）
    var localImages: [PlatformImage] = []

    /// 附件图片 id 列表（解析服务端下发的 JSON 数组字符串）
    var imageIds: [String] {
        guard let json = imageIdsJSON, let data = json.data(using: .utf8) else { return [] }
        return (try? JSONDecoder().decode([String].self, from: data)) ?? []
    }

    enum CodingKeys: String, CodingKey {
        case id
        case conversationId = "conversation_id"
        case role
        case content
        case toolCallsJSON = "tool_calls_json"
        case reasoningContent = "reasoning_content"
        case memoryIds = "memory_ids"
        case imageIdsJSON = "image_ids"
        case toolCallId = "tool_call_id"
        case artifacts
        case createdAt = "created_at"
    }

    /// 本地构建 user 消息（未入库前）
    static func localUser(text: String, conversationId: String, imageIds: [String] = [], localImages: [PlatformImage] = []) -> Message {
        var msg = Message(
            id: UUID().uuidString,
            conversationId: conversationId,
            role: "user",
            content: text,
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
        if !imageIds.isEmpty {
            msg.imageIdsJSON = (try? JSONEncoder().encode(imageIds))
                .flatMap { String(data: $0, encoding: .utf8) }
        }
        msg.localImages = localImages
        return msg
    }

    /// 本地构建 streaming assistant 消息
    static func streamingAssistant(conversationId: String) -> Message {
        Message(
            id: "__streaming__",
            conversationId: conversationId,
            role: "assistant",
            content: "",
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
    }

    /// 返回清除 tool_calls_json 后的消息，不展开为独立 tool 消息
    func withoutToolCalls() -> [Message] {
        var msg = self
        msg.toolCallsJSON = nil
        return [msg]
    }
}
