import Combine
import Foundation
import SwiftUI
#if os(macOS)
import UserNotifications
#endif
#if os(iOS)
import UIKit
#endif
#if os(macOS)
import AppKit
#endif

// MARK: - Plan Node

struct PlanNode: Identifiable {
    let id: String
    var label: String
    var status: String // running | done | failed
}

// MARK: - Tool Confirmation（L3 工具执行前用户确认）

struct ToolConfirmation: Identifiable {
    /// 单条确认条等待用户操作的超时秒数（队首起算）。UI 文案与计时共用一个源。
    static let timeoutSeconds: TimeInterval = 180
    let id: UUID
    let toolName: String
    let displayName: String
    let argsSummary: String
    /// shell 命令的中文解释（人话）：确认条主展示，用户不必读原始命令。nil = 无
    let explain: String?
    /// diff 预览（fs 写工具）：unified patch 文本 + 增减统计 + 目标路径。nil = 无 diff 展示
    let diffText: String?
    let additions: Int?
    let deletions: Int?
    let filePath: String?
    /// L4 高危（如 shell 危险命令）：红色 UI，且必经确认（已无免确认档）
    let hardened: Bool
    let continuation: CheckedContinuation<Bool, Never>
}

// MARK: - User Request（子任务 ask_user 用户介入）

struct UserRequest: Identifiable {
    let id: String // call_id
    let message: String
    let owner: String?
    /// 该 owner 有待人工处理的手递手标签（AskUserBar 显示 [前往处理] 按钮）
    let hasHandoffTab: Bool
    let continuation: CheckedContinuation<String?, Never> // nil=跳过/超时；""=已处理无文本；非空=用户回答
}

// MARK: - Subagent Session（子任务活动面板）

struct SubagentSession: Identifiable {
    let convID: String
    let description: String
    var state: String      // started/completed/timeout/failed
    var round: Int?
    var latestAction: String?
    var summary: String?
    var expanded: Bool = false
    var messages: [Message] = []
    var id: String { convID }
}

// MARK: - 工具调用展示（tool_call / tool_result 事件）

struct ToolCallDisplay: Identifiable {
    let id = UUID()
    let display: String
    var status: Status
    var summary: String?

    enum Status: Equatable {
        case running
        case done
        case failed
    }
}

// MARK: - 响应检验状态（verify 事件）

struct VerifyStatus: Equatable {
    let status: String // checking | done | retry
    let retry: Int
    let reason: String
}

/// 执行计划生命周期状态（客户端从 steps 推断）
enum PlanState: Equatable {
    case none        // 无计划
    case active      // 进行中（存在 pending/in_progress）
    case completed   // 已完成（无进行中步骤且非全取消）
    case cancelled   // 已取消（全部 cancelled）
}

/// 会话模式二档（与服务端 mode 字符串一一对应）：
///   - off    关闭：工具全量可用
///   - normal 计划：仅只读工具（检索/查询）
enum ChatMode: String {
    case off = "work"
    case normal = "plan"

    /// 循环下一档：关 → 计划 → 关
    var next: ChatMode {
        switch self {
        case .off: return .normal
        case .normal: return .off
        }
    }

    /// 是否处于计划模式（禁写；客户端工具仅放行 fs 的 glob/grep/read）。
    var isPlanLike: Bool { self != .off }
}

// MARK: - ChatViewModel

@MainActor
class ChatViewModel: ObservableObject {
    @Published var messages: [Message] = []
    @Published var isStreaming = false
    // 续传锚点：本轮最后一帧的 seq / turn_id（meta 帧下发）。每轮 sendMessage 归零。
    private(set) var lastEventSeq: Int64 = 0
    private(set) var currentTurnID = ""
    /// 懒创建会话在 done 之前先从 meta 拿到的 id。不动 conversationId：
    /// 空白 tab 要靠「done 时 conversationId 仍为空」判定是否补落盘本地产物。
    private(set) var pendingConversationID = ""
    /// 本轮已处理过的客户端工具 call_id：续传会重放 client_tool_call，
    /// 别二次弹审批 / 二次执行（服务端对已消费的 id 会回 404）。
    private(set) var handledClientToolCallIDs: Set<String> = []

    /// 标记一个客户端工具调用已处理；返回 false 表示是续传重放，调用方应跳过。
    func markClientToolCallHandled(_ callID: String) -> Bool {
        handledClientToolCallIDs.insert(callID).inserted
    }
    @Published var thoughtMessage: String?
    @Published var thoughtDone = false
    @Published var statusMessage: String?
    @Published var errorMessage: String?
    @Published var conversationId: String?
    @Published var conversationTitle: String?
    @Published var isEditing = false
    @Published var deepMode = false
    @Published var thinkingMode = true
    @Published var temperature: Double? = nil
    @Published var maxTokens: Int? = nil
    @Published var draftText = ""
    /// 待发附件（草稿区缩略图）。必须放 VM 而不是 ChatView 的 @State：进出 PiP 时
    /// 中栏 ChatView 会被拆掉重建，视图私有状态跟着没，草稿图就丢了。
    @Published var draftAttachments: [DraftAttachment] = []
    /// 待发图上传在途（发送闸门）。同样必须在 VM：上传 Task 不随视图销毁取消，
    /// 标志留在视图里会被重建复位成 false，闸门失效并可能让还原路径覆盖新草稿。
    @Published var isUploadingAttachments = false

    /// ⌘V 粘贴进来的图片字节（一次性投递箱）：剪贴板在 AppDelegate 里读，ChatView 消费后清空。
    @Published var pastedImages: [Data] = []

    func enqueuePastedImages(_ datas: [Data]) {
        guard !datas.isEmpty else { return }
        pastedImages.append(contentsOf: datas)
    }

    /// 当前上下文 token 数（即时，随 done 事件更新）
    @Published var contextTokens = 0
    /// 上下文窗口上限（按模型档位，0=未配置）
    @Published var contextWindow = 0
    /// 会话消耗金额（慢更新，来自 usage 接口）
    @Published var usage: ConversationUsage?

    /// 响应检验（verify）开关，会话级：默认关。开=强制开启，关=强制关闭（后端全局配置作兜底）
    @Published var verifyEnabled = false
    /// 精炼回答开关，会话级：默认关。开=服务端往 system prompt 注入「≤500 字」输出约束，
    /// 用于让回答短到能整段存进文档批注。不走 max_tokens（那是静默硬截断）。
    @Published var briefAnswer = false
    /// 语音输入识别完成后置位：下一次发送携带 speakReply=true（朗读回复）
    var pendingVoiceReply = false
    /// 本轮是否为语音来源（发送时快照，done 后消费）
    private var speakReplyForCurrentTurn = false
    /// 本轮正常完成时回调回复正文（语音模式用于 TTS）
    var onAssistantReplyDone: ((String) -> Void)?

    // MARK: 无痕（ephemeral）

    /// 空白 tab 的「待生效」无痕意图：点击只改本地，首条消息经 /chat {ephemeral:true} 落库。
    @Published private(set) var ephemeralIntent = false
    /// 已有会话的服务端无痕标记（loadConversation/seedEphemeral 带入，PATCH 成功后更新）。
    @Published private(set) var conversationEphemeral = false
    /// 无痕状态变化回调：由 ChatTabManager 接线，镜像到 ChatTab.isEphemeral 供顶栏读取。
    var onEphemeralChanged: ((Bool) -> Void)?
    /// 正在切换已有会话的无痕标记（PATCH 在途）：防双击竞态 + 禁点。
    @Published private(set) var isTogglingEphemeral = false

    /// 是否处于无痕（待生效或已生效）。
    var isEphemeral: Bool { ephemeralIntent || conversationEphemeral }

    /// 当前流的工具调用展示队列（tool_call 入队，tool_result 更新状态）
    @Published var toolQueue: [ToolCallDisplay] = []
    /// 点文档卡片时回调：(documentId)。由 ChatTabManager 接线到 DocumentSelectionCoordinator。
    var onOpenDocument: ((String) -> Void)?
    /// 点开 HTML 产物卡片（拉正文 + 右栏打开）
    var onOpenArtifact: ((String, String) -> Void)?
    /// 待展示工具调用：tool_call 暂存，tool_result 到后按 `tool_call_id` 取出构造完结 pill（不展示 running）
    private struct PendingToolCall {
        /// LLM 原生 FC 的调用 id（服务端 tool_result 回传同一 id；旧服务端可能没有）
        let id: String?
        let display: String
        let tool: String
        let function: String
        let args: [String: Any]
    }
    private var pendingToolCalls: [PendingToolCall] = []
    /// 本轮各文档的合并后引用（docID → 最新已知状态）：同轮 create/update 多次触达同一篇只出一张卡片，
    /// 且后写覆盖、缺省保留（见 `DocumentRef.merged`）。
    private var roundDocRefs: [String: DocumentRef] = [:]
    /// 本轮各文档已落成的本地消息 id（docID → local message id），用于同轮就地更新而非新增卡片。
    private var roundDocRefMessageID: [String: String] = [:]
    /// 响应检验事件状态（verify:checking|done|retry）
    @Published var verifyStatus: VerifyStatus?

    /// 会话模式："work"（默认）| "plan"（只读）。只能由用户切换（Tab 快捷键/胶囊条）。
    /// 服务端权威：由 loadConversation 带入；未知值归一到 off。
    @Published var mode: String = "work"
    /// 当前模式（由服务端 mode 字符串派生）
    var chatMode: ChatMode { ChatMode(rawValue: mode) ?? .off }

    /// 新建会话成功（空 tab 转正）时回调：(conversationId, title)
    var onConversationCreated: ((String, String?) -> Void)?

    /// 会话在服务端已不存在（如无痕会话被销毁）时回调：(conversationId)。
    /// 由 ChatTabManager / ContentView 接线，用于关闭 tab、清理列表、提示用户。
    var onConversationGone: ((String) -> Void)?

    // MARK: 会话接力

    /// 正在生成接力文档（把本会话整理成文档，完成后开新会话续接）
    @Published private(set) var handoffActive = false
    /// 接力文档生成完成时回调：(文档标题, 文档 id, baseURL, token)
    var onHandoffReady: ((String, String, String, String) -> Void)?

    private var capturedHandoffDoc: (title: String, id: String)?
    private var handoffBaseURL = ""
    private var handoffToken = ""

    private var lastHapticTime: Date = .distantPast

