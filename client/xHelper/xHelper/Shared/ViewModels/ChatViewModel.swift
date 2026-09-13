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
    let id: UUID
    let toolName: String
    let displayName: String
    let argsSummary: String
    /// diff 预览（fs 写工具）：unified patch 文本 + 增减统计 + 目标路径。nil = 无 diff 展示
    let diffText: String?
    let additions: Int?
    let deletions: Int?
    let filePath: String?
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

// MARK: - ChatViewModel

@MainActor
class ChatViewModel: ObservableObject {
    @Published var messages: [Message] = []
    @Published var isStreaming = false
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

    /// 当前上下文 token 数（即时，随 done 事件更新）
    @Published var contextTokens = 0
    /// 上下文窗口上限（按模型档位，0=未配置）
    @Published var contextWindow = 0
    /// 会话消耗金额（慢更新，来自 usage 接口）
    @Published var usage: ConversationUsage?

    /// 响应检验（verify）开关，会话级：默认关。开=强制开启，关=强制关闭（后端全局配置作兜底）
    @Published var verifyEnabled = false
    /// 当前流的工具调用展示队列（tool_call 入队，tool_result 更新状态）
    @Published var toolQueue: [ToolCallDisplay] = []
    /// 待展示工具名 FIFO：tool_call 暂存 display，tool_result 到后取出构造完结 pill（不展示 running）
    private var pendingToolDisplays: [String] = []
    /// 响应检验事件状态（verify:checking|done|retry）
    @Published var verifyStatus: VerifyStatus?

    /// 会话模式："work"（默认）| "plan"（只读讨论）。只能由用户切换（Tab 快捷键/胶囊条）。
    /// TODO: 会话重新加载时从服务端同步真实 mode（当前先重置为 work，需会话详情 API 完善）
    @Published var mode: String = "work"
    /// plan 模式展示名（只读讨论状态）
    var isPlanMode: Bool { mode == "plan" }

    /// 新建会话成功（空 tab 转正）时回调：(conversationId, title)
    var onConversationCreated: ((String, String?) -> Void)?

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
    @AppStorage("memoryThreshold") var memoryThreshold: Double = 0.5
    @AppStorage("memoryLimit") var memoryLimit: Int = 8

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
        guard let plan = try? await apiClient.getConversationPlan(conversationId: cid) else { return }
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

    /// L3 工具执行确认（同一时刻最多一个，client tool 顺序执行）
    @Published var pendingToolConfirmation: ToolConfirmation?
    private var confirmationResolved = false

    /// 子任务用户介入请求（ask_user）：数组支持多 task 并发排队。
    /// 独立于 toolQueue——不被文本流清理（ToolActivityToast 在文本流开始时退场）。
    @Published var pendingUserRequests: [UserRequest] = []

    /// 子任务活动面板（SubagentPanel）：按 conv_id 归集，独立于消息流。
    @Published var subagentSessions: [SubagentSession] = []

    /// 子任务面板整体展开态：折叠时只留一行状态条，展开时浮层覆盖消息区。
    @Published var subagentPanelExpanded = false

    /// 信任模式（会话级本地状态）：开启后跳过 L3 工具确认，直接执行。
    /// 适合固定流程/重复操作；只影响客户端确认弹窗，服务端零感知。
    /// 换会话（loadConversation）自动关闭，安全默认。
    @Published var trustedMode = false

    /// 切换计划模式（work ↔ plan）。仅用户可触发（胶囊条点击）。
    /// 已有会话：PATCH 落库，失败回滚；空 tab（无会话）：只改本地，第一条消息经 init_mode 落库。
    func toggleMode() async {
        let next = isPlanMode ? "work" : "plan"
        guard let cid = conversationId, !cid.isEmpty else {
            mode = next // 空 tab：会话尚未创建，模式暂存本地，待首条消息 init_mode 落库
            return
        }
        let previous = mode
        mode = next
        do {
            try await apiClient.updateConversationMode(id: cid, mode: next)
            LogBuffer.shared.info("ChatViewModel.toggleMode: conv=\(cid) -> \(next)")
        } catch {
            LogBuffer.shared.warning("ChatViewModel.toggleMode failed: \(error)")
            mode = previous // 回滚
        }
    }

