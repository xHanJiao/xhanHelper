#if os(macOS)
import AppKit
import CoreGraphics
import SwiftUI

/// 全局「按住左右 ⌘ 说话」：检测 + 路由 + 浮层。
///
/// 为什么不用 Carbon `RegisterEventHotKey`：它注册不了裸修饰键（必须有主键）。
/// 为什么不用 `NSEvent.addGlobalMonitorForEvents`：要辅助功能授权，而且我们不需要吞键。
/// 这里用 `CGEventSource.flagsState` 轮询（30Hz）读**左右 Cmd 的设备位**（0x8 / 0x10）：
/// 两个 ⌘ 同时按住就开录 —— 这个组合没有任何正常用途，误触率接近 0，
/// 而且全程不吞键：Cmd+C / Cmd+Tab / Cmd+点击 照常发给前台 app。
///
/// 与视图解耦：热键不再挂在 ChatView 的 onAppear 上（PiP 收起、红叉关窗时那个视图根本不存在，
/// 旧实现就是在那里把热键槽清空然后静默失效的）。这里用一份独立的 `VoiceChatService`
/// 只跑「一次性采集」，窗口关着也能录、能发、能念回复。
@MainActor
final class VoiceHotKeyController {
    static let shared = VoiceHotKeyController()

    /// 两个 ⌘ 都按住多久才开录。取 0：双 ⌘ 本身没有正常用途，真正的误触由
    /// 「旁边还有别的键 / 鼠标动作 → 本次作废」兜底。检测本身就在 30Hz 轮询，
    /// 再加去抖只会让「按下就说话」吞掉开头（实测每多 50ms 就多吃半个字）
    private let armDelay: TimeInterval = 0
    /// 松开后浮层停留多久
    private let hudLinger: TimeInterval = 1.2

    private enum Route { case continuous, pushToTalk }

    /// 左右 ⌘ 的设备位（`NSEvent.ModifierFlags` 没把这两个常量导到 Swift，只能手写）
    private static let deviceLeftCommand = CGEventFlags(rawValue: 0x8)
    private static let deviceRightCommand = CGEventFlags(rawValue: 0x10)

    private var route: Route?
    /// 本轮已经真的开始录了（按下到开录之间还有权限弹窗/建连，中间松手要能作废）
    private var armed = false
    private var chordDownAt: Date?
    /// 本轮已作废（按下的瞬间旁边还有别的键/鼠标动作 → 不是想说话）
    private var cancelled = false
    private var pollTimer: Timer?

    /// 只跑一次性采集的服务；连续档（语音条开着）仍然交给视图那套
    private let ptt = VoiceChatService()
    private lazy var hud = VoiceHUDPanel(level: { [weak self] in self?.ptt.waveLevel ?? 0 })

    /// 后端地址 + token：窗口关着时也要能建连，所以缓存最后一份
    private var credentials: (baseURL: String, token: String)?
    private var lastViewModel: ChatViewModel?
    /// 浮层要不要跟着这一轮的 听/想/说 走（连续档松手就收，不跟会话）
    private var followTurn = false

    private init() {}

    func start() {
        guard pollTimer == nil else { return }
        ptt.onHoldSentence = { [weak self] text, speakReply in
            self?.submit(text, speakReply: speakReply)
        }
        // 30Hz：判定延迟上限 33ms（20Hz 时最多 50ms，加上去抖会让「按下 → 出声」明显发钝）；
        // Timer 比 displaylink 省电得多
        let timer = Timer(timeInterval: 1.0 / 30.0, repeats: true) { [weak self] _ in
            MainActor.assumeIsolated { self?.tick() }
        }
        RunLoop.main.add(timer, forMode: .common)
        pollTimer = timer
        LogBuffer.shared.diag("[hotkey] 双 ⌘ 按住说话已启动")
    }

    // MARK: - Carbon 备用热键（⌃⌥V）

    /// 按下（不经双 ⌘ 检测，直接开一轮）
    func hotKeyDown() {
        guard route == nil, !armed else { return }
        armed = true
        begin()
    }

    /// 松开
    func hotKeyUp() {
        guard armed else { return }
        armed = false
        finish()
    }

    // MARK: - 检测

    private func tick() {
        let now = Date()
        // 顺手把当前会话的 VM 抓住：窗口关掉后 provider 会返回 nil，这份缓存就是唯一能发话的入口
        if route == nil, let vm = AppShortcutBridge.shared.currentChatViewModel?() {
            lastViewModel = vm
        }
        let flags = CGEventSource.flagsState(.combinedSessionState)
        // 左右 ⌘ 的**设备位**：0x8 = 左、0x10 = 右（NX_DEVICELCMDKEYMASK / NX_DEVICERCMDKEYMASK）。
        // 注意：`CGEventSource.keyState(key: 54)` 读不到右 ⌘（探针实测：右 ⌘ 单独按住时
        // keyState 仍是 false，而 flagsState 已经给出 0x10），所以这里只看 flags。
        let chord = flags.contains(Self.deviceLeftCommand) && flags.contains(Self.deviceRightCommand)

        if chord {
            if chordDownAt == nil {
                chordDownAt = now
                cancelled = false
            } else if !cancelled, !armed,
                      now.timeIntervalSince(chordDownAt ?? now) >= armDelay {
                // 开录前只要旁边还有别的键/鼠标动作，就当这次不是想说话
                if hasOtherModifier || inputSince(chordDownAt ?? now) {
                    cancelled = true
                } else {
                    armed = true
                    begin()
                }
            }
        } else if chordDownAt != nil {
            chordDownAt = nil
            cancelled = false
            if armed {
                armed = false
                finish()
            }
        }

        updateHUD(now: now)
    }

