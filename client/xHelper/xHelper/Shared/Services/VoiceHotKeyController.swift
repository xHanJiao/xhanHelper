#if os(macOS)
import AppKit
import CoreGraphics
import SwiftUI

/// 全局「按住左右 ⌘ 说话」：检测 + 路由 + 浮层。
/// 多按一个 `⌥`（`⌥` + 双 ⌘）走「新会话」档：识别结果进一个新会话，不复用当前会话。
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
    /// 本轮是「新会话」档（⌥ + 双 ⌘）：提交时不进当前会话，另开一个。
    /// 每次真正开录的按下都会重设（尾巴/识别中的按下不算，见 `begin`）
    private var newConversation = false
    private var chordDownAt: Date?
    /// 本轮已作废（按下的瞬间旁边还有别的键/鼠标动作 → 不是想说话）
    private var cancelled = false
    /// 第几次按下（`begin()` 自增）：异步结果回来时用来判断是不是已经被下一次按下接管
    private var pressGen = 0
    private var pollTimer: Timer?

    /// 只跑一次性采集的服务；连续档（语音条开着）仍然交给视图那套
    private let ptt = VoiceChatService()
    private lazy var hud = VoiceHUDPanel(level: { [weak self] in self?.ptt.waveLevel ?? 0 })

    /// 后端地址 + token：窗口关着时也要能建连，所以缓存最后一份
    private var credentials: (baseURL: String, token: String)?
    private var lastViewModel: ChatViewModel?
    /// 上一次由控制器接管朗读的 VM（换会话前要摘 hook，见 `wireHeadlessSpeech`）
    private weak var headlessWiredVM: ChatViewModel?
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
                // 新一轮按下从这一帧就算开始了：上一轮还在飞的慢失败不能落到这一轮头上
                pressGen += 1
            } else if !cancelled, !armed,
                      now.timeIntervalSince(chordDownAt ?? now) >= armDelay {
                // 开录前只要旁边还有别的键/鼠标动作，就当这次不是想说话
                if hasOtherModifier || inputSince(chordDownAt ?? now) {
                    cancelled = true
                } else {
                    armed = true
                    // ⌥ 在按下瞬间前后几帧内到位都算数（arm 与双 ⌘ 按下只差一个 30Hz tick）
                    begin(newConversation: optionHeld)
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

    /// 除 ⌘ 以外还按着 Shift / Control（组合键，不是我们的 chord）。
    /// `⌥` 不算：它是有意的「新会话」档（见 `optionHeld`）
    private var hasOtherModifier: Bool {
        !CGEventSource.flagsState(.combinedSessionState)
            .intersection([.maskShift, .maskControl]).isEmpty
    }

    /// `⌥` 也按着 = 「新会话」档
    private var optionHeld: Bool {
        CGEventSource.flagsState(.combinedSessionState).contains(.maskAlternate)
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

    private func begin(newConversation: Bool = false) {
        // 上一轮还在录音尾巴（松手后 300ms）或识别中时，这一下会被 ptt 忽略（`.ignored`），
        // 不能用它覆盖上一轮还没提交的档位
        if route == nil, !ptt.isHoldRecording, !ptt.isHoldTranscribing {
            self.newConversation = newConversation
        }
        // 本轮按下的序号：异步失败回来时用它判断「这一轮是不是已经被下一次按下接管」
        pressGen += 1
        let gen = pressGen
        // 正在念上一轮回复：先停，否则录进去的是 TTS
        AppShortcutBridge.shared.stopVoicePlayback?()
        // 旧 VM 的朗读 hook 也要在这一刻摘：录制期间到达的 delta 会重新开口念，
        // 被 AEC 漏进本轮录音，或拼进上一轮的 speech 缓冲
        detachHeadlessSpeech()

        // 语音条开着：交给视图那套（按住暂存、松手整段提交，行为与以前一致）
        // Tink 由视图侧自己播（`.started` 时），这里不重复
        // 「新会话」档不路由：视图那套提交固定落旧 VM，换不了会话
        if !newConversation, AppShortcutBridge.shared.voiceBarActive?() == true {
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
            // 本次按下已作废：不清 `cancelled` 就会每 33ms 重进这里，beep 连成一片（docs/bugs/068）
            cancelled = true
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
                // 慢失败可能落在下一次按下已经开始之后（权限弹窗/建连期间松手再按）：
                // 那是上一轮的事，别把新一轮的 cancelled 置真，否则这次按下直到松手都没反应
                guard self.pressGen == gen else { return }
                NSSound.beep()
                // 一次失败只响一声，别让 tick 在按住期间反复重试
                self.cancelled = true
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
        // 这一轮按下时定的档；消费完立刻清，别漏给下一次识别
        let wantsNew = newConversation
        newConversation = false
        let vm: ChatViewModel?
        if wantsNew {
            // 新会话档：新建 tab 并把结果发到它上面（关窗时桥也能建，见 ChatTabManager）
            vm = AppShortcutBridge.shared.newChatViewModel?()
        } else {
            vm = AppShortcutBridge.shared.currentChatViewModel?() ?? lastViewModel
        }
        guard let vm, let creds = credentials else {
            LogBuffer.shared.warning("[hotkey] 识别到 \(text.count) 字，但\(wantsNew ? "开不了新会话" : "没有会话")/没有凭据，这次不发")
            NSSound.beep()
            followTurn = false
            hud.idle(now: Date())
            return
        }
        lastViewModel = vm
        // 新会话的 VM 刚建出来，没有在跑的轮次
        if !wantsNew, vm.isStreaming {
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
        // 上一轮的 hook 还挂在别的 VM 上时先摘掉（见 `detachHeadlessSpeech`）
        detachHeadlessSpeech()
        headlessWiredVM = vm
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

    /// 摘掉控制器自己挂在 VM 上的朗读 hook（视图接过 `replyOwner` 的不动）
    private func detachHeadlessSpeech() {
        guard let old = headlessWiredVM else { return }
        headlessWiredVM = nil
        guard old.replyOwner == ObjectIdentifier(ptt) else { return }
        old.onAssistantReplyDelta = nil
        old.onAssistantReplyDone = nil
        old.onAssistantReplyAborted = nil
        old.replyOwner = nil
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
        // 连续档（语音条开着）followTurn=false：松手就收、不跟着会话念完。
        // 但**按住期间**必须留着浮层 —— 那一路的录音在视图服务里，浮层消失会让人以为
        // 没进语音（原来直接 idle，表现成「按下去一秒半浮层自己没了」）。
        if !followTurn, !armed {
            hud.idle(now: now)
            return
        }
        // 与语音条同一套优先级：说 > 想 > 听
        if ptt.isSpeaking || AppShortcutBridge.shared.voiceIsSpeaking?() == true {
            hud.setPhase(.speaking)
        } else if ptt.isHoldTranscribing || (lastViewModel?.isStreaming ?? false) {
            hud.setPhase(.thinking)
        } else if ptt.isHoldRecording || armed {
            hud.setPhase(.listening)
        } else {
            hud.idle(now: now)
        }
    }
}
#endif