    @AppStorage("llm_temperature") static var cachedTemperature: Double = 1.0
    @AppStorage("llm_maxTokens") static var cachedMaxTokens: Int = 4096
    @AppStorage("llm_thinking") static var cachedThinking: Bool = true

    func applyLLMDefaults() {
        thinkingMode = Self.cachedThinking
        temperature = Self.cachedTemperature
        maxTokens = Self.cachedMaxTokens
    }

    func refreshAndCacheLLMConfig() async {
        guard let config = try? await apiClient.getLLMConfig() else {
            LogBuffer.shared.warning("ChatViewModel.refreshAndCacheLLMConfig: failed, using cached defaults")
            return
        }
        if let t = config.temperature { Self.cachedTemperature = t }
        if let m = config.maxTokens { Self.cachedMaxTokens = m }
        if let th = config.thinking { Self.cachedThinking = th }
        applyLLMDefaults()
    }
    @Published var lastMemoryIDs: [String]?
    @Published var lastNoteIDs: [String]?
    @Published var showMemoryPopup = false
    @Published var showNotePopup = false
    @Published var showChatSettings = false

    @Published var planNodes: [PlanNode] = []
    @Published var isPlanModalVisible = false
    @Published var isPlanMinimized = false

    /// 执行计划（todo）：goal + 步骤列表 + 生命周期状态。对话结束/加载会话时刷新。
    @Published var planGoal = ""
    @Published var planTodo: [PlanStep] = []
    @Published var planState: PlanState = .none
    /// 用户主动隐藏（×）：内存态，持久记录在 UserDefaults（per conversation）
    @Published var planDismissed = false
    /// 便签展开/收起：纯内存态，新计划（指纹变化）出现时默认展开；不持久化
    @Published var planExpanded = false
    // —— 完成/取消态横幅：已砍掉（2026-08-02 简化：完成/取消后列表直接消失，不做提示）。
    //     保留注释供未来恢复：显示 5s 后消失，不受 dismissed 影响；触发条件是"对话内状态转变"。
    // @Published var planFlashBanner: String?
    // private var planFlashTask: Task<Void, Never>?

    /// 从服务端拉取当前会话的执行计划，并按生命周期状态机更新 UI。
    /// - Parameters:
    ///   - previousFingerprint: 重置前上一个计划的指纹（loadConversation 传）；
    ///     nil 时用当前指纹（done 刷新路径）。
    ///   - previousExpanded: 重置前便签折叠态，用于"同计划重载"恢复；nil 时保持当前值。
    func loadPlanTodo(previousFingerprint: String? = nil, previousExpanded: Bool? = nil) async {
        guard let cid = conversationId, !cid.isEmpty else { return }
        let plan: ConversationPlan
        do {
            plan = try await apiClient.getConversationPlan(conversationId: cid)
        } catch {
            // 有意降级：计划便签拉不到就保持旧状态，不打断当前会话
            LogBuffer.shared.warning("loadPlanTodo: 拉取执行计划失败 \(error)")
            return
        }
        let oldFingerprint = previousFingerprint ?? planFingerprint()
        planGoal = plan.goal
        planTodo = plan.steps
        if planFingerprint() != oldFingerprint {
            planExpanded = true // 新计划（指纹变化）→ 便签默认展开
        } else if let previousExpanded {
            planExpanded = previousExpanded // 同计划重载 → 恢复之前的折叠态（不打扰收起）
        }
        evaluatePlanState(cid: cid)
    }

    /// × 按钮：隐藏当前计划（记录指纹到 UserDefaults，进度更新不打扰，新计划指纹变化重新显示）
    func dismissPlanTodo() {
        guard let cid = conversationId, !cid.isEmpty else { return }
        planDefaultsSet(dismissedFingerprint: planFingerprint(), for: cid)
        planDismissed = true
    }

    /// 切换便签展开/收起（点击便签触发）。
    func togglePlanExpand() {
        planExpanded.toggle()
    }

    /// 计划状态机：active（列表）/ completed|cancelled（一次性横幅）/ none（不显示）
    private func evaluatePlanState(cid: String) {
        guard !planTodo.isEmpty else {
            planState = .none
            planDismissed = false
            return
        }
        let hasActive = planTodo.contains { $0.status == "pending" || $0.status == "in_progress" }
        if hasActive {
            planState = .active
            // 隐藏保持：指纹相同（同一计划，进度更新不打扰）；指纹变化（新计划）→ 打破隐藏
            let fp = planFingerprint()
            let hiddenFP = planDefaultsDismissedFingerprint(for: cid)
            planDismissed = hiddenFP != nil && hiddenFP == fp
            if hiddenFP != nil && !planDismissed {
                planDefaultsSet(dismissedFingerprint: nil, for: cid) // 新计划：清掉旧隐藏记录
            }
            return
        }
        // 无进行中步骤：completed 或 cancelled —— 简化版：直接不显示（列表消失，无提示）。
        let allCancelled = planTodo.allSatisfy { $0.status == "cancelled" }
        planState = allCancelled ? .cancelled : .completed
        planDismissed = false
        // —— 完成/取消态横幅（已砍掉，保留注释供未来恢复）——
        // 原逻辑：对话内状态转变到 completed/cancelled 且 completedSeen 未记录 → 显示横幅 5s。
        // 需要恢复时启用：planDefaultsCompletedSeen + planDefaultsSet(completedSeen:) + showPlanFlash。
    }

    private func planFingerprint() -> String {
        planGoal + "|" + planTodo.map(\.content).joined(separator: "|")
    }

    // —— 完成/取消态横幅（已砍掉，保留注释供未来恢复）——
    // private func showPlanFlash(_ text: String) {
    //     planFlashBanner = text
    //     planFlashTask?.cancel()
    //     planFlashTask = Task { @MainActor [weak self] in
    //         try? await Task.sleep(for: .seconds(5))
    //         guard !Task.isCancelled else { return }
    //         self?.planFlashBanner = nil
    //     }
    // }

    // —— 计划持久化（UserDefaults，per conversation）——

    private func planDefaultsDismissedFingerprint(for cid: String) -> String? {
        (UserDefaults.standard.dictionary(forKey: "plan_dismissed") as? [String: String])?[cid]
    }

    private func planDefaultsSet(dismissedFingerprint fp: String?, for cid: String) {
        var dict = (UserDefaults.standard.dictionary(forKey: "plan_dismissed") as? [String: String]) ?? [:]
        if let fp {
            dict[cid] = fp
        } else {
            dict.removeValue(forKey: cid)
        }
        UserDefaults.standard.set(dict, forKey: "plan_dismissed")
    }

    // —— completedSeen 持久化（完成/取消态横幅已砍掉，保留注释供未来恢复）——
    // private func planDefaultsCompletedSeen(for cid: String) -> Bool {
    //     (UserDefaults.standard.dictionary(forKey: "plan_completed_seen") as? [String: Bool])?[cid] ?? false
    // }
    //
    // private func planDefaultsSet(completedSeen seen: Bool, for cid: String) {
    //     var dict = (UserDefaults.standard.dictionary(forKey: "plan_completed_seen") as? [String: Bool]) ?? [:]
    //     dict[cid] = seen
    //     UserDefaults.standard.set(dict, forKey: "plan_completed_seen")
    // }

    /// L3 工具执行确认队列：并发子任务的 client_tool_call 会同时到达，
    /// 单槽会被后到者覆盖（被顶掉的 continuation 永不 resume）。改数组排队，只显示队首。
    @Published var pendingToolConfirmations: [ToolConfirmation] = []
    /// 队首确认的超时任务（只给队首计时：排队条目在被看到前不计时，杜绝静默失效）。
    private var confirmationTimeoutTask: Task<Void, Never>?

    /// 子任务用户介入请求（ask_user）：数组支持多 task 并发排队。
    /// 独立于 toolQueue——不被文本流清理（ToolActivityToast 在文本流开始时退场）。
    @Published var pendingUserRequests: [UserRequest] = []

    /// 子任务活动面板（SubagentPanel）：按 conv_id 归集，独立于消息流。
    @Published var subagentSessions: [SubagentSession] = []

    /// 子任务面板整体展开态：折叠时只留一行状态条，展开时浮层覆盖消息区。
    @Published var subagentPanelExpanded = false

    /// 循环切换会话模式（关 → 计划 → 关）。仅用户可触发（胶囊条点击 / 输入框聚焦时 Tab）。
    /// 已有会话：PATCH 落库，失败回滚；空 tab（无会话）：只改本地，第一条消息经 init_mode 落库。
    func cycleMode() async {
        let next = chatMode.next
        let toast: String
        switch next {
        case .off: toast = "已退出计划模式"
        case .normal: toast = "已进入计划模式（仅只读工具）"
        }
        guard let cid = conversationId, !cid.isEmpty else {
            mode = next.rawValue // 空 tab：会话尚未创建，模式暂存本地，待首条消息 init_mode 落库
            ToastCenter.shared.show(toast)
            return
        }
        let previous = mode
        mode = next.rawValue
        do {
            try await apiClient.updateConversationMode(id: cid, mode: next.rawValue)
            LogBuffer.shared.info("ChatViewModel.cycleMode: conv=\(cid) -> \(next.rawValue)")
            ToastCenter.shared.show(toast)
        } catch {
            LogBuffer.shared.warning("ChatViewModel.cycleMode failed: \(error)")
            mode = previous // 回滚
            ToastCenter.shared.show("切换模式失败，请重试")
        }
    }

    // MARK: - 模型切换（deep/flash，会话级客户端持久化）

    /// deep 模式持久化键：按会话 ID 区分；空白 tab（未建会话）用共享键，
    /// 首条消息创建会话后经 SSE done 把当前选择写入新会话键（预存继承）。
    private var deepModeDefaultsKey: String {
        guard let cid = conversationId, !cid.isEmpty else { return "xhan_conv_deep_mode_pending" }
        return "xhan_conv_deep_mode_\(cid)"
    }

    /// 切换当前会话模型：deep（pro 模型 + thinking）↔ flash（快速模型）。
    /// 仅客户端本地状态；deep 强制联动 thinking=true（thinking 不再作为独立开关）。
    func toggleDeepMode() {
        deepMode.toggle()
        if deepMode {
            thinkingMode = true // deep 联动：pro 模型 + 思考
        }
        UserDefaults.standard.set(deepMode, forKey: deepModeDefaultsKey)
        ToastCenter.shared.show(deepMode ? "已切换：深度模型" : "已切换：快速模型")
    }