    /// 除 ⌘ 以外还按着 Shift / Control / Option（组合键，不是我们的 chord）
    private var hasOtherModifier: Bool {
        !CGEventSource.flagsState(.combinedSessionState)
            .intersection([.maskShift, .maskControl, .maskAlternate]).isEmpty
    }

    /// 从 `start` 之后有没有发生过按键/鼠标按下/滚轮（`CGEventSource` 的整机计数，免授权）
    private func inputSince(_ start: Date) -> Bool {
        let elapsed = Date().timeIntervalSince(start)
        let types: [CGEventType] = [.keyDown, .leftMouseDown, .rightMouseDown, .otherMouseDown, .scrollWheel]
        return types.contains {
            CGEventSource.secondsSinceLastEventType(.combinedSessionState, eventType: $0) < elapsed
        }
    }

    // MARK: - 一轮的开合

    private func begin() {
        // 正在念上一轮回复：先停，否则录进去的是 TTS
        AppShortcutBridge.shared.stopVoicePlayback?()

        // 语音条开着：交给视图那套（按住暂存、松手整段提交，行为与以前一致）
        // Tink 由视图侧自己播（`.started` 时），这里不重复
        if AppShortcutBridge.shared.voiceBarActive?() == true {
            route = .continuous
            followTurn = false
            hud.show(phase: .listening)
            AppShortcutBridge.shared.onVoicePress?()
            return
        }

        guard let creds = currentCredentials() else {
            LogBuffer.shared.warning("[hotkey] 没有后端凭据（窗口没开过？），这次忽略")
            NSSound.beep()
            armed = false
            return
        }
        NSSound(named: "Tink")?.play()
        credentials = creds
        route = .pushToTalk
        followTurn = true
        hud.show(phase: .listening)
        Task { [weak self] in
            guard let self else { return }
            let result = await self.ptt.hotKeyPress(baseURL: creds.baseURL, token: creds.token)
            if result == .failed {
                NSSound.beep()
                self.route = nil
                self.armed = false
                self.followTurn = false
                self.hud.idle(now: Date())
            }
        }
    }

    private func finish() {
        guard let route else { return }
        self.route = nil
        switch route {
        case .continuous:
            AppShortcutBridge.shared.onVoiceRelease?()
            hud.idle(now: Date())
        case .pushToTalk:
            Task { [weak self] in
                guard let self else { return }
                let submitted = await self.ptt.hotKeyRelease()
                if !submitted {
                    NSSound.beep()
                    self.followTurn = false
                    self.hud.idle(now: Date())
                }
            }
        }
    }

    // MARK: - 提交

    private func submit(_ text: String, speakReply: Bool) {
        let vm = AppShortcutBridge.shared.currentChatViewModel?() ?? lastViewModel
        guard let vm, let creds = credentials else {
            LogBuffer.shared.warning("[hotkey] 识别到 \(text.count) 字，但没有会话/凭据可发")
            NSSound.beep()
            followTurn = false
            hud.idle(now: Date())
            return
        }
        lastViewModel = vm
        if vm.isStreaming {
            // 不发 /api/chat/stop：服务端 Register 会取消旧 loop，迟到的 stop 会误杀新轮
            // 这是「立刻接一轮语音」，不是用户停止：保留排队消息
            vm.stopStreaming(notifyServer: false, clearPending: false)
        }
        // 没有任何语音视图活着（关窗 / PiP 收起）时自己接管朗读，否则视图那套已经接好了
        if AppShortcutBridge.shared.voiceHotKeyOwner == nil {
            wireHeadlessSpeech(vm: vm, baseURL: creds.baseURL, token: creds.token)
        }
        vm.sendMessage(text, baseURL: creds.baseURL, token: creds.token, speakReply: speakReply)
    }

    /// 无视图时把这一轮回复接到自己的 TTS 上（听完就能听到答案，不用开窗）
    private func wireHeadlessSpeech(vm: ChatViewModel, baseURL: String, token: String) {
        vm.onAssistantReplyDelta = { [weak self] text in
            self?.ptt.appendReplyDelta(text, baseURL: baseURL, token: token)
        }
        vm.onAssistantReplyDone = { [weak self] text in
            self?.ptt.endReplyStream(text, baseURL: baseURL, token: token)
        }
        vm.onAssistantReplyAborted = { [weak self] in
            self?.ptt.stopSpeaking()
        }
        vm.replyOwner = ObjectIdentifier(ptt)
    }

    private func currentCredentials() -> (baseURL: String, token: String)? {
        if let live = AppShortcutBridge.shared.voiceCredentials?() {
            credentials = live
        }
        return credentials
    }

    // MARK: - 浮层相位

    private func updateHUD(now: Date) {
        guard hud.isVisible else {
            followTurn = false
            return
        }
        // 连续档松手后浮层就收（会话本身在视图里继续跑）
        guard followTurn else {
            hud.idle(now: now)
            return
        }
        // 与语音条同一套优先级：说 > 想 > 听
        if ptt.isSpeaking || AppShortcutBridge.shared.voiceIsSpeaking?() == true {
            hud.setPhase(.speaking)
        } else if ptt.isHoldTranscribing || (lastViewModel?.isStreaming ?? false) {
            hud.setPhase(.thinking)
        } else if ptt.isHoldRecording {
            hud.setPhase(.listening)
        } else {
            hud.idle(now: now)
        }
    }
}
#endif