    /// 切换信任模式。仅用户可触发（胶囊条点击），切换时 Toast 明确反馈。
    func toggleTrustedMode() {
        trustedMode.toggle()
        if trustedMode {
            ToastCenter.shared.show("已进入信任模式：高风险工具不再确认")
        } else {
            ToastCenter.shared.show("已退出信任模式")
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

    /// 挂起等待用户确认 L3 工具执行；60s 超时视为拒绝。
    /// force=true 时信任模式不跳过（用于敏感域/明文页等注入高风险场景——信任模式本身就不可信）。
    /// bridgeToBrowser=true 时把确认同步发布到 BrowserActivityCenter，浏览器内也显示确认条（与聊天条同步出现）。
    /// diffText/additions/deletions/filePath：fs 写工具传 diff 预览，确认条展示 diff 卡片。
    func awaitToolConfirmation(
        toolName: String,
        arguments: [String: Any],
        force: Bool = false,
        bridgeToBrowser: Bool = false,
        diffText: String? = nil,
        additions: Int? = nil,
        deletions: Int? = nil,
        filePath: String? = nil
    ) async -> Bool {
        // 信任模式：跳过确认直接放行（force 时不跳过）
        if trustedMode && !force {
            return true
        }
        // 浏览器工具走人话摘要（ref→元素文本、密码脱敏、来源等），其余保留原始参数
        let argsSummary: String
        #if os(macOS)
        if bridgeToBrowser {
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
        confirmationResolved = false
        let myID = UUID() // per-confirmation 标识：超时任务只处理"当前 pending 还是自己"的确认，杜绝双重 resume
        let displayName = LocalToolRegistry.shared.displayName(for: toolName)
        // 浏览器确认桥：与 pendingToolConfirmation 同步发布，两处确认条同时出现（macOS only）
        #if os(macOS)
        if bridgeToBrowser {
            BrowserActivityCenter.shared.presentConfirmation(
                displayName: displayName,
                argsSummary: argsSummary,
                onResolve: { [weak self] allow in
                    self?.resolveToolConfirmation(allow: allow)
                }
            )
        }
        #endif
        return await withCheckedContinuation { (cont: CheckedContinuation<Bool, Never>) in
            pendingToolConfirmation = ToolConfirmation(
                id: myID,
                toolName: toolName,
                displayName: displayName,
                argsSummary: argsSummary,
                diffText: diffText,
                additions: additions,
                deletions: deletions,
                filePath: filePath,
                continuation: cont
            )
            playConfirmationAlert()
            // 60s 超时自动拒绝。confirmationResolved 是共享标志（新确认会重置），
            // 必须叠加 id 检查：旧确认的超时任务醒来时 pending 已是新确认（或 nil），跳过，
            // 否则会对已解决/已替换的 continuation 二次 resume 导致崩溃。
            Task { @MainActor [weak self] in
                try? await Task.sleep(for: .seconds(60))
                guard let self,
                      self.pendingToolConfirmation?.id == myID,
                      !self.confirmationResolved else { return }
                self.confirmationResolved = true
                self.pendingToolConfirmation = nil
                #if os(macOS)
                BrowserActivityCenter.shared.dismissConfirmation()
                #endif
                cont.resume(returning: false)
            }
        }
    }

    /// 用户点击允许/拒绝，结束确认等待
    func resolveToolConfirmation(allow: Bool) {
        guard let conf = pendingToolConfirmation, !confirmationResolved else { return }
        confirmationResolved = true
        pendingToolConfirmation = nil
        #if os(macOS)
        BrowserActivityCenter.shared.dismissConfirmation()
        #endif
        conf.continuation.resume(returning: allow)
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
                subagentSessions[idx].round = parseSubagentRound(content)
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
        guard let msgs = try? await apiClient.getMessages(conversationId: convID),
              let idx = subagentSessions.firstIndex(where: { $0.convID == convID }) else { return }
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
        if UserDefaults.standard.object(forKey: "xhan_l3_alert_sound") == nil
            || UserDefaults.standard.bool(forKey: "xhan_l3_alert_sound") {
            if let sound = NSSound(named: .init("Glass")) {
                sound.play()
            } else {
                NSSound.beep()
            }
        }
        #endif
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
    #if os(iOS)
    private var backgroundTaskID: UIBackgroundTaskIdentifier = .invalid
    #endif

    init(apiClient: APIClient) {
        self.apiClient = apiClient
        applyLLMDefaults()
    }

    // MARK: Load

    /// 加载会话。mode 从会话数据带入（服务端权威）；trustedMode 仅"切换会话"时重置，
    /// 同一会话刷新（scene 激活/重试/手动刷新）传 preserveTrustedMode=true 保持。
    func loadConversation(id: String, title: String, mode: String? = nil, preserveTrustedMode: Bool = false) async {
        sseClient.disconnect()
        isStreaming = false
        conversationId = id
        conversationTitle = title
        self.mode = mode ?? "work" // 会话列表数据带 mode（服务端权威）；旧数据缺失时默认 work
        // deep/flash 会话级本地状态：从该会话键恢复，无记录默认 flash
        deepMode = UserDefaults.standard.bool(forKey: "xhan_conv_deep_mode_\(id)")
        if deepMode {
            thinkingMode = true // deep 联动 thinking
        }
        // 响应检验开关会话级恢复：无记录默认关
        verifyEnabled = UserDefaults.standard.bool(forKey: "xhan_conv_verify_enabled_\(id)")
        toolQueue = []
        pendingToolDisplays = []
        verifyStatus = nil
        if !preserveTrustedMode {
            trustedMode = false // 信任模式会话级：换会话自动关闭（安全默认）
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
            if let first = messages.first, let last = messages.last {
                await mergeLocalToolResults(from: first.createdAt, to: last.createdAt)
            }
            oldestCursor = result.oldestCursor
            newestCursor = result.newestCursor
            hasMoreBefore = result.hasMoreBefore
            hasMoreAfter = result.hasMoreAfter
        } catch {
            LogBuffer.shared.error("ChatViewModel.loadConversation: \(error)")
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
                if let first = newMsgs.first, let last = messages.last {
                    Task { await mergeLocalToolResults(from: first.createdAt, to: last.createdAt) }
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
        messages = []
        isStreaming = false
        thoughtMessage = nil
        thoughtDone = false
        subagentSessions = []
        subagentPanelExpanded = false
        statusMessage = nil
        errorMessage = nil
        conversationId = ""
        conversationTitle = nil
        isEditing = false
        contextTokens = 0
        contextWindow = 0
        usage = nil
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
        streamingContent = ""
    }

    // MARK: Send

    func sendMessage(_ text: String, baseURL: String, token: String, agentId: String? = nil) {
        let userMsg = Message.localUser(text: text, conversationId: conversationId ?? "")
        messages.append(userMsg)

        isStreaming = true
        streamingContent = ""
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
        pendingToolDisplays = []
        verifyStatus = nil

        messages.append(.streamingAssistant(conversationId: conversationId ?? ""))

        Task { @MainActor in
            let clientTools = await Self.collectClientTools()
            setupSSE(baseURL: baseURL, token: token, text: text, agentId: agentId, clientTools: clientTools)
        }
        #if os(iOS)
        beginBackgroundTask()
        #endif
    }

    private func setupSSE(baseURL: String, token: String, text: String, agentId: String? = nil, clientTools: [[String: Any]]? = nil) {
        sseClient.disconnect()
        sseClient.onEvent = { [weak self] event in
            Task { @MainActor in
                self?.handleEvent(event)
            }
        }
        sseClient.onError = { [weak self] error in
            Task { @MainActor in
                let nsError = error as NSError
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
        // 空 tab（无会话）时带 init_mode：会话创建时应用客户端已切换的模式（work/plan）
        let initMode: String? = (conversationId == nil || conversationId?.isEmpty == true) ? mode : nil
        sseClient.connect(baseURL: baseURL, token: token, message: text, conversationId: conversationId, deepMode: deepMode, agentId: agentId, thinking: thinkingMode, temperature: temperature, maxTokens: maxTokens, memoryThreshold: memoryThreshold, memoryLimit: memoryLimit, clientTools: clientTools, initMode: initMode, verifyEnabled: verifyEnabled)
    }

    func stopStreaming() {
        // 先立即断开 SSE + UI 收尾（思考框消失），再后台通知服务端显式终止 ReAct。
        // 顺序保证：UI 即时响应，stopChat 失败/慢速不影响停止体验。
        let api = apiClient
        let cid = conversationId
        sseClient.disconnect()
        finishStreaming()
        detachStreamingMessage()
        Task {
            if let cid, !cid.isEmpty {
                do {
                    let stopped = try await api.stopChat(conversationId: cid)
                    LogBuffer.shared.info("stopChat: stopped=\(stopped)")
                } catch {
                    LogBuffer.shared.error("stopChat failed: \(error)")
                }
            }
        }
    }

    // MARK: SSE Event Handling

    private func handleEvent(_ event: StreamEvent) {
        switch event.type {
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
            pendingToolDisplays = []
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
                if pendingToolDisplays.isEmpty { toolQueue = [] }
                pendingToolDisplays.append(tc.display)
            }

        case .toolResult:
            if let tr = parseToolResult(event.data), !pendingToolDisplays.isEmpty {
                // 只在成功/失败时展示 pill（不展示 running）
                let display = pendingToolDisplays.removeFirst()
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
                pendingToolDisplays = []
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
            finishStreaming()
            statusMessage = nil
            toolQueue = []
            pendingToolDisplays = []
            verifyStatus = nil
            if let err = parseDoneError(event.data) {
                ToastCenter.shared.show("服务提示：\(err)")
            }
            if let cid = parseConversationIdFromDone(event.data) {
                conversationId = cid
                onConversationCreated?(cid, parseTitleFromDone(event.data))
                // 空白 tab 首条消息建会话后：把当前 deep/flash 选择预存到新会话键（后续重载继承）
                UserDefaults.standard.set(deepMode, forKey: "xhan_conv_deep_mode_\(cid)")
                // 同上：响应检验开关预存
                UserDefaults.standard.set(verifyEnabled, forKey: "xhan_conv_verify_enabled_\(cid)")
            }
            if let t = parseTitleFromDone(event.data) {
                conversationTitle = t
            }
            if let msgID = parseMessageIdFromDone(event.data) {
                replaceStreamingMessageID(with: msgID)
            }
            if let userMsgID = parseUserMessageIdFromDone(event.data) {
                replaceUserMessageID(with: userMsgID)
            }
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
        // 停止/出错路径无 done 事件，思考块转静态折叠态（done 路径同样安全）
        thoughtDone = true
        // 停止/出错时清校验状态，防 pill 残留
        // 注意：不清 toolQueue——工具 pill 由各自自动退场 timer 清理，
        // done 路径由 .done handler 清（此处清了 timer 永远没机会跑）
        pendingToolDisplays = []
        verifyStatus = nil
        #if os(iOS)
        endBackgroundTask()
        #endif
    }

    /// 终止/出错后给残留的流式消息挂唯一本地 ID。
    /// 服务端取消时不持久化部分回复（readSSEStream 直接 return，不发 done），
    /// 该消息拿不到真实 ID；若继续占用哨兵 `__streaming__`，会与下一次发送的新占位
    /// 在 ForEach 里撞 ID（SwiftUI 复用视图），导致被终止内容先闪现在新消息位置。
    /// 只在非 done 收尾路径调用——done 路径由 replaceStreamingMessageID 换真实 ID。
    private func detachStreamingMessage() {
        guard let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        let m = messages[idx]
        messages[idx] = Message(
            id: "local_" + UUID().uuidString,
            conversationId: m.conversationId,
            role: m.role,
            content: m.content,
            toolCallsJSON: nil,
            createdAt: m.createdAt
        )
    }

    #if os(iOS)
    private func beginBackgroundTask() {
        backgroundTaskID = UIApplication.shared.beginBackgroundTask { [weak self] in
            self?.sseClient.disconnect()
            self?.endBackgroundTask()
            Task { @MainActor in
                self?.isStreaming = false
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
        var msg = messages[index]
        messages.remove(at: index)
        msg = Message(
            id: msgID,
            conversationId: msg.conversationId,
            role: msg.role,
            content: msg.content,
            toolCallsJSON: nil,
            createdAt: msg.createdAt
        )
        messages.insert(msg, at: index)
    }

    private func replaceUserMessageID(with msgID: String) {
        guard let index = messages.lastIndex(where: { $0.role == "user" }) else { return }
        let msg = messages[index]
        messages[index] = Message(
            id: msgID,
            conversationId: msg.conversationId,
            role: "user",
            content: msg.content,
            toolCallsJSON: nil,
            createdAt: msg.createdAt
        )
    }

    private func updateLastAssistantMessage(_ content: String) {
        guard let idx = messages.lastIndex(where: { $0.id == "__streaming__" }) else { return }
        let last = messages[idx]
        messages[idx] = Message(
            id: "__streaming__",
            conversationId: last.conversationId,
            role: "assistant",
            content: content,
            toolCallsJSON: nil,
            createdAt: last.createdAt
        )
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
        let display: String
    }

    private func parseToolCall(_ data: String) -> ParsedToolCall? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any]
        else { return nil }
        let display = obj["display"] as? String ?? obj["function"] as? String ?? "工具"
        return ParsedToolCall(display: display)
    }

    private struct ParsedToolResult {
        let success: Bool
        let error: String?
    }

    private func parseToolResult(_ data: String) -> ParsedToolResult? {
        guard let jsonData = data.data(using: .utf8),
              let obj = try? JSONSerialization.jsonObject(with: jsonData) as? [String: Any],
              let success = obj["success"] as? Bool
        else { return nil }
        return ParsedToolResult(success: success, error: obj["error"] as? String)
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

    // MARK: Local Tool Results

    func insertChartMessage(_ cd: ChartData) {
        let payload = (try? JSONEncoder().encode(cd)) ?? Data()
        var msg = Message(
            id: "chart_\(UUID().uuidString)",
            conversationId: conversationId ?? "",
            role: "local_tool_result",
            content: "",
            toolCallsJSON: nil,
            createdAt: ISO8601DateFormatter().string(from: Date())
        )
        msg.localToolResult = LocalToolResultData(type: .chart, payload: payload)
        msg.chartData = cd
        messages.append(msg)

        Task {
            await LocalToolResultStore.shared.upsert(type: .chart, payload: payload, message: msg)
        }
    }

    private func mergeLocalToolResults(from: String, to: String) async {
        let existingIDs = Set(messages.lazy.filter { $0.role == "local_tool_result" }.map { $0.id })
        let entries = await LocalToolResultStore.shared.entries(for: conversationId ?? "", from: from, to: to)

        for entry in entries where !existingIDs.contains(entry.id) {
            var msg = Message(
                id: entry.id,
                conversationId: conversationId ?? "",
                role: "local_tool_result",
                content: "",
                toolCallsJSON: nil,
                createdAt: entry.createdAt
            )
            msg.localToolResult = LocalToolResultData(type: entry.type, payload: entry.payload)
            if entry.type == .chart, let cd = try? JSONDecoder().decode(ChartData.self, from: entry.payload) {
                msg.chartData = cd
            }
            messages.append(msg)
        }

        messages.sort { $0.createdAt < $1.createdAt }

        for i in stride(from: messages.count - 1, through: 0, by: -1) {
            guard messages[i].role == "local_tool_result" else { continue }
            for j in i + 1 ..< messages.count {
                if messages[j].role == "assistant" {
                    let item = messages.remove(at: i)
                    let insertAt = j > i ? j : j + 1
                    messages.insert(item, at: min(insertAt, messages.count))
                    break
                }
            }
        }
    }
}