    // MARK: - 响应检验开关（verify，会话级客户端持久化，macOS only）

    private var verifyEnabledDefaultsKey: String {
        guard let cid = conversationId, !cid.isEmpty else { return "xhan_conv_verify_enabled_pending" }
        return "xhan_conv_verify_enabled_\(cid)"
    }

    /// 切换响应检验：开=强制开启（每轮多一次 LLM 检验调用），关=强制关闭。
    /// 仅客户端本地状态，随每次请求经 verify_enabled 传给服务端。
    func toggleVerify() {
        verifyEnabled.toggle()
        UserDefaults.standard.set(verifyEnabled, forKey: verifyEnabledDefaultsKey)
        ToastCenter.shared.show(verifyEnabled ? "已开启：回复校验（每轮多一次检查）" : "已关闭：回复校验")
    }

    private var briefAnswerDefaultsKey: String {
        guard let cid = conversationId, !cid.isEmpty else { return "xhan_conv_brief_answer_pending" }
        return "xhan_conv_brief_answer_\(cid)"
    }

    /// 切换精炼回答：开=本轮回答控制在 500 字内（便于存成批注）。
    /// 仅客户端本地状态，随每次请求经 brief_answer 传给服务端。
    func toggleBriefAnswer() {
        briefAnswer.toggle()
        UserDefaults.standard.set(briefAnswer, forKey: briefAnswerDefaultsKey)
        ToastCenter.shared.show(briefAnswer ? "已开启：精炼回答（≤500 字）" : "已关闭：精炼回答")
    }

    // MARK: - 无痕（ephemeral）

    /// 打开会话时由上层灌入服务端无痕标记（不走 apiClient——标记随列表数据一起来）。
    func seedEphemeral(_ value: Bool) {
        ephemeralIntent = false
        conversationEphemeral = value
        onEphemeralChanged?(isEphemeral)
    }

    /// 切换无痕：空白 tab 只改本地意图（不建会话）；已有会话经 PATCH 落库。
    func toggleEphemeral() {
        guard let cid = conversationId, !cid.isEmpty else {
            ephemeralIntent.toggle()
            onEphemeralChanged?(isEphemeral)
            ToastCenter.shared.show(ephemeralIntent ? "已开启无痕：发送后约 30 分钟自动销毁" : "已关闭无痕")
            return
        }
        guard !isTogglingEphemeral else { return } // 防双击：PATCH 在途时忽略
        isTogglingEphemeral = true
        Task {
            await setEphemeralOnServer(!conversationEphemeral, convID: cid)
            isTogglingEphemeral = false
        }
    }

    private func setEphemeralOnServer(_ value: Bool, convID: String) async {
        do {
            try await apiClient.setConversationEphemeral(id: convID, ephemeral: value)
            conversationEphemeral = value
            onEphemeralChanged?(isEphemeral)
            ToastCenter.shared.show(value ? "已开启无痕：最后一轮约 30 分钟后自动销毁，可撤销" : "已关闭无痕")
        } catch {
            // 默认会话禁止无痕：服务端 400 兜底
            LogBuffer.shared.warning("ChatViewModel.setEphemeralOnServer failed: \(error)")
            ToastCenter.shared.show(ToastCenter.friendlyErrorText(error, action: "设置无痕"))
        }
    }

    /// 首条消息建成无痕会话后：把「待生效」意图转成服务端标记。
    private func promoteEphemeralIntent() {
        guard ephemeralIntent else { return }
        ephemeralIntent = false
        conversationEphemeral = true
        onEphemeralChanged?(true)
    }

    /// 挂起等待用户确认工具执行；队首起 180s 超时视为拒绝。
    /// 只在确实需要确认时调用（调用方判定：L4 高危 / fs 写 / 敏感上下文升级），本方法内不再有「跳过」分支。
    /// hardened=true：L4（如 shell 危险命令），红色 UI。
    /// browserArgsSummary=true 时（浏览器工具）用 BrowserActivityCenter.confirmationBody 生成人话摘要。
    /// diffText/additions/deletions/filePath：fs 写工具传 diff 预览，确认条展示 diff 卡片。
    func awaitToolConfirmation(
        toolName: String,
        arguments: [String: Any],
        hardened: Bool = false,
        browserArgsSummary: Bool = false,
        diffText: String? = nil,
        additions: Int? = nil,
        deletions: Int? = nil,
        filePath: String? = nil,
        explanation: String? = nil
    ) async -> Bool {
        // 浏览器工具走人话摘要（ref→元素文本、密码脱敏、来源等），其余保留原始参数
        let argsSummary: String
        if toolName.hasPrefix("shell") {
            // shell 危险命令必须完整展示，不复用 prefix(80) 截断
            argsSummary = (arguments["command"] as? String) ?? "(无命令)"
        } else if toolName.hasPrefix("calendar__") {
            // 日历写是 L4：完整展示「哪条日程、什么时候、写进哪本日历」，不做 80 字截断
            let function = String(toolName.dropFirst("calendar__".count))
            var summary = CalendarCore.confirmationSummary(function: function, arguments: arguments)
            #if os(macOS)
            // 改/删只拿到 id：补上事件标题与时间，别让人对着一个 id 点「允许」
            if let eventID = arguments["event_id"] as? String,
               let line = CalendarStore.shared.summary(eventID: eventID, occurrence: arguments["occurrence"] as? Int) {
                summary += " ｜ " + line
            }
            #endif
            argsSummary = summary
        } else {
            #if os(macOS)
            if browserArgsSummary {
                argsSummary = BrowserActivityCenter.shared.confirmationBody(toolName: toolName, arguments: arguments)
            } else {
                argsSummary = arguments.isEmpty
                    ? "(无参数)"
                    : String(arguments.map { "\($0.key)=\($0.value)" }.joined(separator: ", ").prefix(80))
            }
            #else
            argsSummary = arguments.isEmpty
                ? "(无参数)"
                : String(arguments.map { "\($0.key)=\($0.value)" }.joined(separator: ", ").prefix(80))
            #endif
        }
        let displayName = LocalToolRegistry.shared.displayName(for: toolName)
        return await withCheckedContinuation { (cont: CheckedContinuation<Bool, Never>) in
            let isNewHead = pendingToolConfirmations.isEmpty
            pendingToolConfirmations.append(ToolConfirmation(
                id: UUID(),
                toolName: toolName,
                displayName: displayName,
                argsSummary: argsSummary,
                explain: explanation,
                diffText: diffText,
                additions: additions,
                deletions: deletions,
                filePath: filePath,
                hardened: hardened,
                continuation: cont
            ))
            // 只有新条目成为队首时才起计时；排在后面的不重臂，避免重置队首已跑的计时。
            if isNewHead { armHeadConfirmationTimer() }
        }
    }

    /// 只给队首确认起超时计时：排在你没看到它之前不会倒计时。
    /// 队首变化（新条目上位 / 队首被解决）时调用；旧任务先取消。
    private func armHeadConfirmationTimer() {
        confirmationTimeoutTask?.cancel()
        guard let head = pendingToolConfirmations.first else { confirmationTimeoutTask = nil; return }
        playConfirmationAlert()
        confirmationTimeoutTask = Task { @MainActor [weak self] in
            try? await Task.sleep(for: .seconds(ToolConfirmation.timeoutSeconds))
            guard !Task.isCancelled, let self else { return }
            self.resolveToolConfirmation(id: head.id, allow: false)
        }
    }

    /// 用户点击允许/拒绝（或超时），结束对应确认；随后自动轮到下一条。
    func resolveToolConfirmation(id: UUID, allow: Bool) {
        guard let idx = pendingToolConfirmations.firstIndex(where: { $0.id == id }) else { return }
        let conf = pendingToolConfirmations.remove(at: idx)
        conf.continuation.resume(returning: allow)
        // 只有移除队首才轮到下一条、才需重臂计时；移除队中其它项不动队首计时与提示音。
        if idx == 0 { armHeadConfirmationTimer() }
    }

    /// 键盘 Tab / Esc：作用于当前队首确认。
    func resolveTopToolConfirmation(allow: Bool) {
        guard let head = pendingToolConfirmations.first else { return }
        resolveToolConfirmation(id: head.id, allow: allow)
    }

    /// 子任务请求用户介入（ask_user）：弹 AskUserBar + 系统通知（仅队列首个发，避免轰炸）。
    /// 等待用户：可仅点"已处理"（返回空字符串），也可输入回答文本（返回文本），
    /// 或"跳过"（返回 nil）；超时（下发 timeout_secs×0.9，至少 10s）视为跳过。
    func awaitUserRequest(callID: String, message: String, timeoutSecs: Double?, owner: String?) async -> String? {
        let timeout = max(10, (timeoutSecs ?? 120) * 0.9)
        return await withCheckedContinuation { (cont: CheckedContinuation<String?, Never>) in
            let hasHandoff = owner.map { BrowserToolProvider.shared.handoffTabId(forOwner: $0) != nil } ?? false
            pendingUserRequests.append(UserRequest(id: callID, message: message, owner: owner, hasHandoffTab: hasHandoff, continuation: cont))
            if pendingUserRequests.count == 1 {
                playAskUserNotification(message)
            }
            // 超时自动跳过。并发安全性：只有该 callID 仍在队列时才 resolve（防止已手动响应后的双重 resume）
            Task { [weak self] in
                try? await Task.sleep(for: .seconds(timeout))
                guard let self, self.pendingUserRequests.contains(where: { $0.id == callID }) else { return }
                self.resolveUserRequest(callID: callID, reply: nil)
            }
        }
    }

    /// 用户响应 ask_user：reply=nil 跳过；""=已处理无文本；非空=回答文本
    func resolveUserRequest(callID: String, reply: String?) {
        guard let idx = pendingUserRequests.firstIndex(where: { $0.id == callID }) else { return }
        let req = pendingUserRequests.remove(at: idx)
        req.continuation.resume(returning: reply)
    }

    // MARK: - 子任务活动面板（subagent SSE 事件）

    /// 解析 subagent 事件（start/end/status/tool_call/tool_result）并更新活动面板。
    /// 展开中的会话收到新事件后防抖 1s 重拉 DB 消息（MsgSink 已落库，事件只驱动刷新）。
    private func handleSubagentEvent(_ data: String) {
        guard let d = data.data(using: .utf8),
              let obj = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any],
              let convID = obj["conv_id"] as? String else { return }
        let type = obj["type"] as? String ?? ""
        let content = obj["content"] as? String ?? ""
        let state = obj["state"] as? String ?? ""
        let description = obj["description"] as? String ?? "子任务"

        switch type {
        case "start":
            if let idx = subagentSessions.firstIndex(where: { $0.convID == convID }) {
                subagentSessions[idx].state = "started"
            } else {
                subagentSessions.append(SubagentSession(convID: convID, description: description, state: "started"))
            }
        case "end":
            if let idx = subagentSessions.firstIndex(where: { $0.convID == convID }) {
                subagentSessions[idx].state = state.isEmpty ? "completed" : state
                subagentSessions[idx].summary = content.isEmpty ? nil : content
            }
            scheduleSubagentReload(convID)
        case "status":
            if let idx = subagentSessions.firstIndex(where: { $0.convID == convID }) {
                if let round = parseSubagentRound(content) {
                    subagentSessions[idx].round = round
                } else if !content.isEmpty {
                    // 非轮次状态（如「正在等待客户端执行 fs.grep…」心跳）：显示在最新动作行，
                    // 否则长等待时面板完全不动，用户看不出停在哪一步
                    subagentSessions[idx].latestAction = content
                }
            }
            scheduleSubagentReload(convID)
        case "tool_call":
            if let idx = subagentSessions.firstIndex(where: { $0.convID == convID }),
               let tc = parseToolCall(content) {
                subagentSessions[idx].latestAction = tc.display
            }
        case "tool_result":
            scheduleSubagentReload(convID)
        default:
            break
        }
    }

    /// 展开中的会话防抖重拉消息（轮数有限，重复拉取幂等覆盖，不做取消）
    private func scheduleSubagentReload(_ convID: String) {
        guard subagentSessions.first(where: { $0.convID == convID })?.expanded == true else { return }
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(1))
            await reloadSubagentMessages(convID)
        }
    }

    /// 拉取子会话完整消息（DB 为准，活动面板展开态渲染）
    func reloadSubagentMessages(_ convID: String) async {
        let msgs: [Message]
        do {
            msgs = try await apiClient.getMessages(conversationId: convID)
        } catch {
            // 有意降级：子任务面板保留上一次的消息，不打断主对话
            LogBuffer.shared.warning("reloadSubagentMessages(\(convID)): \(error)")
            return
        }
        guard let idx = subagentSessions.firstIndex(where: { $0.convID == convID }) else { return }
        subagentSessions[idx].messages = msgs
    }

    /// 展开/收起子任务卡片；展开时立即拉一次消息
    func toggleSubagentExpand(_ convID: String) {
        guard let idx = subagentSessions.firstIndex(where: { $0.convID == convID }) else { return }
        subagentSessions[idx].expanded.toggle()
        if subagentSessions[idx].expanded {
            Task { await reloadSubagentMessages(convID) }
        }
    }

    /// 切换子任务面板整体展开态（折叠=一行状态条，展开=覆盖消息区的浮层）
    func toggleSubagentPanel() {
        subagentPanelExpanded.toggle()
    }

    /// 从 MiniReAct 状态文本解析轮次（"第 N/M 轮"）
    private func parseSubagentRound(_ content: String) -> Int? {
        let pattern = #"第 (\d+)/"#
        guard let regex = try? NSRegularExpression(pattern: pattern),
              let m = regex.firstMatch(in: content, range: NSRange(content.startIndex..., in: content)),
              let r = Range(m.range(at: 1), in: content) else { return nil }
        return Int(content[r])
    }

    /// ask_user 到达时发 macOS 系统通知（仅队列首个），让用户知道子任务需要他
    private func playAskUserNotification(_ message: String) {
        NotificationManager.notifyUserIntervention(message: message)
    }

    /// L3 确认出现时播放提示音（macOS：Glass 系统音，设置可关；iOS 静默）。
    private func playConfirmationAlert() {
        #if os(macOS)
        let key = LocalPaths.scoped("xhan_l3_alert_sound")
        if UserDefaults.standard.object(forKey: key) == nil
            || UserDefaults.standard.bool(forKey: key) {
            if let sound = NSSound(named: .init("Glass")) {
                sound.play()
            } else {
                NSSound.beep()
            }
        }
        #endif
    }

    /// 久等后的回复完成提示音（macOS：Bottle 系统音；默认开，设置可关；iOS 静默）。
    private func playLongReplyAlert() {
        #if os(macOS)
        let key = LongReplyAlert.soundKey
        if UserDefaults.standard.object(forKey: key) == nil
            || UserDefaults.standard.bool(forKey: key) {
            if let sound = NSSound(named: .init("Bottle")) {
                sound.play()
            } else {
                NSSound.beep()
            }
        }
        #endif
    }

    /// 「久等」阈值（秒）。设置里存秒值便于测试；无记录时 dev 默认 5s、prod 默认 180s。
    private static func longReplyAlertThreshold() -> TimeInterval {
        let seconds = UserDefaults.standard.object(forKey: LongReplyAlert.secondsKey) as? Int
            ?? LongReplyAlert.defaultSeconds
        return TimeInterval(seconds)
    }

    // 游标分页
    @Published var oldestCursor = ""
    @Published var newestCursor = ""
    @Published var hasMoreBefore = false
    @Published var hasMoreAfter = false
    @Published var isLoadingMore = false
    @Published var isInitialLoading = true
    @Published var loadMoreError: String?
    @Published var scrollToBottomRequested = false

    let apiClient: APIClient // 扩展（ChatViewModel+ClientTools）需要访问
    private let sseClient = SSEClient()
    private var streamingContent = ""
    private var throttleTimer: Timer?
    /// 本轮用户消息发出的时刻（用于「久等后回复完成」提示音；done/停止时清空）
    private var streamStartedAt: Date?
    #if os(iOS)
    private var backgroundTaskID: UIBackgroundTaskIdentifier = .invalid
    #endif

    init(apiClient: APIClient) {
        self.apiClient = apiClient
        applyLLMDefaults()
    }

    // MARK: Load

    /// 加载会话。mode 从会话数据带入（服务端权威）；思考态/子任务面板等临时 UI 状态仅"切换会话"时重置，
    /// 同一会话刷新（scene 激活/重试/手动刷新）传 preserveTransientState=true 保持。
    func loadConversation(id: String, title: String, mode: String? = nil, preserveTransientState: Bool = false) async {
        sseClient.disconnect()
        isStreaming = false
        conversationId = id
        pendingConversationID = "" // 切会话后不能再用上一轮 meta 给的 id
        conversationTitle = title
        self.mode = mode ?? "work" // 会话列表数据带 mode（服务端权威）；旧数据缺失时默认 work
        // 2026-10：模型切换按钮已从输入栏下架 → 恒为快速模型，不再从会话键恢复
        // （残留键值不再读，避免旧会话永久卡在深度且无入口可关）
        deepMode = false
        // 响应检验开关会话级恢复：无记录默认关
        verifyEnabled = UserDefaults.standard.bool(forKey: "xhan_conv_verify_enabled_\(id)")
        // 2026-10：精炼回答按钮已从输入栏下架 → 恒为常规长度，不再从会话键恢复
        briefAnswer = false
        toolQueue = []
        pendingToolCalls = []
        roundDocRefs = [:]
        roundDocRefMessageID = [:]
        verifyStatus = nil
        if !preserveTransientState {
            // 思考内容会话级：仅切会话时清空（同会话刷新/scene 激活保留折叠态）
            thoughtMessage = nil
            thoughtDone = false
            // 子任务活动面板同款：仅切会话时清空
            subagentSessions = []
            subagentPanelExpanded = false
        }
        // 记录重置前计划指纹与折叠态：同会话重载（scene 激活/刷新/重试）时保持收起状态，
        // 真·新计划（指纹变化/切换会话）仍默认展开。捕获必须在清空之前。
        let previousPlanFingerprint = planFingerprint()
        let previousPlanExpanded = planExpanded
        planGoal = ""
        planTodo = []
        planState = .none
        planDismissed = false
        // planFlashBanner = nil（横幅已砍掉）
        isInitialLoading = true
        errorMessage = nil
        loadMoreError = nil
        let oldLastId = messages.last?.id
        defer { isInitialLoading = false }
        Task { await self.loadPlanTodo(previousFingerprint: previousPlanFingerprint, previousExpanded: previousPlanExpanded) }
        do {
            let result = try await apiClient.getMessagesCursor(conversationId: id)
            messages = result.messages.flatMap { $0.withoutToolCalls() }
            if oldLastId != nil, oldLastId != messages.last?.id {
                scrollToBottomRequested = true
            }
            if let first = messages.first {
                await mergeLocalToolResults(from: first.createdAt)
            }
            oldestCursor = result.oldestCursor
            newestCursor = result.newestCursor
            hasMoreBefore = result.hasMoreBefore
            hasMoreAfter = result.hasMoreAfter
            // 无痕标记随消息响应一起来：所有加载路径（openConversation / iOS 直连 / 历史回退）
            // 统一在此灌入，避免只在 openConversation 注入导致 pill 与服务端不一致。
            seedEphemeral(result.ephemeral ?? false)
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadConversation: \(error)")
            // 会话不存在（如无痕会话已销毁）→ 通知上层关闭 tab，不再显示"加载失败"
            if (error as NSError).code == 404 {
                onConversationGone?(id)
                return
            }
            errorMessage = "加载消息失败"
        }
        await refreshUsage()
    }

    /// 拉取会话消耗金额（慢更新）。上下文 token 由 done 事件即时提供，这里主要补金额。
    func refreshUsage() async {
        guard let cid = conversationId, !cid.isEmpty else { return }
        do {
            let u = try await apiClient.getConversationUsage(conversationId: cid)
            usage = u
            // 加载时用接口值初始化；done 后接口值与 SSE 一致，覆盖无害
            if u.contextTokens > 0 { contextTokens = u.contextTokens }
            if u.contextWindow > 0 { contextWindow = u.contextWindow }
        } catch {
            LogBuffer.shared.warning("ChatViewModel.refreshUsage: \(error)")
        }
    }

    func loadMore(direction: String = "before") async {
        guard !isLoadingMore else { return }
        guard let cid = conversationId, !cid.isEmpty else { return }
        isLoadingMore = true
        loadMoreError = nil
        let cursor = direction == "before" ? oldestCursor : newestCursor
        do {
            let result = try await apiClient.getMessagesCursor(
                conversationId: cid,
                cursor: cursor,
                direction: direction
            )
            if direction == "before" {
                let newMsgs = result.messages.flatMap { $0.withoutToolCalls() }
                messages = newMsgs + messages
                oldestCursor = result.oldestCursor
                let stillHasMore = result.hasMoreBefore
                DispatchQueue.main.async {
                    self.hasMoreBefore = stillHasMore
                }
                if let first = newMsgs.first {
                    Task { await mergeLocalToolResults(from: first.createdAt) }
                }
            } else {
                messages = messages + result.messages.flatMap { $0.withoutToolCalls() }
                newestCursor = result.newestCursor
                hasMoreAfter = result.hasMoreAfter
            }
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadMore: \(error)")
            loadMoreError = "加载失败，点击重试"
        }
        isLoadingMore = false
    }

    func loadMessages(_ msgs: [Message]) {
        messages = msgs
    }

    // MARK: Reset

    /// 重置为新聊天状态（不发消息不在后端创建会话，conversationId 置空字符串
    /// 使 SSE 携带 conversation_id="" 由后端创建非默认新会话）
    func resetForNewChat() {
        stopStreaming()
        handoffActive = false
        capturedHandoffDoc = nil
        messages = []
        isStreaming = false
        thoughtMessage = nil
        thoughtDone = false
        subagentSessions = []
        subagentPanelExpanded = false
        statusMessage = nil
        errorMessage = nil
        conversationId = ""
        // 必须一起清：sendMessage 会在 conversationId 为空时回退到 pendingConversationID，
        // 留着它会让「新对话」的第一条消息发进上一个会话
        pendingConversationID = ""
        conversationTitle = nil
        ephemeralIntent = false
        conversationEphemeral = false
        onEphemeralChanged?(false)
        isEditing = false
        contextTokens = 0
        contextWindow = 0
        usage = nil
        mode = "work"   // 空对话不继承上一篇的模式（删掉 plan 会话后不能停在只读）
        planNodes = []
        isPlanModalVisible = false
        isPlanMinimized = false
        planGoal = ""
        planTodo = []
        planState = .none
        planDismissed = false
        planExpanded = false
        lastMemoryIDs = nil
        lastNoteIDs = nil
        showMemoryPopup = false
        showNotePopup = false
        oldestCursor = ""
        newestCursor = ""
        hasMoreBefore = false
        hasMoreAfter = false
        isLoadingMore = false
        isInitialLoading = false
        loadMoreError = nil
        draftText = ""
        draftAttachments = []
        pendingVoiceReply = false // 否则新会话里手打的第一条会被当成语音轮（短答 + 自动朗读）
        streamingContent = ""
    }

    // MARK: Send

    func sendMessage(_ text: String, baseURL: String, token: String, agentId: String? = nil, hiddenRound: Bool = false, attachmentIds: [String] = [], localImages: [PlatformImage] = [], speakReply: Bool = false) {
        speakReplyForCurrentTurn = speakReply
        // 新会话第一轮还没 done 时插话：meta 帧已经给了 id，这一轮必须落回同一会话，
        // 否则会另起一个会话、第一轮还在后台继续跑（连续对话最常见的路径）。
        let turnConversationID = sendConversationID
        let userMsg = Message.localUser(text: text, conversationId: turnConversationID ?? "", imageIds: attachmentIds, localImages: localImages)
        messages.append(userMsg)

        isStreaming = true
        streamingContent = ""
        streamStartedAt = Date()
        lastEventSeq = 0
        currentTurnID = ""
        pendingConversationID = ""
        handledClientToolCallIDs.removeAll()
        isResumeInFlight = false // 上一轮续传若以「200 直接关闭」收尾，标志不许漏到新一轮
        thoughtMessage = nil
        thoughtDone = false
        subagentSessions = []
        subagentPanelExpanded = false
        statusMessage = nil
        errorMessage = nil
        planNodes = []
        isPlanModalVisible = false
        isPlanMinimized = false
        lastMemoryIDs = nil
        lastNoteIDs = nil
        toolQueue = []
        pendingToolCalls = []
        roundDocRefs = [:]
        roundDocRefMessageID = [:]
        verifyStatus = nil

        messages.append(.streamingAssistant(conversationId: turnConversationID ?? ""))

        Task { @MainActor in
            let clientTools = await Self.collectClientTools()
            setupSSE(baseURL: baseURL, token: token, text: text, agentId: agentId, clientTools: clientTools, hiddenRound: hiddenRound, attachmentIds: attachmentIds, conversationOverride: turnConversationID)
        }
        #if os(iOS)
        beginBackgroundTask()
        #endif
    }

    // MARK: 会话接力

    /// 触发接力：把本会话整理成一篇带「接力」tag 的文档，完成后由 onHandoffReady 开新会话续接。
    func beginHandoff(baseURL: String, token: String, agentId: String? = nil) {
        guard !isStreaming, !chatMode.isPlanLike, let cid = conversationId, !cid.isEmpty else { return }
        handoffActive = true
        capturedHandoffDoc = nil
        handoffBaseURL = baseURL
        handoffToken = token
        sendMessage(Self.handoffPrompt, baseURL: baseURL, token: token, agentId: agentId, hiddenRound: true)
    }

    /// 接力文档生成提示词（借 opencode compaction 的 anchored summary 模板 + 规则）
    static let handoffPrompt = """
    把本次会话到目前为止的讨论整理成一篇"接力文档"，供另一个会话接手继续。

    要求：
    - 必须调用 document.create_document 创建，标题自拟、能概括主题。
    - tags 参数必须固定包含「接力」；如有其他合适标签，用逗号追加在其后。
    - 正文严格使用下列 Markdown 结构，章节与顺序不变，每节都保留（为空填「(无)」）。

    ## 目标
    - 用户想达成什么（一两句）

    ## 重要细节
    - 约束与偏好、已确定的决策及原因、重要事实与假设、继续所需的确切上下文

    ## 工作状态
    ### 已完成
    - 已得出的结论、已完成的工作、已产出的文档/笔记

    ### 进行中
    - 当前正在讨论或处理的内容、未完成的探索

    ### 阻塞
    - 卡点、未决问题、未知项

    ## 下一步
    1. 紧接着要做的具体动作
    2. 已知的后续动作

    ## 相关文档 / 笔记
    - 文档或笔记的标题、id：为什么相关

    规则：
    - 用简短要点，不写散文长句。
    - 保留精确的文件路径、符号名、命令、错误串、URL、ID。
    - 不要提及"整理/压缩/摘要"这个动作本身。
    - 只做整理，不要继续讨论；创建完成后只回复我文档标题。
    """

    /// 新会话首条消息：指向接力文档，请新会话先读取再续接
    static func handoffTrigger(title: String, id: String) -> String {
        """
        【接力会话】上一个会话已整理为接力文档《\(title)》(id=\(id))。
        请先调用 document.get_document 读取它，然后基于它继续；有下一步就直接继续，不确定就先问我。
        """
    }

    private func setupSSE(baseURL: String, token: String, text: String, agentId: String? = nil, clientTools: [[String: Any]]? = nil, hiddenRound: Bool = false, attachmentIds: [String] = [], conversationOverride: String? = nil) {
        sseClient.disconnect()
        sseClient.onEvent = { [weak self] event in
            Task { @MainActor in
                self?.handleEvent(event)
            }
        }
        sseClient.onError = { [weak self] error in
            Task { @MainActor in
                let nsError = error as NSError
                // 续传窗口里的 404/501 = 后端没有 /api/chat/stream（老后端），
                // 按「不支持续传」收尾并拉历史；别当成会话已销毁去关 tab。
                if nsError.domain == "XHANHTTP", nsError.code == 404 || nsError.code == 501,
                   self?.isResumeInFlight == true {
                    LogBuffer.shared.warning("ChatViewModel.sse: resume unsupported (HTTP \(nsError.code)), fallback to reload")
                    self?.handleStreamEnded(reload: false)
                    return
                }
                // 会话不存在（如无痕会话已销毁）：SSEClient 以 HTTP 状态码上报
                if nsError.domain == "XHANHTTP", nsError.code == 404 {
                    LogBuffer.shared.warning("ChatViewModel.sse: conversation gone (404)")
                    self?.isResumeInFlight = false
                    self?.finishStreaming()
                    self?.detachStreamingMessage()
                    if let cid = self?.conversationId, !cid.isEmpty {
                        self?.onConversationGone?(cid)
                    }
                    return
                }
                self?.isResumeInFlight = false
                if nsError.domain == NSURLErrorDomain,
                   nsError.code == NSURLErrorNetworkConnectionLost || nsError.code == NSURLErrorCancelled {
                    self?.finishStreaming()
                    self?.detachStreamingMessage()
                    return
                }
                LogBuffer.shared.error("ChatViewModel.sse onError: \(error)")
                ToastCenter.shared.show(ToastCenter.friendlyErrorText(nsError, action: "发送"))
                self?.finishStreaming()
                self?.detachStreamingMessage()
            }
        }
        // 空 tab（无会话）时带 init_mode：会话创建时应用客户端已切换的模式（work/plan）。
        // 已有 pending id 说明会话正在创建/已创建，不能再下发 init_mode。
        let targetConversationID = conversationOverride ?? sendConversationID
        // 空串也是「空 tab」：会话要新建，init_mode 必须带上
        let initMode: String? = (targetConversationID ?? "").isEmpty ? mode : nil
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: targetConversationID, deepMode: deepMode, agentId: agentId, thinking: thinkingMode, temperature: temperature, maxTokens: maxTokens, clientTools: clientTools, initMode: initMode, verifyEnabled: verifyEnabled, briefAnswer: briefAnswer, voiceMode: speakReplyForCurrentTurn, hiddenRound: hiddenRound, attachments: attachmentIds, ephemeral: ephemeralIntent)
    }

    /// `notifyServer=false` 用于「同一会话立刻发下一句」：服务端 Register 本来就会取消旧 loop，
    /// 再发 stop 有可能赶在新一轮注册之后到达，把新轮停掉。
    func stopStreaming(notifyServer: Bool = true) {
        // 先立即断开 SSE + UI 收尾（思考框消失），再后台通知服务端显式终止 ReAct。
        // 顺序保证：UI 即时响应，stopChat 失败/慢速不影响停止体验。
        handoffActive = false
        capturedHandoffDoc = nil
        let api = apiClient
        let cid = effectiveConversationID
        sseClient.disconnect()
        #if os(macOS)
        // 杀掉本地 shell 进程组（stopStreaming 原本只断 SSE + 通知服务端，杀不掉本地命令）
        ShellProcessRegistry.shared.killAll()
        #endif
        finishStreaming()
        detachStreamingMessage()
        Task {
            guard notifyServer, let cid, !cid.isEmpty else { return }
            do {
                let stopped = try await api.stopChat(conversationId: cid)
                LogBuffer.shared.info("stopChat: stopped=\(stopped)")
            } catch {
                LogBuffer.shared.error("stopChat failed: \(error)")
            }
        }
    }

    // MARK: 续传（挂起 → 回前台）

    /// 续传请求在途：这个窗口里的 404/501 说明「后端没有续传能力」，
    /// 不能当成会话已销毁（旧后端 + 新客户端时会把用户 tab 关掉）。
    private var isResumeInFlight = false

    /// 续传用的会话 id：空白 tab 还没拿到 done 时，先看 meta 帧给的 pending id。
    private var effectiveConversationID: String? {
        if let cid = conversationId, !cid.isEmpty { return cid }
        return pendingConversationID.isEmpty ? nil : pendingConversationID
    }

    /// 发新轮用的会话 id：空白 tab 必须发空串，后端据此创建**非默认**会话；
    /// 发 nil 会被当成老客户端、落进「默认对话」（见 server/controller/chat.go 的
    /// `req.ConversationID == nil` 分支）。续传仍用 effectiveConversationID（要真实 id）。
    private var sendConversationID: String? {
        if let cid = conversationId { return cid }
        return pendingConversationID.isEmpty ? "" : pendingConversationID
    }

    /// 回前台续传：带 turn + from 重连，补发断点后的帧并继续跟流。
    /// 返回 false 表示续不了（没有会话 id / 没收到 meta），调用方改走拉历史。
    @discardableResult
    func resumeStreaming() -> Bool {
        guard isStreaming else { return false }
        guard let cid = effectiveConversationID, !currentTurnID.isEmpty else {
            LogBuffer.shared.warning("resumeStreaming: missing conversation/turn id, fallback to reload")
            handleStreamEnded(reload: false)
            return false
        }
        LogBuffer.shared.info("resumeStreaming: conv=\(cid) turn=\(currentTurnID) from=\(lastEventSeq)")
        isResumeInFlight = true
        sseClient.resume(baseURL: apiClient.baseURL, token: apiClient.token,
                         conversationId: cid, turn: currentTurnID, from: lastEventSeq)
        return true
    }

    /// 续传拿不到可接的轮次（idle / reset）时的统一收尾：结束流式 → 以服务端为准重载。
    private func handleStreamEnded(reload: Bool) {
        isResumeInFlight = false
        let cid = effectiveConversationID
        finishStreaming()
        detachStreamingMessage()
        if reload {
            ToastCenter.shared.show("连接中断，已重新加载")
        }
        guard let cid else { return }
        // 空白 tab：先用 meta 给的 id 转正，否则下次发送会又新建一个会话
        if (conversationId ?? "").isEmpty {
            conversationId = cid
            onConversationCreated?(cid, "新对话")
        }
        Task { await self.loadConversation(id: cid, title: conversationTitle ?? "新对话", mode: mode) }
    }

    // MARK: SSE Event Handling

    private func handleEvent(_ event: StreamEvent) {
        if let idStr = event.id, let seq = Int64(idStr) {
            lastEventSeq = seq
        }
        switch event.type {
        case .meta:
            guard let d = event.data.data(using: .utf8),
                  let obj = (try? JSONSerialization.jsonObject(with: d)) as? [String: Any] else {
                LogBuffer.shared.warning("meta: bad payload: \(event.data.prefix(200))")
                break
            }
            if let turnID = obj["turn_id"] as? String, !turnID.isEmpty {
                if !currentTurnID.isEmpty, currentTurnID != turnID {
                    // 手里是另一轮（断开期间另一台设备发了新消息，把这一轮取消了）
                    handleStreamEnded(reload: true)
                    break
                }
                currentTurnID = turnID
            }
            if let cid = obj["conversation_id"] as? String, !cid.isEmpty,
               (conversationId ?? "").isEmpty {
                pendingConversationID = cid
            }

        case .idle, .reset:
            // 续传没有可接的轮次 / 连续性断了：按历史重载，别留在灰态
            LogBuffer.shared.info("chat stream ended without resume (\(event.type == .reset ? "reset" : "idle"))")
            handleStreamEnded(reload: event.type == .reset)

        case .planNodeStart:
            if let node = parsePlanNode(event.data) {
                if !isPlanModalVisible { isPlanModalVisible = true }
                if let idx = planNodes.firstIndex(where: { $0.id == node.id }) {
                    planNodes[idx] = node
                } else {
                    planNodes.append(node)
                }
            }

        case .planNodeDone:
            if let node = parsePlanNode(event.data) {
                if let idx = planNodes.firstIndex(where: { $0.id == node.id }) {
                    planNodes[idx] = node
                }
            }

        case .thought, .thoughtDelta:
            if thoughtMessage == nil { thoughtMessage = "" }
            thoughtMessage! += event.data

        case .thoughtDone:
            thoughtDone = true

        case .message:
            // 文本开始推送：清「正在分析/等待」提示 + 工具/校验 pill（内容才是主角，对齐前端行为）
            statusMessage = nil
            toolQueue = []
            pendingToolCalls = []
            verifyStatus = nil
            if let delta = parseMessageDelta(event.data) {
                streamingContent += delta
                throttleFlush()
                let now = Date()
                if now.timeIntervalSince(lastHapticTime) >= 0.15 {
                    #if os(iOS)
                    UIImpactFeedbackGenerator(style: .light).impactOccurred()
                    #endif
                    lastHapticTime = now
                }
            }

        case .status:
            statusMessage = parseStatus(event.data)

        case .toolCall:
            if let tc = parseToolCall(event.data) {
                // 新一轮工具执行（上一轮 pending 已清空）：清掉旧完结 pill
                if pendingToolCalls.isEmpty { toolQueue = [] }
                // 客户端工具（浏览器）服务端 display 是 "browser.agent_browser_click"，
                // 换成本地中文短名，与其它工具 pill 一致
                let display = tc.tool == "browser"
                    ? LocalToolRegistry.shared.displayName(for: "\(tc.tool)__\(tc.function)")
                    : tc.display
                pendingToolCalls.append(PendingToolCall(
                    id: tc.id, display: display, tool: tc.tool, function: tc.function, args: tc.args))
            }

        case .toolResult:
            if let tr = parseToolResult(event.data), !pendingToolCalls.isEmpty {
                // 只在成功/失败时展示 pill（不展示 running）
                // 按 tool_call_id 精确配对：服务端 tool_call 的到达顺序 ≠ 结果顺序（客户端工具后发），
                // FIFO 只在旧服务端没有 id 时兜底。无匹配 = 对应 tool_call 被丢，不能吞掉别人的条目。
                guard let idx = ToolCallPairing.index(
                    ofResultID: tr.toolCallId,
                    in: pendingToolCalls.map(\.id)
                ) else {
                    LogBuffer.shared.warning("tool_result 无匹配 tool_call：id=\(tr.toolCallId ?? "nil")")
                    break
                }
                let pending = pendingToolCalls.remove(at: idx)
                let display = pending.display
                // 会话接力：捕获本轮生成的接力文档（document.create_document 结果里的 id/title）
                if handoffActive, pending.tool == "document", pending.function == "create_document",
                   tr.success, let id = tr.data?["id"] as? String {
                    capturedHandoffDoc = (title: tr.data?["title"] as? String ?? "接力文档", id: id)
                }
                // 聊天里的文档卡片：agent 建/改过的文档派生成本地产物，独立成一条
                // local_tool_result 消息并落盘（切屏/重载后按时间重放）。只认 create/update，
                // 同轮多次触达同一篇就地合并更新（后写覆盖、缺省保留）。
                // HTML 产物：卡片立刻挂到当前这条回复上（服务端字段要等重载才有）。
                // create（新建/整页覆盖）与 patch（局部改）都要挂：patch 后同 id 的卡
                // 按「只留最新」收敛到本轮，右栏 tab 也要静默重拉。
                if pending.tool == "html", pending.function == "create" || pending.function == "patch", tr.success,
                   let aid = tr.data?["artifact_id"] as? String {
                    let title = tr.data?["title"] as? String ?? ""
                    appendArtifactRef(ArtifactRef(
                        id: aid,
                        type: tr.data?["type"] as? String ?? "html",
                        title: title,
                        summary: tr.data?["summary"] as? String ?? ""
                    ))
                    // 原地覆盖 / 局部修改都不产生新 id：若这页正开在右栏，静默重拉一次，
                    // 否则用户看到的是旧版、以为没改成。新建时 tab 不存在，天然 no-op。
                    ArtifactOpenCoordinator.shared.reloadIfOpen(id: aid, title: title)
                }
                if pending.tool == "document", tr.success,
                   let patch = DocumentArtifactProducer.patch(function: pending.function,
                                                              args: pending.args,
                                                              data: tr.data) {
                    upsertRoundDocumentRef(patch)
                }
                let summary = tr.success ? "✅ \(display) 已完成" : "❌ \(display): \(tr.error ?? "失败")"
                let tc = ToolCallDisplay(display: display, status: tr.success ? .done : .failed, summary: summary)
                toolQueue.append(tc)
                // 自动退场：成功 1.5s / 失败 3s；若期间被 message/verify/done/新 tool_call
                // 清掉队列（队首 id 不匹配）则 no-op，不重复删除
                let id = tc.id
                let delay: UInt64 = tr.success ? 1_500_000_000 : 3_000_000_000
                Task { @MainActor in
                    try? await Task.sleep(nanoseconds: delay)
                    // 按 id 移除（不限于队首）：多 pill 时各自 timer 独立退场
                    if let idx = toolQueue.firstIndex(where: { $0.id == id }) {
                        toolQueue.remove(at: idx)
                    }
                }
            }

        case .verify:
            if let vs = parseVerify(event.data) {
                // verify 只在全部工具结束后发生：清空工具 pill，让位校验 pill
                toolQueue = []
                pendingToolCalls = []
                verifyStatus = VerifyStatus(status: vs.status, retry: vs.retry, reason: vs.reason ?? "")
            }

        case .remindersChanged:
            LogBuffer.shared.debug("[Reminder] SSE reminders_changed received")
            NotificationCenter.default.post(name: .remindersSyncTriggered, object: nil)

        case .clientToolCall:
            handleClientToolCall(event.data)

        case .subagent:
            handleSubagentEvent(event.data)

        case .done:
            isResumeInFlight = false
            let doneError = parseDoneError(event.data)
            // 久等后回复完成提示音：判定必须在 finishStreaming 清空 streamStartedAt 之前
            let replyElapsed = streamStartedAt.map { Date().timeIntervalSince($0) } ?? 0
            let hasReplyContent = !streamingContent.isEmpty
            let replyText = streamingContent
            let shouldSpeak = speakReplyForCurrentTurn && doneError == nil && hasReplyContent
            LogBuffer.shared.diag("[voice] done: speakReply=\(speakReplyForCurrentTurn) " +
                                  "hasContent=\(hasReplyContent) error=\(doneError ?? "-") shouldSpeak=\(shouldSpeak)")
            speakReplyForCurrentTurn = false
            finishStreaming()
            if shouldSpeak {
                onAssistantReplyDone?(replyText)
            }
            // 仅正常完成才响：done 带 error 说明这轮有异常，不报喜；无内容（空回复/被截断）也不响
            if hasReplyContent, doneError == nil, replyElapsed >= Self.longReplyAlertThreshold() {
                playLongReplyAlert()
            }
            statusMessage = nil
            toolQueue = []
            pendingToolCalls = []
            verifyStatus = nil
            if let err = doneError {
                ToastCenter.shared.show("服务提示：\(err)")
            }
            var blankCID: String?   // 空白 tab 首轮的真实会话 id：等 user id 也换好再补落盘
            if let cid = parseConversationIdFromDone(event.data) {
                let wasBlank = (conversationId ?? "").isEmpty
                conversationId = cid
                if wasBlank { blankCID = cid }
                onConversationCreated?(cid, parseTitleFromDone(event.data))
                // 空白 tab 若开了无痕意图：会话已按 ephemeral=true 建成 → 转成服务端标记
                promoteEphemeralIntent()
                // 空白 tab 首条消息建会话后：把当前开关预存到新会话键（后续重载继承）
                UserDefaults.standard.set(verifyEnabled, forKey: "xhan_conv_verify_enabled_\(cid)")
            }
            if let t = parseTitleFromDone(event.data) {
                conversationTitle = t
            }
            if let msgID = parseMessageIdFromDone(event.data) {
                replaceStreamingMessageID(with: msgID)
            }
            if let userMsgID = parseUserMessageIdFromDone(event.data) {
                let oldUserID = currentRoundUserID
                replaceUserMessageID(with: userMsgID)
                // 卡片锚点跟着换成服务端 id，重载后按 id 归位（不靠两个时钟比大小）
                reattachLocalArtifactAnchors(from: oldUserID, to: userMsgID)
            }
            // 空白 tab 首轮：卡片此前只留内存（那时还没有会话 id），现在 id 与 user id 都齐了再补落盘，
            // 这样落下的锚点直接就是服务端 user id。
            if let blankCID { persistLocalArtifactsUnderConversation(blankCID) }
            // 上下文用量即时更新（来自 done 事件，零请求）
            if let ctx = parseContextFromDone(event.data) {
                if ctx.tokens > 0 { contextTokens = ctx.tokens }
                if ctx.window > 0 { contextWindow = ctx.window }
            }
            // 金额慢更新：成本异步落库有延迟，稍后再拉一次
            Task {
                try? await Task.sleep(for: .seconds(1.5))
                await self.refreshUsage()
            }
            // 对话结束刷新执行计划（todo 可能被 plan.save 更新）
            Task { await self.loadPlanTodo() }
            NotificationCenter.default.post(name: .conversationListNeedsRefresh, object: nil)
            if let mids = parseMemoryIDsFromDone(event.data), !mids.isEmpty {
                lastMemoryIDs = mids
            }
            if let nids = parseNoteIDsFromDone(event.data), !nids.isEmpty {
                lastNoteIDs = nids
            }
            // 任务完成，立即渐隐弹窗
            withAnimation(.easeOut(duration: 0.3)) {
                isPlanModalVisible = false
            }
            DispatchQueue.main.asyncAfter(deadline: .now() + 0.3) { [self] in
                planNodes = []
            }
            // 会话接力：文档生成结束，通知开新会话续接；未捕获到文档则取消并提示
            if handoffActive {
                handoffActive = false
                if let doc = capturedHandoffDoc {
                    capturedHandoffDoc = nil
                    onHandoffReady?(doc.title, doc.id, handoffBaseURL, handoffToken)
                } else {
                    ToastCenter.shared.show("未检测到新文档，已取消接力")
                }
            }

        case .error:
            LogBuffer.shared.error("ChatViewModel.sse error event: \(event.data)")
            ToastCenter.shared.show("服务出错：\(event.data)")
            finishStreaming()
            detachStreamingMessage()
        default:
            break
        }
    }

    // MARK: Streaming Lifecycle

    private func finishStreaming() {
        throttleTimer?.invalidate()
        throttleTimer = nil
        applyStreamingContent()
        // 停止/出错时若 __streaming__ 占位无内容，直接移除（防三个点动画残留）；
        // 思考内容保留供折叠查看——但占位消息被移除时思考块无处挂，一并清空
        if let idx = messages.lastIndex(where: { $0.id == "__streaming__" }),
           messages[idx].content.isEmpty {
            messages.remove(at: idx)
            thoughtMessage = nil
        }
        isStreaming = false
        streamStartedAt = nil
        // 停止/出错路径无 done 事件，思考块转静态折叠态（done 路径同样安全）
        thoughtDone = true
        // 停止/出错时清校验状态，防 pill 残留
        // 注意：不清 toolQueue——工具 pill 由各自自动退场 timer 清理，
        // done 路径由 .done handler 清（此处清了 timer 永远没机会跑）
        pendingToolCalls = []
        verifyStatus = nil
        #if os(iOS)
        endBackgroundTask()
        #endif
        // 一轮结束（含停止/出错）：agent 可能刚改过文档，让打开着的详情页校一次正文
        NotificationCenter.default.post(name: .chatTurnEnded, object: nil)
    }

    /// 终止/出错后给残留的流式消息挂唯一本地 ID。
    /// 服务端取消时不持久化部分回复（readSSEStream 直接 return，不发 done），
    /// 该消息拿不到真实 ID；若继续占用哨兵 `__streaming__`，会与下一次发送的新占位
    /// 在 ForEach 里撞 ID（SwiftUI 复用视图），导致被终止内容先闪现在新消息位置。
    /// 只在非 done 收尾路径调用——done 路径由 replaceStreamingMessageID 换真实 ID。
    private func detachStreamingMessage() {
        guard let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        messages[idx].id = "local_" + UUID().uuidString
        messages[idx].toolCallsJSON = nil
    }

    #if os(iOS)
    private func beginBackgroundTask() {
        backgroundTaskID = UIApplication.shared.beginBackgroundTask { [weak self] in
            self?.sseClient.disconnect()
            self?.endBackgroundTask()
            Task { @MainActor in
                self?.isStreaming = false
                self?.streamStartedAt = nil
            }
        }
    }

    private func endBackgroundTask() {
        guard backgroundTaskID != .invalid else { return }
        UIApplication.shared.endBackgroundTask(backgroundTaskID)
        backgroundTaskID = .invalid
    }
    #endif

    // MARK: Plan Parsing

    private func parsePlanNode(_ data: String) -> PlanNode? {
        guard let d = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode(PlanNodeJSON.self, from: d)
        else { return nil }
        return PlanNode(id: obj.id, label: obj.label, status: obj.status)
    }

    private struct PlanNodeJSON: Codable {
        let id: String
        let label: String
        let status: String
    }

    // MARK: Helpers

    private func replaceStreamingMessageID(with msgID: String) {
        guard let index = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        messages[index].id = msgID
        messages[index].toolCallsJSON = nil
    }

    // 原地改 id，不重建消息：重建会把 imageIdsJSON / localImages 一起丢掉，
    // 附件图会在 done 事件到达时当场消失。
    private func replaceUserMessageID(with msgID: String) {
        guard let index = messages.lastIndex(where: { $0.role == "user" }) else { return }
        messages[index].id = msgID
    }

    private func updateLastAssistantMessage(_ content: String) {
        guard let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        messages[idx].content = content
        messages[idx].toolCallsJSON = nil
    }

    private func throttleFlush() {
        guard throttleTimer == nil else { return }
        throttleTimer = Timer.scheduledTimer(withTimeInterval: 0.1, repeats: false) { [weak self] _ in
            Task { @MainActor [weak self] in
                self?.throttleTimer = nil
                self?.applyStreamingContent()
            }
        }
    }

    private func applyStreamingContent() {
        let lastContent = messages.last(where: { $0.id == "__streaming__" })?.content
        guard lastContent != streamingContent else { return }
        updateLastAssistantMessage(streamingContent)
    }

    private func parseMessageDelta(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["delta"]
    }

    private func parseStatus(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONDecoder().decode([String: String].self, from: jsonData)
        else { return nil }
        return obj["message"]
    }

    private func parseTitleFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["title"] as? String
    }

    private func parseConversationIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["conversation_id"] as? String
    }

    private func parseMessageIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["message_id"] as? String
    }

    private func parseUserMessageIdFromDone(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        return obj["user_message_id"] as? String
    }

    /// 解析 done 事件中的上下文用量（context_tokens / context_window）。
    private func parseContextFromDone(_ data: String) -> (tokens: Int, window: Int)? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        let tokens = obj["context_tokens"] as? Int ?? 0
        let window = obj["context_window"] as? Int ?? 0
        if tokens == 0 && window == 0 { return nil }
        return (tokens, window)
    }

    private func parseMemoryIDsFromDone(_ data: String) -> [String]? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let ids = obj["memory_ids"] as? [String]
        else { return nil }
        return ids
    }

    private func parseNoteIDsFromDone(_ data: String) -> [String]? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let ids = obj["note_ids"] as? [String]
        else { return nil }
        return ids
    }

    // MARK: Tool call / result / verify 解析

    private struct ParsedToolCall {
        let id: String?
        let display: String
        let tool: String
        let function: String
        let args: [String: Any]
    }

    private func parseToolCall(_ data: String) -> ParsedToolCall? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        let display = obj["display"] as? String ?? obj["function"] as? String ?? "工具"
        // 入参原样留存：工具返回值常只有 id，标题/tags/正文得从入参捞（如 update_document）。
        let args = obj["args"] as? [String: Any] ?? [:]
        return ParsedToolCall(
            id: ToolCallPairing.callID(fromEventJSON: data),
            display: display,
            tool: obj["tool"] as? String ?? "",
            function: obj["function"] as? String ?? "",
            args: args
        )
    }

    private struct ParsedToolResult {
        let success: Bool
        let error: String?
        let data: [String: Any]?
        /// LLM tool_call id（服务端新事件带上；旧服务端为 nil）
        let toolCallId: String?
    }

    private func parseToolResult(_ data: String) -> ParsedToolResult? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let success = obj["success"] as? Bool
        else { return nil }
        return ParsedToolResult(
            success: success,
            error: obj["error"] as? String,
            data: obj["data"] as? [String: Any],
            toolCallId: ToolCallPairing.resultID(fromEventJSON: data)
        )
    }

    private struct ParsedVerify {
        let status: String
        let retry: Int
        let reason: String?
    }

    private func parseVerify(_ data: String) -> ParsedVerify? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let status = obj["status"] as? String
        else { return nil }
        return ParsedVerify(status: status, retry: obj["retry"] as? Int ?? 0, reason: obj["reason"] as? String)
    }

    private func parseDoneError(_ data: String) -> String? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let err = obj["error"] as? String, !err.isEmpty
        else { return nil }
        return err
    }

    /// 把 HTML 产物引用挂到当前流式回复上（幂等：同 id 只留一张；同轮再改则就地更新摘要）。
    /// 只改内存，不落盘——重载后由消息字段接管（服务端是唯一事实来源）。
    private func appendArtifactRef(_ ref: ArtifactRef) {
        guard let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        var refs = messages[idx].artifactRefs
        if let i = refs.firstIndex(where: { $0.id == ref.id }) {
            refs[i] = ref
        } else {
            refs.append(ref)
        }
        messages[idx].artifacts = refs
    }

    // MARK: Local Tool Results

    /// 把本地产物落成一条独立的 `local_tool_result` 消息并持久化，返回本地消息 id。
    /// 用本地稳定 id + createdAt（不依赖会变的 assistant id）。插到当前流式 assistant **之前**，
    /// 卡片即显示在该回复的思考 + 正文上方；重载后由 `mergeLocalToolResults` 按位置重放到同样位置。
    @discardableResult
    func insertLocalArtifact(_ artifact: LocalArtifact) -> String? {
        let payload: Data
        do {
            payload = try artifact.payload()
        } catch {
            LogBuffer.shared.error("insertLocalArtifact: encode \(artifact.type.rawValue) failed: \(error)")
            return nil
        }
        var msg = Message(
            id: "local_\(UUID().uuidString)",
            conversationId: conversationId ?? "",
            role: "local_tool_result",
            content: "",
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
        msg.localToolResult = LocalToolResultData(type: artifact.type, payload: payload)
        msg.localArtifact = artifact
        if let streamingIdx = messages.lastIndex(where: { $0.id == "__streaming__" }) {
            messages.insert(msg, at: streamingIdx)
        } else {
            messages.append(msg)
        }

        let id = msg.id
        let createdAt = msg.createdAt
        let convID = msg.conversationId
        let type = artifact.type
        // 归位锚点：本轮 user 消息 id（流式期是本地 UUID，done 后由 reattachLocalArtifactAnchors 改写）
        let anchor = currentRoundUserID
        // 空白 tab 首轮：会话 id 还没生成（要等 done），此时按空 id 落盘会写进 `.json`，
        // 重载按真实 id 读不到 → 卡片丢。先只留内存，done 拿到真实 id 后补落盘
        // （见 persistLocalArtifactsUnderConversation）。
        guard !convID.isEmpty else { return id }
        Task {
            do {
                try await LocalToolResultStore.shared.upsert(
                    type: type, payload: payload, id: id, createdAt: createdAt,
                    conversationId: convID, anchorUserID: anchor)
            } catch {
                LogBuffer.shared.error("insertLocalArtifact: persist \(type.rawValue) failed: \(error)")
            }
        }
        return id
    }

    /// 同轮文档卡片：把 patch 合并进本轮累积状态；同轮已出过卡片就就地更新（位置不变），
    /// 否则新落一条本地消息。见 `roundDocRefs` / `roundDocRefMessageID`。
    private func upsertRoundDocumentRef(_ patch: DocumentRefPatch) {
        let merged = (roundDocRefs[patch.id] ?? DocumentRef.make(from: patch)).merged(with: patch)
        roundDocRefs[patch.id] = merged

        let artifact = LocalArtifact.docRefs([merged])
        let payload: Data
        do {
            payload = try artifact.payload()
        } catch {
            LogBuffer.shared.error("upsertRoundDocumentRef: encode failed doc=\(patch.id): \(error)")
            return
        }

        if let localID = roundDocRefMessageID[patch.id],
           let idx = messages.firstIndex(where: { $0.id == localID }) {
            messages[idx].localArtifact = artifact
            messages[idx].localToolResult = LocalToolResultData(type: .docRefs, payload: payload)
            let createdAt = messages[idx].createdAt
            let convID = messages[idx].conversationId
            let anchor = currentRoundUserID
            // 空会话 id（首轮）先只留内存，done 后由 persistLocalArtifactsUnderConversation 补写。
            guard !convID.isEmpty else { return }
            Task {
                do {
                    try await LocalToolResultStore.shared.upsert(
                        type: .docRefs, payload: payload, id: localID, createdAt: createdAt,
                        conversationId: convID, anchorUserID: anchor)
                } catch {
                    LogBuffer.shared.error("upsertRoundDocumentRef: persist failed doc=\(patch.id): \(error)")
                }
            }
        } else if let newID = insertLocalArtifact(artifact) {
            roundDocRefMessageID[patch.id] = newID
        }
    }

    private func mergeLocalToolResults(from: String) async {
        let existingIDs = Set(messages.lazy.filter { $0.role == "local_tool_result" }.map { $0.id })

        let entries: [LocalToolResultStore.Entry]
        do {
            entries = try await LocalToolResultStore.shared.allEntries(for: conversationId ?? "")
        } catch {
            LogBuffer.shared.error("mergeLocalToolResults: load failed: \(error)")
            return
        }

        // 有锚点且锚点在本页 → 直接按位置插回（不参与时间排序）。
        // 其余（旧数据 / 停止轮锚点还是本地 id）→ 回退：只按下界过滤，再走时间排序 + 位置归位。
        var anchored: [(afterID: String, item: Message)] = []
        for entry in entries where !existingIDs.contains(entry.id) {
            guard let artifact = LocalArtifact(type: entry.type, payload: entry.payload) else {
                LogBuffer.shared.error("mergeLocalToolResults: decode \(entry.type.rawValue) id=\(entry.id) failed")
                continue
            }
            var msg = Message(
                id: entry.id,
                conversationId: conversationId ?? "",
                role: "local_tool_result",
                content: "",
                toolCallsJSON: nil,
                createdAt: entry.createdAt
            )
            msg.localToolResult = LocalToolResultData(type: entry.type, payload: entry.payload)
            msg.localArtifact = artifact

            if let anchor = entry.anchorUserID, messages.contains(where: { $0.id == anchor }) {
                anchored.append((afterID: anchor, item: msg))
            } else if LocalArtifactTime.isOnOrAfter(entry.createdAt, from: from) {
                messages.append(msg)
            }
            // 剩下的：锚点所在轮不在本页（上翻分页后会再读到），先不显示
        }

        // 按解析后的时间排序（本地卡片 UTC `Z` vs 服务端 `+08:00`，字符串排序会错位）。
        messages.sort { LocalArtifactTime.isOrdered($0.createdAt, before: $1.createdAt) }
        messages = LocalArtifactMerge.reposition(
            messages,
            isLocal: { $0.role == "local_tool_result" },
            isUser: { $0.role == "user" },
            isAssistant: { $0.role == "assistant" }
        )
        messages = LocalArtifactMerge.placeAnchored(
            messages, anchored: anchored, id: \.id
        )
    }

    /// 空白 tab 首轮的本地产物落盘（那时会话 id 尚未生成，insertLocalArtifact 只留了内存）。
    /// 拿到真实 id 后按该 id 补写，并清掉历史遗留的空会话键文件（`local_results/.json`）。
    /// 锚点用**扫描到的本轮 user 消息 id**（调用点在 done 的 user id 替换之后，故已是服务端 id）。
    private func persistLocalArtifactsUnderConversation(_ cid: String) {
        guard !cid.isEmpty else { return }
        var anchor: String?
        for msg in messages {
            if msg.role == "user" {
                anchor = msg.id
                continue
            }
            guard msg.role == "local_tool_result", let ltr = msg.localToolResult else { continue }
            let id = msg.id
            let createdAt = msg.createdAt
            let type = ltr.type
            let payload = ltr.payload
            let anchorID = anchor   // 每轮 Task 捕获当期值，别在循环里共享可变的 anchor
            Task {
                do {
                    try await LocalToolResultStore.shared.upsert(
                        type: type, payload: payload, id: id, createdAt: createdAt,
                        conversationId: cid, anchorUserID: anchorID)
                } catch {
                    LogBuffer.shared.error("persistLocalArtifactsUnderConversation: persist \(type.rawValue) failed: \(error)")
                }
            }
        }
        // 历史版本在空 id 下落过盘（文件名 `.json`）：一次性清掉，避免越攒越多。
        Task {
            do {
                try await LocalToolResultStore.shared.removeConversation("")
            } catch {
                LogBuffer.shared.error("persistLocalArtifactsUnderConversation: clean empty-key failed: \(error)")
            }
        }
    }

    /// 本轮 user 消息 id —— 本地产物的归位锚点。
    private var currentRoundUserID: String? {
        messages.last(where: { $0.role == "user" })?.id
    }

    /// done 把 user 消息 id 从本地 UUID 换成服务端 id：把本轮卡片的锚点一起改写，
    /// 否则重载后锚点匹配不上，只能退回按时间归位（被客户端时钟偏移带偏）。
    private func reattachLocalArtifactAnchors(from old: String?, to new: String) {
        guard let old, old != new, let cid = conversationId, !cid.isEmpty else { return }
        Task {
            do {
                try await LocalToolResultStore.shared.reattachAnchor(conversationId: cid, from: old, to: new)
            } catch {
                LogBuffer.shared.error("reattachLocalArtifactAnchors: \(error)")
            }
        }
    }
}
