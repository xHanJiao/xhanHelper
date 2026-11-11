import AVFoundation
import Combine
import Foundation

extension Notification.Name {
    /// 设置页开始试听：聊天窗先停朗读，避免两路音频叠在一起。
    static let voicePreviewWillStart = Notification.Name("VoicePreviewWillStart")
}

/// 语音模式：连续对话（常驻 ASR 连接）/ 长按说话（本地录音，松手整段灌入）。
enum VoiceMode: String {
    case continuous
    case hold
}

/// 语音聊天客户端：麦克风采集 → 自家后端 WebSocket → 豆包流式 ASR；
/// 回复文本 → 自家后端 TTS → 同一个 AVAudioEngine（VPIO 回声消除）播放。
@MainActor
final class VoiceChatService: ObservableObject {
    enum State: Equatable {
        case idle
        case requestingPermission
        case recording
        case finishing
    }

    @Published private(set) var state: State = .idle
    @Published private(set) var transcript = ""
    @Published private(set) var isAvailable = false
    @Published private(set) var speaker = ""

    /// 识别出最终文本后回调（由 ChatView 填入输入框）。
    var onFinalTranscript: ((String) -> Void)?
    /// C 模式：服务端下发一条已判停的分句，客户端直接提交一轮对话。
    var onSentence: ((String) -> Void)?
    /// 长按说话：松手整段识别出文本，客户端提交一轮。
    /// `speakReply` = 全局热键触发时朗读回复；语音条里的按住说话不朗读（保持现状）。
    var onHoldSentence: ((String, Bool) -> Void)?
    /// C3：播报期间用户开口，立即停生成（由 ChatView 接 /api/chat/stop）。
    var onBargeIn: (() -> Void)?
    /// 正在流式回答（C 模式的空闲计时器据此顺延，别在 AI 说话时把会话收掉）。
    var isChatBusy: (() -> Bool)?

    private var socket: URLSessionWebSocketTask?
    private let audio = VoiceAudioEngine()
    private var audioConfigured = false
    private var receiveTask: Task<Void, Never>?
    private var timeoutTask: Task<Void, Never>?
    private var maxTimer: Task<Void, Never>?
    private var sessionTask: Task<Void, Never>?
    private var idleTask: Task<Void, Never>?
    /// start/cancel 竞态代次：cancel 或新 start 会让旧 start 的续体失效
    private var generation = 0
    /// TTS 请求代次：stopSpeaking 后迟到的合成结果不再播放
    private var speakEpoch = 0
    private var maxSeconds = 60
    private var sessionSeconds = 600

    /// C 模式（连续对话）会话进行中；state 为 .recording 表示正在收音频
    @Published private(set) var continuousActive = false { didSet { syncVoiceBarPin() } }
    /// 波形用的瞬时音量 0...1（≈60Hz 更新）。
    /// **刻意不是 @Published**：Canvas 每帧自己读它，不必让整棵 ChatView 跟着刷新。
    private(set) var waveLevel: Double = 0
    /// TTS 正在播放（「说」态）
    @Published private(set) var isSpeaking = false
    /// 回复已生成完、TTS 还在合成音频（还没开播）：界面据此停在「想」，
    /// 不要在这段里先闪回「听」（听→想→听→说的那个多余「听」）。
    @Published private(set) var isPreparingSpeech = false
    /// 句级朗读进度：`speechSentences` = 已切开的句子 + 还没切开的流式尾巴；
    /// `speechIndex` = 正在播的句子。界面据此做「已播正常 / 当前高亮 / 未播淡显」。
    @Published private(set) var speechSentences: [String] = []
    @Published private(set) var speechIndex: Int = 0
    /// 空闲多久没听到完整分句就结束会话（秒）
    private let continuousIdleSeconds = 30
    /// 按住说：按住期间判停分句只暂存，不提交、不打断；松手后整段作为一轮。
    @Published private(set) var isHolding = false
    private var heldSentences: [String] = []
    /// 已松手、还在等最后半句 definite（1.5s 静默 / 3s 硬上限）
    private var heldAwaitingTail = false
    private var heldTailTask: Task<Void, Never>?
    private var heldCapTask: Task<Void, Never>?
    private let holdTailSeconds: Double = 1.5
    private let holdMaxSeconds: Double = 3

    /// 语音模式（continuous / hold）。默认 continuous：老用户升级后行为不变。
    @Published private(set) var mode: VoiceMode = VoiceMode(
        rawValue: UserDefaults.standard.string(forKey: VoicePreference.modeKey) ?? VoicePreference.modeDefault
    ) ?? .continuous
    /// 长按档：语音条开着（不代表正在录音）
    @Published private(set) var holdModeActive = false { didSet { syncVoiceBarPin() } }
    /// 切档过渡：旧链路已拆、新档还没就绪（连续档要等 ticket + 建连）。
    /// 界面靠它别把语音条整排换回普通按钮 —— 否则切一下闪一下。
    @Published private(set) var isSwitchingMode = false { didSet { syncVoiceBarPin() } }
    /// 长按说话：正在本地录音
    @Published private(set) var isHoldRecording = false
    /// 长按说话：松手后正在整段识别
    @Published private(set) var isHoldTranscribing = false
    /// 语音条是否开着（两档共用）。切档过渡期也算开着。
    var voiceBarActive: Bool { continuousActive || holdModeActive || isSwitchingMode }
    /// 语音条开着时不释放 VPIO：释放后再开会丢掉按下瞬间的音频（docs/bugs/057）
    private func syncVoiceBarPin() { audio.setVoiceBarOpen(voiceBarActive) }
    private var holdCapturing = false
    /// 全局热键按住说话：不进语音条、不置 holdModeActive（只影响这轮是否朗读回复）
    private var pushToTalkActive = false
    /// 这轮热键说话已经提交过（含 60 秒自动发送）：松手时别再误报失败
    private var pushToTalkSubmitted = false
    /// 全局热键这一轮由谁接管：按下时定，松手按它路由（不能看松手瞬间的状态，
    /// 连续会话可能在按住期间结束）。nil = 这次按下没被接管。
    private enum HotKeyPath { case continuousHold, pushToTalk }
    private var hotKeyPath: HotKeyPath?
    /// 按下代次：权限弹窗期间「按下→松开→再按下」时，旧 continuation 不能动新一次
    private var hotKeyAttempt = 0
    private var holdPCM = Data()
    private var holdFrames: [NearFieldGate.Frame] = []
    private var holdStartedAt = Date()
    private var holdGen = 0
    private var holdBaseURL = ""
    private var holdToken = ""
    private let holdTalkMaxSeconds = 60
    private let holdTalkMinSeconds: Double = 0.3
    /// 松手后再多收一段尾巴（毫秒）。VPIO 输入 + tap 缓冲有 150~300ms 延迟，
    /// 立刻 `removeTap` 会把最后几个字丢在设备缓冲里。这段只有静音，ASR 会忽略。
    private let holdTailGraceMs = 300
    private var speakEndTask: Task<Void, Never>?
    // 句级 TTS 流水线（代次复用 speakEpoch）
    private var speechFrozen: [String] = []
    private var speechTail = ""
    private var speechConsumed = 0
    private var speechTasks: [Int: Task<Data?, Never>] = [:]
    private var speechEnded = true
    private var speechNeedsPlaceholder = false
    private var speechPump: Task<Void, Never>?
    private var wakeCont: AsyncStream<Void>.Continuation?
    private var playContinuation: CheckedContinuation<Void, Error>?
    private var gateTask: Task<Void, Never>?
    /// 麦克风门是否生效（播报期间 + 播报结束后的 600ms 尾音窗口）
    private var micGated = false
    /// 打断判定：连续 3 帧（tap 3200 帧 @48k，约 200ms）超过阈值才算真的开口
    private var loudFrames = 0
    /// 近场闸开启时的打断判定：连续 5 帧（约 330ms），滤掉碰一下手机的瞬时尖峰。
    /// 闸门关闭时退回加闸之前的 3 帧 / 0.03 / 2.2（「关闭＝与现状完全一致」）。
    private var bargeInFrames: Int { nearFieldSensitivity == .off ? 3 : 5 }
    /// 打断判定的绝对地板（`onLevel` 是 RMS×4 的刻度）：真静音时的数字噪声不该触发
    private var bargeInFloor: Double { nearFieldSensitivity == .off ? 0.03 : 0.06 }
    /// 用户要明显高于「残响本底」才算开口。远场（0.5~1m）说话只有 0.04~0.08，钉死绝对
    /// 阈值就永远打不断（051 的教训），所以阈值跟着本机残响走：漏音大的音箱自己抬阈值。
    private var bargeInRatio: Double { nearFieldSensitivity == .off ? 2.2 : 4 }
    /// 播报期间门控电平的最近若干帧：p20 当残响本底，短暂人声抬不动它
    private var levelWindow: [Double] = []
    private let levelWindowSize = 30
    /// 门刚打开的一小段先不听：播放起始的爆音不算打断
    private var gateStartedAt = Date.distantPast
    /// 最近一次确认用户在说话的时间（服务端 `voice` 事件 / 新分句）：空闲收口看它，
    /// 别在人家说话中途把会话收掉
    private var lastVoiceAt = Date()

    /// 近场电平闸用的电平历史（约 2 秒）：每帧标上门控标记，播报期的 TTS 残留不算用户说话
    private var levelHistory: [NearFieldGate.Frame] = []
    private var lastNearFieldNoticeAt = Date.distantPast
    /// 本次发言期间出现过近处人声（任意 2 秒窗通过即记）。句子到达时用它放行：
    /// 只看到达那一刻的窗口会误杀被背景噪声拖后的句子，见 docs/bugs/058。
    private var nearFieldHeard = false
    /// 证据锁存的时刻。超过 15 秒没有新的近场帧就作废：ASR 整句丢掉时不会有 `sentence`
    /// 来消费证据，留着会把很久之后远处的声音放行。也不能设太短——058 里句子会被背景
    /// 噪声拖后好几秒。
    private var nearFieldHeardAt = Date.distantPast
    /// 上一句提交的时刻：那之前的电平帧不能再给下一句当证据
    /// （否则清零后 2 秒窗里的本句帧会立刻把证据重新锁存）
    private var lastSentenceAt = Date.distantPast
    /// 观察用（不改上行音频）：本地近场门若真按阈值逐帧清零，会挖掉多少发言时长
    private var probeStart: Date?
    private var probeLowStart: Date?
    private var probeLastHigh: Date?
    private var probeHoleSeconds: Double = 0
    private var probeMaxHole: Double = 0

    /// 每次现读：设置页改完下一句就生效，不用重开会话。
    private var nearFieldSensitivity: NearFieldGate.Sensitivity {
        NearFieldGate.Sensitivity(
            rawValue: UserDefaults.standard.string(forKey: VoicePreference.nearFieldKey) ?? ""
        ) ?? .standard
    }

    // 诊断（只记长度/次数，不记识别文本）
    private var sentBytes = 0
    private var sentChunks = 0
    private var droppedChunks = 0
    private var lastSendLog = Date.distantPast
    private var lastLevelLog = Date.distantPast
    private var peakLevel: Double = 0

    /// 语音链路诊断：走 `diag`（恒编译），`info` 在 Release 里是空操作，真机抓日志会一片空白。
    private func vlog(_ message: String) {
        LogBuffer.shared.diag("[voice] \(message)")
    }

    private func fmt3(_ value: Double) -> String {
        String(format: "%.3f", value)
    }

    var isRecording: Bool { state == .recording }
    var isBusy: Bool { state == .requestingPermission || state == .finishing }

    // MARK: 能力探测

    func refresh(baseURL: String, token: String) async {
        guard !baseURL.isEmpty, !token.isEmpty else {
            isAvailable = false
            return
        }
        do {
            let cfg = try await APIClient(baseURL: baseURL, token: token).getVoiceConfig()
            isAvailable = cfg.ready
            speaker = cfg.speaker
            maxSeconds = max(5, cfg.maxSeconds)
            sessionSeconds = max(30, cfg.sessionSeconds ?? 600)
            vlog("config ready=\(cfg.ready) max=\(maxSeconds)s session=\(sessionSeconds)s")
        } catch {
            // 未配置/网络失败时静默隐藏按钮，不影响文字聊天
            isAvailable = false
            LogBuffer.shared.diag("voice config unavailable: \(error.localizedDescription)")
        }
    }

    // MARK: 录音

    /// A 模式（识别 → 填输入框 → 手动发送）：macOS 已无入口（用户只留「连续对话」），
    /// 代码保留；真要用记得把入口接回来，别以为它在跑。
    func start(baseURL: String, token: String) async {
        await startSession(baseURL: baseURL, token: token, continuous: false)
    }

    /// C 模式：开麦后一直收音频，每收到一条已判停分句就通过 onSentence 提交一轮。
    func startContinuous(baseURL: String, token: String) async {
        await startSession(baseURL: baseURL, token: token, continuous: true)
    }

    private func startSession(baseURL: String, token: String, continuous: Bool) async {
        vlog("start requested mode=\(continuous ? "continuous" : "single") state=\(state) active=\(continuousActive) base=\(baseURL)")
        guard state == .idle, !continuousActive else {
            vlog("start ignored: state=\(state) continuousActive=\(continuousActive)")
            isSwitchingMode = false
            return
        }
        guard !baseURL.isEmpty, !token.isEmpty else {
            vlog("start ignored: empty baseURL/token")
            isSwitchingMode = false
            return
        }

        generation += 1
        let gen = generation
        state = .requestingPermission
        guard await ensureMicrophonePermission() else {
            vlog("microphone permission denied")
            if gen == generation { fail("没有麦克风权限，请在系统设置中允许 xhanHelper 使用麦克风") }
            return
        }
        vlog("microphone permission ok")
        guard gen == generation, state == .requestingPermission else { return }
        clearHeld()
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .spokenAudio,
                                                         options: [.defaultToSpeaker, .allowBluetooth])
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif

        do {
            let api = APIClient(baseURL: baseURL, token: token)
            let ticket = try await api.createVoiceASRTicket().ticket
            vlog("ticket ok (len=\(ticket.count))")
            guard gen == generation, state == .requestingPermission else {
                vlog("start aborted after ticket: gen=\(gen)/\(generation) state=\(state)")
                return
            }
            guard let url = websocketURL(baseURL: baseURL, ticket: ticket, continuous: continuous) else {
                fail("服务地址无效")
                return
            }
            stopSpeaking()
            // 兜底：上一轮的尾音门若因异常留在「关」的状态，新会话一帧都发不出去
            if micGated {
                vlog("sticky mic gate detected on start, forcing open")
                endMicGate(flushPreroll: false)
            }
            transcript = ""
            resetNearFieldEvidence()
            continuousActive = continuous
            isSwitchingMode = false
            sentBytes = 0
            sentChunks = 0
            droppedChunks = 0
            lastSendLog = .distantPast
            lastLevelLog = .distantPast
            peakLevel = 0
            levelHistory.removeAll(keepingCapacity: true)
            let task = URLSession.shared.webSocketTask(with: url)
            socket = task
            task.resume()
            vlog("ws resumed path=\(url.path) query=\(continuous ? "mode=continuous" : "-")")
            startReceiveLoop(task)

            configureAudio()
            // 立刻开录：服务端要先连豆包（几百毫秒）才会 ready，等它会把开头吞掉；
            // 早于 ready 的帧由 socket 缓冲，服务端连上后照常读。
            startCapture()
        } catch {
            vlog("start failed: \(error.localizedDescription)")
            if gen == generation { fail("无法开始语音识别：\(error.localizedDescription)") }
        }
    }

    /// 音频引擎回调只接一次：采集分片直接进 ASR，音量用于波纹与打断判定。
    private func configureAudio() {
        guard !audioConfigured else { return }
        audioConfigured = true
        audio.onChunk = { [weak self] data in
            Task { @MainActor [weak self] in
                self?.sendAudio(data)
            }
        }
        audio.onError = { [weak self] message in
            Task { @MainActor [weak self] in
                self?.fail("麦克风采集失败：\(message)")
            }
        }
        audio.onLevel = { [weak self] value in
            Task { @MainActor [weak self] in
                self?.handleLevel(Double(value))
            }
        }
        audio.onLog = { message in
            Task { @MainActor in
                LogBuffer.shared.diag("[voice] \(message)")
            }
        }
    }

    /// 音量为波纹供数，同时做 C3 的打断判定（只在播报期间有效）。
    private func handleLevel(_ value: Double) {
        guard state == .recording else { return }
        // 波形直接用这一帧的原始值（不做 EMA）：抖动留给波形自己的 15ms 起 / 60ms 落，
        // 否则 4~5Hz 的音节会被平滑成一条慢慢鼓起来的线
        waveLevel = value
        peakLevel = max(peakLevel, value)

        // 近场闸：每帧都记，但标上门控——播报期读到的是 TTS 漏音残留，不能当用户说话
        let now = Date()
        levelHistory.append(NearFieldGate.Frame(at: now, level: value, gated: micGated))
        let cutoff = now.addingTimeInterval(-(NearFieldGate.windowSeconds + 0.5))
        levelHistory.removeAll { $0.at < cutoff }
        // 长按说话：整段帧单独留一份，松手后离线判定（不受 2 秒窗裁剪）
        if holdCapturing {
            holdFrames.append(NearFieldGate.Frame(at: now, level: value, gated: false))
        }

        // 提交闸的证据：任意 2 秒窗里有近处人声就记一笔，句子到达时按它放行（058）
        if nearFieldHeard, now.timeIntervalSince(nearFieldHeardAt) > 15 {
            nearFieldHeard = false
        }
        if !nearFieldHeard,
           NearFieldGate.containsNearSpeech(frames: levelHistory, sensitivity: nearFieldSensitivity,
                                            since: lastSentenceAt) {
            nearFieldHeard = true
            nearFieldHeardAt = now
        }
        trackNearFieldProbe(value, now: now)

        if !(continuousActive && micGated) {
            loudFrames = 0
            levelWindow.removeAll(keepingCapacity: true)
        } else {
            // 门刚开也照样采样：预热期只听不判，本底不能从 0 开始长（否则开头 1 秒只剩硬地板）
            levelWindow.append(value)
            if levelWindow.count > levelWindowSize { levelWindow.removeFirst() }
        }

        let threshold = max(bargeInFloor, residualBaseline * bargeInRatio)
        if Date().timeIntervalSince(lastLevelLog) >= 1 {
            lastLevelLog = Date()
            vlog("level raw=\(fmt3(value)) peak=\(fmt3(peakLevel)) gated=\(micGated ? "yes" : "no") " +
                 "p20=\(fmt3(residualBaseline)) threshold=\(fmt3(threshold))")
            peakLevel = 0
        }

        // 门刚打开的一小段先不听：播放起始的爆音不算打断
        guard continuousActive, micGated, Date().timeIntervalSince(gateStartedAt) >= 0.25 else {
            loudFrames = 0
            return
        }
        if value > threshold {
            loudFrames += 1
        } else {
            loudFrames = 0
        }
        if loudFrames >= bargeInFrames {
            loudFrames = 0
            vlog("barge-in triggered raw=\(fmt3(value)) threshold=\(fmt3(threshold)) " +
                 "p20=\(fmt3(residualBaseline))")
            bargeIn()
        }
    }

    /// 门控电平的 p20：残响的短时抖动抬不高它，用户开口的那几帧也抬不动它。
    private var residualBaseline: Double {
        guard !levelWindow.isEmpty else { return 0 }
        let sorted = levelWindow.sorted()
        return sorted[(sorted.count - 1) / 5]
    }

    /// 用户开口打断：立刻停朗读、停生成，把门控期间攒下的音频冲回去继续识别。
    private func bargeIn() {
        endMicGate(flushPreroll: true)
        stopSpeaking()
        onBargeIn?()
    }

    /// 被近场闸丢掉时的轻提示：误拒比误收难受，必须让人看见；限频 10 秒，别刷屏。
    private func notifyNearFieldIgnored() {
        let now = Date()
        guard now.timeIntervalSince(lastNearFieldNoticeAt) >= 10 else { return }
        lastNearFieldNoticeAt = now
        ToastCenter.shared.show("已忽略：声音来自远处")
    }

    /// 观察用：本地近场门若真按阈值逐帧清零，会把「发言时长」挖掉多少。
    /// 只统计「第一次过阈值」到「句子到达」之间被关掉的时长（≥100ms 才算一个洞），
    /// 句间静音和尾部静音不计入。判据：占比 <5% 才值得考虑上前处理。
    private func trackNearFieldProbe(_ value: Double, now: Date) {
        guard continuousActive, !micGated, let threshold = nearFieldSensitivity.threshold else { return }
        if value >= threshold {
            if probeStart == nil {
                probeStart = now
                probeLowStart = nil
                probeHoleSeconds = 0
                probeMaxHole = 0
            }
            probeLastHigh = now
            if let low = probeLowStart {
                let hole = now.timeIntervalSince(low)
                probeMaxHole = max(probeMaxHole, hole)
                if hole >= 0.1 { probeHoleSeconds += hole }
                probeLowStart = nil
            }
        } else if probeStart != nil, probeLowStart == nil {
            probeLowStart = now
        } else if let low = probeLowStart, now.timeIntervalSince(low) > 5 {
            // 超过 5 秒没过阈值：这句没判停（被丢/没识别出来），下一句重新起算，
            // 别把这段空档算成「被挖掉的洞」
            probeStart = nil
            probeLowStart = nil
        }
    }

    private func logNearFieldProbe(chars: Int) {
        // 分母取「第一次过阈值 → 最后一次过阈值」：判停尾音和噪声拖出的延迟不算发言时长，
        // 否则同一段音频会随句子晚到而把 holes 占比稀释掉
        guard let start = probeStart, let lastHigh = probeLastHigh else { return }
        let span = lastHigh.timeIntervalSince(start)
        let holes = probeHoleSeconds
        let maxHole = probeMaxHole
        probeStart = nil
        probeLowStart = nil
        probeLastHigh = nil
        guard span > 0.5 else { return }
        vlog("nearfield probe: chars=\(chars) span=\(fmt3(span))s " +
             "holes=\(fmt3(holes))s (\(Int(holes / span * 100))%) maxHole=\(fmt3(maxHole))s")
    }

    /// 跨会话别带证据：上一轮的「近处人声」不能放行新会话的第一句
    private func resetNearFieldEvidence() {
        nearFieldHeard = false
        nearFieldHeardAt = .distantPast
        lastSentenceAt = .distantPast
        probeStart = nil
        probeLowStart = nil
        probeLastHigh = nil
        probeHoleSeconds = 0
        probeMaxHole = 0
    }

    /// 开始推 PCM。
    private func startCapture() {
        guard state == .requestingPermission else {
            vlog("startCapture ignored: state=\(state)")
            return
        }
        do {
            try audio.startCapture()
            state = .recording
            lastVoiceAt = Date()
            vlog("capture started state=\(state)")
            if continuousActive {
                armSessionLimit()
                armIdleTimer()
                return
            }
            maxTimer?.cancel()
            maxTimer = Task { [weak self] in
                guard let self else { return }
                let seconds = max(1, self.maxSeconds - 2)
                try? await Task.sleep(for: .seconds(seconds))
                guard !Task.isCancelled, self.state == .recording else { return }
                self.stop()
            }
        } catch {
            vlog("capture failed: \(error.localizedDescription)")
            fail("麦克风采集失败：\(error.localizedDescription)")
        }
    }

    func stop() {
        guard state == .recording, !continuousActive else {
            vlog("stop ignored: state=\(state) continuousActive=\(continuousActive)")
            return
        }
        vlog("stop requested")
        state = .finishing
        maxTimer?.cancel()
        maxTimer = nil
        sendTailThenEnd(detachCapture())
        armFinishTimeout()
    }

    /// 结束 C 模式会话：先发负包，让服务端把最后一句吐完再随连接关闭一起收尾。
    /// `flush` = 用户主动退出时先把暂存提交（不丢话）；上限/空闲收口传 false。
    func stopContinuous(flush: Bool = true) {
        vlog("stopContinuous requested state=\(state) active=\(continuousActive) flush=\(flush)")
        guard continuousActive else { return }
        if flush { flushHeld(force: true) }
        // 还没开采集（socket 刚建/权限确认中）：直接收掉，不用等负包，也别卡 15 秒兜底
        if state == .requestingPermission {
            endContinuous(reason: nil)
            return
        }
        guard state == .recording else { return }
        state = .finishing
        sessionTask?.cancel()
        sessionTask = nil
        idleTask?.cancel()
        idleTask = nil
        sendTailThenEnd(detachCapture())
        armFinishTimeout()
    }

    /// 结束录音后等 final 的兜底：负包没吐结果时不至于一直卡在 finishing。
    private func armFinishTimeout() {
        timeoutTask?.cancel()
        timeoutTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(15))
            guard !Task.isCancelled, let self, self.state == .finishing else { return }
            self.fail("语音识别超时")
        }
    }

    func cancel() {
        vlog("cancel: state=\(state) active=\(continuousActive) sent=\(sentBytes)B")
        generation += 1
        continuousActive = false
        resetNearFieldEvidence()
        holdModeActive = false
        isSwitchingMode = false
        abortHold()
        clearHeld()
        cancelTimers()
        _ = detachCapture()
        sendControl("cancel")
        closeSocket()
        state = .idle
    }

    // MARK: C 模式会话

    /// 会话硬上限：按音频时长计费（静音也计时），到点必须收。
    private func armSessionLimit(grace: Bool = false) {
        sessionTask?.cancel()
        sessionTask = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(for: .seconds(grace ? 15 : self.sessionSeconds))
            guard !Task.isCancelled, self.continuousActive else { return }
            // 正在回答/朗读时别硬切：给 15 秒宽限，宽限到了再收
            if !grace, self.isSpeaking || (self.isChatBusy?() ?? false) {
                self.armSessionLimit(grace: true)
                return
            }
            LogBuffer.shared.diag("[voice] session limit reached (\(self.sessionSeconds)s)")
            self.stopContinuous(flush: false)
            ToastCenter.shared.show("语音会话已到上限（\(self.sessionSeconds / 60) 分钟），已结束")
        }
    }

    // MARK: 按住说（hold 暂存）

    /// 按下（空格/按钮点击）：之后收到的判停分句只暂存，不提交、不打断。
    func startHold() {
        guard continuousActive, state == .recording else { return }
        // 松手后的等尾巴窗口内再次按住：上一批暂存还没提交，先提交，别丢话
        if !heldSentences.isEmpty { flushHeld(force: true) }
        cancelHeldTasks()
        heldSentences = []
        heldAwaitingTail = false
        isHolding = true
        lastVoiceAt = Date()
        stopSpeaking()
        vlog("hold start")
    }

    /// 松开：最后半句可能还在 ASR 判停里，先等 1.5s 静默（3s 硬上限），再整段提交。
    func endHold() {
        guard isHolding else { return }
        isHolding = false
        heldAwaitingTail = true
        vlog("hold end")
        heldCapTask?.cancel()
        heldCapTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(self?.holdMaxSeconds ?? 3))
            guard !Task.isCancelled, let self, self.heldAwaitingTail else { return }
            self.flushHeld()
        }
        armHeldTail()
    }

    /// 尾巴窗口：每来一条 sentence/voice 重置 1.5s 静默计时（3s 上限不重置）。
    private func armHeldTail() {
        guard heldAwaitingTail else { return }
        heldTailTask?.cancel()
        heldTailTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(self?.holdTailSeconds ?? 1.5))
            guard !Task.isCancelled, let self, self.heldAwaitingTail else { return }
            self.flushHeld()
        }
    }

    /// 合并暂存并提交一轮（走与普通分句相同的 onSentence 路径）。
    private func flushHeld(force: Bool = false) {
        if isHolding {
            guard force else { return }
            isHolding = false
        }
        // 会话已结束/正在收尾（上限、断线、用户已退出）：清空暂存，不补发
        guard continuousActive, state == .recording else {
            clearHeld()
            return
        }
        heldAwaitingTail = false
        cancelHeldTasks()
        let text = heldSentences.joined().trimmingCharacters(in: .whitespacesAndNewlines)
        heldSentences = []
        guard !text.isEmpty else { return }
        vlog("hold flush chars=\(text.count)")
        lastVoiceAt = Date()
        onSentence?(text)
    }

    /// 是否有还没提交的暂存（用于退出语音时决定是否通知服务端停旧轮）
    var hasHeldText: Bool { !heldSentences.isEmpty }

    private func cancelHeldTasks() {
        heldTailTask?.cancel()
        heldTailTask = nil
        heldCapTask?.cancel()
        heldCapTask = nil
    }

    private func clearHeld() {
        isHolding = false
        heldAwaitingTail = false
        cancelHeldTasks()
        heldSentences = []
    }

    // MARK: 长按说话（本地录音 + 松手整段灌入）

    /// 进入长按档的语音条（不碰麦克风、不建连接）。
    func enterHoldMode() {
        guard !voiceBarActive else { return }
        generation += 1
        holdGen += 1
        holdModeActive = true
        vlog("enter voice (hold)")
    }

    /// 退出语音条：连续档先收会话，长按档直接释放。
    func exitVoiceMode() {
        // 切档过渡期还没有会话：硬收口，别让在飞的 startContinuous 又把语音条开起来
        if isSwitchingMode { cancel() }
        if continuousActive { stopContinuous() }
        if holdModeActive {
            holdGen += 1
            holdModeActive = false
            abortHold()
            vlog("exit voice (hold)")
        }
    }

    /// 切档：两档互斥。语音条开着时直接拆旧链路，按新档位重开。
    func switchMode(_ next: VoiceMode, baseURL: String, token: String) {
        guard next != mode else { return }
        mode = next
        UserDefaults.standard.set(next.rawValue, forKey: VoicePreference.modeKey)
        vlog("mode switched: \(next.rawValue)")
        guard voiceBarActive else { return }
        cancel()
        // 连续档要等 ticket + 建连才 continuousActive，这段里靠它保持语音条不拆
        isSwitchingMode = true
        holdModeActive = false
        if next == .hold {
            holdGen += 1
            holdModeActive = true
            isSwitchingMode = false
            return
        }
        Task { await startContinuous(baseURL: baseURL, token: token) }
    }

    /// 按下：只本地录音（累积 PCM + 电平帧），不建连接。
    func startHoldTalk(baseURL: String, token: String) async {
        guard mode == .hold, holdModeActive, !isHoldRecording, !isHoldTranscribing else { return }
        guard state == .idle else {
            vlog("hold start ignored: state=\(state)")
            return
        }
        guard !baseURL.isEmpty, !token.isEmpty else { return }
        holdBaseURL = baseURL
        holdToken = token
        holdGen += 1
        let gen = holdGen
        guard await ensureMicrophonePermission() else {
            vlog("hold microphone permission denied")
            ToastCenter.shared.show("没有麦克风权限，请在系统设置中允许 xhanHelper 使用麦克风")
            return
        }
        guard gen == holdGen, holdModeActive, mode == .hold else { return }
        beginHoldCapture()
    }

    /// 全局热键按下：不进语音条、不置 holdModeActive 的本地录音。
    /// 返回是否真的开录（权限弹窗期间松手、已有会话等情况返回 false）。
    func startPushToTalk(baseURL: String, token: String) async -> Bool {
        guard !continuousActive, !isSwitchingMode, !isHoldRecording, !isHoldTranscribing else {
            return false
        }
        pushToTalkSubmitted = false
        // 语音条已经开着（录音档）：沿用那条采集链路，只是这轮回复要朗读
        if holdModeActive {
            await startHoldTalk(baseURL: baseURL, token: token)
            pushToTalkActive = isHoldRecording
            return isHoldRecording
        }
        guard state == .idle else {
            vlog("ptt start ignored: state=\(state)")
            return false
        }
        guard !baseURL.isEmpty, !token.isEmpty else { return false }
        holdBaseURL = baseURL
        holdToken = token
        holdGen += 1
        let gen = holdGen
        guard await ensureMicrophonePermission() else {
            vlog("ptt microphone permission denied")
            return false
        }
        guard gen == holdGen, state == .idle else { return false }
        pushToTalkActive = true
        beginHoldCapture()
        if !isHoldRecording { pushToTalkActive = false }
        return isHoldRecording
    }

    enum HotKeyStartResult { case started, failed, ignored }

    /// 全局热键按下：连续会话开着就按住暂存，否则一次性采集。
    /// `.ignored` = 重复按下 / 已被松手作废（调用方不要发声）。
    func hotKeyPress(baseURL: String, token: String) async -> HotKeyStartResult {
        // 松开事件丢了（⌘-Tab / 安全输入）：60 秒兜底收完录音后清掉残留路径，
        // 否则之后每次按下都会被当成重复按下，热键静默失效
        if hotKeyPath == .pushToTalk, !isHoldRecording, !isHoldTranscribing { hotKeyPath = nil }
        if hotKeyPath == .continuousHold, !isHolding, !continuousActive { hotKeyPath = nil }
        guard hotKeyPath == nil, !isHoldRecording, !isHolding, !isHoldTranscribing else {
            return .ignored
        }
        hotKeyAttempt += 1
        let attempt = hotKeyAttempt
        // 打断上一轮朗读：否则录进去的会是 TTS
        stopSpeaking()
        if continuousActive {
            hotKeyPath = .continuousHold
            startHold()
            guard isHolding else {
                hotKeyPath = nil
                return .failed
            }
            return .started
        }
        hotKeyPath = .pushToTalk
        let started = await startPushToTalk(baseURL: baseURL, token: token)
        // 权限弹窗期间松手 / 已经开了新一次按下：别把它当启动失败，也别动新的路径
        guard hotKeyPath == .pushToTalk, attempt == hotKeyAttempt else { return .ignored }
        guard started else {
            hotKeyPath = nil
            return .failed
        }
        return .started
    }

    /// 全局热键松开。返回 false 表示这轮没提交（调用方 beep）。
    func hotKeyRelease() async -> Bool {
        guard let path = hotKeyPath else { return true }
        hotKeyPath = nil
        switch path {
        case .continuousHold:
            if isHolding { endHold() }
            return true
        case .pushToTalk:
            return await finishPushToTalk()
        }
    }

    /// 全局热键松开：整段识别并提交。返回是否提交了一轮（false → 调用方 beep）。
    func finishPushToTalk() async -> Bool {
        // 60 秒自动发送 / 上一轮已经提交：松手只是收尾，别当失败
        if isHoldTranscribing || pushToTalkSubmitted {
            pushToTalkSubmitted = false
            return true
        }
        // 不能因为「还没开录」直接 return：权限弹窗期间松手必须 bump holdGen，
        // 否则在飞的 startPushToTalk 续体仍会开麦，没人再收（P1）
        return await finishHoldCapture(cancelled: false)
    }

    /// 采集内核：iOS 会话配置 + 清空缓冲 + 开 tap。
    private func beginHoldCapture() {
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .spokenAudio,
                                                         options: [.defaultToSpeaker, .allowBluetooth])
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif
        configureAudio()
        holdPCM = Data()
        holdFrames.removeAll(keepingCapacity: true)
        holdStartedAt = Date()
        do {
            try audio.startCapture()
            state = .recording
            holdCapturing = true
            isHoldRecording = true
            vlog("hold start")
        } catch {
            vlog("hold capture failed: \(error.localizedDescription)")
            ToastCenter.shared.show("麦克风采集失败：\(error.localizedDescription)")
        }
    }

    /// 松手：整段判定 → 灌入流式 ASR → 出字提交（语音条里的按住说话不朗读）。
    @discardableResult
    func finishHoldTalk(cancelled: Bool) async -> Bool {
        await finishHoldCapture(cancelled: cancelled)
    }

    /// 按住说话 / 全局热键共用的收尾内核。返回是否提交了一轮。
    private func finishHoldCapture(cancelled: Bool) async -> Bool {
        defer { pushToTalkActive = false }
        guard isHoldRecording else {
            // 只作废「按下还没落到录音」的那次启动（权限弹窗期间松手）；
            // 识别进行中再被调一次（60 秒自动提交后抬手/失焦）不能加代次，否则会把已识别文本丢掉
            if !isHoldTranscribing { holdGen += 1 }
            return false
        }
        // 松手不代表音频已经到齐：先留着 tap 再收一段尾巴，否则结尾几个字留在设备缓冲里被丢掉
        let held = Date().timeIntervalSince(holdStartedAt)
        if !cancelled {
            try? await Task.sleep(for: .milliseconds(holdTailGraceMs))
            // 这 300ms 里可能被切档/退出语音打断（abortHold 已经把采集拆了）
            guard isHoldRecording else { return false }
        }
        isHoldRecording = false
        holdCapturing = false
        if let tail = audio.stopCapture() { holdPCM.append(tail) }
        waveLevel = 0
        state = .idle
        let gen = holdGen
        let pcm = holdPCM
        let frames = holdFrames
        holdPCM = Data()
        holdFrames.removeAll(keepingCapacity: true)
        if cancelled {
            vlog("hold cancelled")
            return false
        }
        let duration = held
        guard duration >= holdTalkMinSeconds else {
            ToastCenter.shared.show("说话时间太短")
            return false
        }
        switch NearFieldGate.judgeHoldSpan(frames: frames, sensitivity: nearFieldSensitivity) {
        case .tooShort:
            vlog("hold too short: frames=\(frames.count) duration=\(fmt3(duration))s")
            ToastCenter.shared.show("说话时间太短")
            return false
        case .reject:
            vlog("hold dropped: near-field gate frames=\(frames.count)")
            notifyNearFieldIgnored()
            return false
        case .accept:
            break
        }
        guard !pcm.isEmpty else {
            ToastCenter.shared.show("说话时间太短")
            return false
        }
        isHoldTranscribing = true
        defer { if gen == holdGen { isHoldTranscribing = false } }
        do {
            let text = try await transcribeHold(pcm, baseURL: holdBaseURL, token: holdToken)
            guard gen == holdGen, holdModeActive || pushToTalkActive else { return false }
            let trimmed = text.trimmingCharacters(in: .whitespacesAndNewlines)
            vlog("hold done chars=\(trimmed.count) audio=\(fmt3(Double(pcm.count) / 32000))s held=\(fmt3(duration))s")
            guard !trimmed.isEmpty else {
                ToastCenter.shared.show("没有听清，请再说一次")
                return false
            }
            if pushToTalkActive { pushToTalkSubmitted = true }
            onHoldSentence?(trimmed, pushToTalkActive)
            return true
        } catch {
            guard gen == holdGen else { return false }
            LogBuffer.shared.error("voice hold transcribe failed: \(error.localizedDescription)")
            ToastCenter.shared.show("语音识别失败")
            return false
        }
    }

    /// 把整段 PCM 灌进一条短连接：松手才建连，吐完即断。
    private func transcribeHold(_ pcm: Data, baseURL: String, token: String) async throws -> String {
        let ticket = try await APIClient(baseURL: baseURL, token: token).createVoiceASRTicket().ticket
        guard let url = websocketURL(baseURL: baseURL, ticket: ticket, continuous: true) else {
            throw NSError(domain: "VoiceChatService", code: -1,
                          userInfo: [NSLocalizedDescriptionKey: "服务地址无效"])
        }
        let task = URLSession.shared.webSocketTask(with: url)
        task.resume()
        defer { task.cancel(with: .normalClosure, reason: nil) }
        // 灌入不按实时节拍：豆包接受快于实时的音频（6.2s 音频 0.79s 出 final）
        let chunkSize = 6400
        var offset = 0
        while offset < pcm.count {
            let end = min(offset + chunkSize, pcm.count)
            try await task.send(.data(pcm.subdata(in: offset..<end)))
            offset = end
        }
        try await task.send(.string("{\"type\":\"end\"}"))
        // 兜底：服务端吐完会主动关连接；真卡住也不能把 UI 挂死
        let watchdog = Task {
            try? await Task.sleep(for: .seconds(15))
            task.cancel(with: .normalClosure, reason: nil)
        }
        defer { watchdog.cancel() }
        var text = ""
        while true {
            let message: URLSessionWebSocketTask.Message
            do {
                message = try await task.receive()
            } catch {
                // 服务端吐完分句后主动关连接：正常收尾
                vlog("hold ws closed: \(error.localizedDescription)")
                break
            }
            guard case .string(let raw) = message, let data = raw.data(using: .utf8) else { continue }
            let parsed: VoiceSocketMessage
            do {
                parsed = try JSONDecoder().decode(VoiceSocketMessage.self, from: data)
            } catch {
                LogBuffer.shared.warning("voice hold frame undecodable: \(error.localizedDescription)")
                continue
            }
            switch parsed.type {
            case "sentence":
                if let value = parsed.text { text += value }
            case "error":
                throw NSError(domain: "VoiceChatService", code: -2,
                              userInfo: [NSLocalizedDescriptionKey: parsed.error ?? "语音识别失败"])
            default:
                break
            }
        }
        return text
    }

    /// 硬拆长按采集（退出语音 / 切档）：丢掉本次录音。
    private func abortHold() {
        holdCapturing = false
        isHoldRecording = false
        isHoldTranscribing = false
        pushToTalkActive = false
        hotKeyPath = nil
        holdGen += 1
        holdPCM = Data()
        holdFrames.removeAll(keepingCapacity: true)
        if state == .recording {
            _ = detachCapture()
        }
        waveLevel = 0
    }

    /// 空闲收口：一直没人说话就别占着豆包并发、白扣音频时长。
    private func armIdleTimer() {
        idleTask?.cancel()
        idleTask = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(for: .seconds(self.continuousIdleSeconds))
            guard !Task.isCancelled, self.continuousActive else { return }
            // AI 正在回答/朗读、用户按住说（可能正在想）、或刚刚还在说话（长句没判停）
            // 也算「会话仍在进行」，顺延而不是收口
            if self.isSpeaking || self.isHolding || (self.isChatBusy?() ?? false)
                || Date().timeIntervalSince(self.lastVoiceAt) < TimeInterval(self.continuousIdleSeconds) {
                self.armIdleTimer()
                return
            }
            LogBuffer.shared.diag("[voice] idle limit reached (\(self.continuousIdleSeconds)s), stopping")
            self.stopContinuous(flush: false)
            ToastCenter.shared.show("\(self.continuousIdleSeconds) 秒没有听到说话，语音会话已结束")
        }
    }

    /// 停止采集，返回不足 200ms 的尾巴（调用方负责在 end 之前发出）。
    private func detachCapture() -> Data? {
        micGated = false
        audio.setGated(false)
        let tail = audio.stopCapture()
        waveLevel = 0
        return tail
    }

    private func cancelTimers() {
        maxTimer?.cancel()
        maxTimer = nil
        sessionTask?.cancel()
        sessionTask = nil
        idleTask?.cancel()
        idleTask = nil
        timeoutTask?.cancel()
        timeoutTask = nil
    }

    private func endContinuous(reason: String?) {
        vlog("end continuous: reason=\(reason ?? "-") state=\(state) sent=\(sentBytes)B chunks=\(sentChunks) dropped=\(droppedChunks)")
        guard continuousActive else { return }
        generation += 1
        continuousActive = false
        resetNearFieldEvidence()
        clearHeld()
        cancelTimers()
        _ = detachCapture()
        closeSocket()
        state = .idle
        if let reason { ToastCenter.shared.show(reason) }
    }

    // MARK: TTS

    /// 句级 TTS 流水线：流式正文边到边切句合成，按序播放。
    /// 与 PWA useVoiceChat 的切句/排队规则逐项一致（见 design.md C2）。
    private func beginSpeechStream(baseURL: String, token: String) {
        stopSpeaking()
        // 本轮发言已结束：证据与探针统计不能留到下一句
        // （近场那句被 ASR 丢掉时，不会有 sentence 来消费证据）
        resetNearFieldEvidence()
        lastSentenceAt = Date()
        speakEpoch += 1
        speechFrozen = []
        speechTail = ""
        speechConsumed = 0
        speechTasks = [:]
        speechSentences = []
        speechIndex = 0
        speechEnded = false
        speechNeedsPlaceholder = false
        isPreparingSpeech = true
        startSpeechPump(epoch: speakEpoch, baseURL: baseURL, token: token)
    }

    /// 流式正文（累积全文）：切出的句子立刻排队合成，尾巴只显示不合成。
    func appendReplyDelta(_ text: String, baseURL: String, token: String) {
        guard !baseURL.isEmpty, !token.isEmpty else { return }
        if speechEnded { beginSpeechStream(baseURL: baseURL, token: token) }
        guard text.count > speechConsumed else { return }
        let (cuts, consumed) = Self.cutSentences(text, from: speechConsumed, first: speechFrozen.isEmpty)
        for sentence in cuts {
            freezeSentence(sentence, baseURL: baseURL, token: token)
        }
        speechConsumed = consumed
        speechTail = String(text.dropFirst(consumed))
        speechSentences = speechTail.isEmpty ? speechFrozen : speechFrozen + [speechTail]
        if !cuts.isEmpty { wakeSpeechPump() }
    }

    /// 本轮回复结束：尾巴整句合成，播完即收。
    func endReplyStream(_ text: String, baseURL: String, token: String) {
        if speechEnded {
            // 极端情况：一条 delta 回调都没走（正常流式不会），收尾时整段补上
            guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty else { return }
            beginSpeechStream(baseURL: baseURL, token: token)
        }
        if !text.isEmpty { appendReplyDelta(text, baseURL: baseURL, token: token) }
        if !speechTail.isEmpty {
            freezeSentence(speechTail, baseURL: baseURL, token: token)
            speechTail = ""
            speechSentences = speechFrozen
        }
        // 纯代码/表格：整段都念不出来时，收尾补一句提示（沿用一次性朗读的行为）
        speechNeedsPlaceholder = speechFrozen.allSatisfy { Self.sanitizeForSpeech($0).isEmpty }
        speechEnded = true
        wakeSpeechPump()
    }

    private func freezeSentence(_ sentence: String, baseURL: String, token: String) {
        let index = speechFrozen.count
        speechFrozen.append(sentence)
        let speech = Self.sanitizeForSpeech(sentence)
        guard !speech.isEmpty else {
            speechTasks[index] = Task { Optional<Data>.none }
            return
        }
        // 每次合成时读：设置窗与聊天窗可能同时开着，切换后下一句立即生效。
        let speaker = UserDefaults.standard.string(forKey: VoicePreference.speakerKey) ?? ""
        speechTasks[index] = Task {
            do {
                return try await APIClient(baseURL: baseURL, token: token).synthesizeSpeech(text: speech, speaker: speaker)
            } catch {
                LogBuffer.shared.error("voice tts sentence failed: \(error.localizedDescription)")
                return nil
            }
        }
    }

    private func startSpeechPump(epoch: Int, baseURL: String, token: String) {
        let (stream, continuation) = AsyncStream<Void>.makeStream(bufferingPolicy: .bufferingNewest(1))
        wakeCont = continuation
        speechPump?.cancel()
        speechPump = Task { [weak self] in
            guard let self else { return }
            var iterator = stream.makeAsyncIterator()
            var index = 0
            var didPlay = false
            while true {
                if epoch != self.speakEpoch { return }
                if index < self.speechFrozen.count {
                    let data = await self.speechTasks[index]?.value
                    if epoch != self.speakEpoch { return }
                    self.speechIndex = index
                    if let data, !data.isEmpty {
                        if !didPlay {
                            didPlay = true
                            self.prepareForPlayback()
                        }
                        self.isPreparingSpeech = false
                        self.isSpeaking = true
                        do {
                            try await self.playAndWait(data)
                        } catch {
                            self.vlog("tts play failed: \(error.localizedDescription)")
                        }
                        if epoch != self.speakEpoch { return }
                    }
                    index += 1
                    continue
                }
                if self.speechEnded { break }
                _ = await iterator.next()
            }
            let placeholderSpeaker = UserDefaults.standard.string(forKey: VoicePreference.speakerKey) ?? ""
            if !didPlay, self.speechNeedsPlaceholder,
               let data = try? await APIClient(baseURL: baseURL, token: token)
                .synthesizeSpeech(text: "这条回复主要是代码或表格，我放在屏幕上了。", speaker: placeholderSpeaker) {
                if epoch != self.speakEpoch { return }
                self.prepareForPlayback()
                self.isPreparingSpeech = false
                self.isSpeaking = true
                try? await self.playAndWait(data)
            }
            guard epoch == self.speakEpoch else { return }
            self.speechTasks = [:]
            self.wakeCont?.finish()
            self.wakeCont = nil
            self.speechPump = nil
            self.isPreparingSpeech = false
            self.speechSentences = []
            self.speechIndex = 0
            self.finishSpeaking()
        }
    }

    private func prepareForPlayback() {
        #if os(iOS)
        // AEC 场景必须用 .voiceChat；.spokenAudio 下系统不会给回声参考
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .voiceChat,
                                                         options: [.defaultToSpeaker, .allowBluetooth])
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif
        configureAudio()
        // 播报期间压住麦克风（AEC 只削掉大部分回声，尾音还会漏进来）
        beginMicGate()
    }

    private func playAndWait(_ data: Data) async throws {
        try await withCheckedThrowingContinuation { (continuation: CheckedContinuation<Void, Error>) in
            playContinuation = continuation
            do {
                try audio.play(data) { [weak self] in
                    Task { @MainActor [weak self] in self?.resumePlayback() }
                }
            } catch {
                playContinuation = nil
                continuation.resume(throwing: error)
            }
        }
    }

    private func resumePlayback() {
        let continuation = playContinuation
        playContinuation = nil
        continuation?.resume()
    }

    private func wakeSpeechPump() {
        wakeCont?.yield(())
    }

    /// 句末标点处切；第一句额外在 12 字后的逗号处切开，让首包音频尽早响。
    private static func cutSentences(_ text: String, from consumed: Int,
                                     first: Bool) -> ([String], Int) {
        let chars = Array(text)
        guard consumed < chars.count else { return ([], consumed) }
        var cuts: [String] = []
        var start = consumed
        var allowClause = first
        var i = consumed
        while i < chars.count {
            let ch = chars[i]
            let length = i - start + 1
            if Self.sentenceEnd.contains(ch) || (allowClause && length >= 12 && Self.clauseEnd.contains(ch)) {
                cuts.append(String(chars[start...i]))
                start = i + 1
                allowClause = false
            }
            i += 1
        }
        return (cuts, start)
    }

    private static let sentenceEnd: Set<Character> = ["。", "！", "？", "!", "?", "；", ";", "…", "\n"]
    private static let clauseEnd: Set<Character> = ["，", ",", "、"]

    /// 播报正常结束：解除「说」态，再延后 600ms 解除麦克风门（收掉尾音）。
    private func finishSpeaking() {
        vlog("tts play finished")
        isSpeaking = false
        speakEndTask?.cancel()
        speakEndTask = Task { [weak self] in
            try? await Task.sleep(for: .milliseconds(600))
            guard !Task.isCancelled, let self, !self.isSpeaking else { return }
            self.endMicGate(flushPreroll: false)
        }
    }

    private func beginMicGate() {
        gateTask?.cancel()
        gateTask = nil
        micGated = true
        levelWindow.removeAll(keepingCapacity: true)
        gateStartedAt = Date()
        vlog("mic gate on (tts speaking)")
        audio.setGated(true)
    }

    private func endMicGate(flushPreroll: Bool) {
        gateTask?.cancel()
        gateTask = nil
        micGated = false
        vlog("mic gate off flush=\(flushPreroll)")
        audio.setGated(false, flushPreroll: flushPreroll)
    }

    func stopSpeaking() {
        speakEpoch += 1
        speechEnded = true
        speechPump?.cancel()
        speechPump = nil
        wakeCont?.finish()
        wakeCont = nil
        speechTasks.values.forEach { $0.cancel() }
        speechTasks = [:]
        speechFrozen = []
        speechTail = ""
        speechConsumed = 0
        speechNeedsPlaceholder = false
        speechSentences = []
        speechIndex = 0
        speakEndTask?.cancel()
        speakEndTask = nil
        let wasSpeaking = isSpeaking
        isSpeaking = false
        isPreparingSpeech = false
        audio.stopPlayback()
        resumePlayback()
        // 只要门还关着就开：仅在 wasSpeaking 时开会让「TTS 尾音门 600ms 窗口内启动新一轮」
        // 永久卡在门控里（一帧都不会发给 ASR，表现为麦克风亮着但毫无反应）
        if wasSpeaking || micGated { endMicGate(flushPreroll: false) }
    }

    // MARK: 内部

    private func sendAudio(_ data: Data) {
        // 长按说话：本地累积，松手才建连整段灌入
        if holdCapturing {
            holdPCM.append(data)
            if holdPCM.count >= 16000 * 2 * holdTalkMaxSeconds {
                ToastCenter.shared.show("已到 60 秒，自动发送")
                Task { await self.finishHoldTalk(cancelled: false) }
            }
            return
        }
        // 只接受 recording：stop() 之后的音频帧（含排队中的 Task）一律丢弃，
        // 尾巴由 stop() 通过 sendTailThenEnd 保证排在 end 之前。
        guard state == .recording, let socket else {
            droppedChunks += 1
            return
        }
        sentChunks += 1
        sentBytes += data.count
        if Date().timeIntervalSince(lastSendLog) >= 5 {
            lastSendLog = Date()
            vlog("asr up: chunks=\(sentChunks) bytes=\(sentBytes) dropped=\(droppedChunks)")
        }
        socket.send(.data(data)) { error in
            guard let error else { return }
            Task { @MainActor in
                LogBuffer.shared.error("voice asr send failed: \(error.localizedDescription)")
            }
        }
    }

    private func sendControl(_ type: String) {
        guard let socket else {
            vlog("control \(type) skipped: no socket")
            return
        }
        vlog("control send: \(type)")
        let text = "{\"type\":\"\(type)\"}"
        socket.send(.string(text)) { error in
            guard let error else { return }
            Task { @MainActor in
                LogBuffer.shared.error("voice asr control failed: \(error.localizedDescription)")
            }
        }
    }

    /// 先发尾巴 PCM，在其 send 完成后再发 end，保证负包之前不会再有音频帧。
    private func sendTailThenEnd(_ tail: Data?) {
        guard let socket else { return }
        if let tail, !tail.isEmpty {
            socket.send(.data(tail)) { [weak self] error in
                Task { @MainActor in
                    if let error {
                        LogBuffer.shared.error("voice asr tail send failed: \(error.localizedDescription)")
                    }
                    self?.sendControl("end")
                }
            }
        } else {
            sendControl("end")
        }
    }

    private func startReceiveLoop(_ task: URLSessionWebSocketTask) {
        receiveTask?.cancel()
        receiveTask = Task { @MainActor [weak self] in
            while !Task.isCancelled {
                do {
                    let message = try await task.receive()
                    guard let self else { return }
                    if case .string(let text) = message {
                        self.handleSocketText(text)
                    }
                } catch {
                    guard let self else { return }
                    LogBuffer.shared.diag("[voice] ws receive ended: \(error.localizedDescription) " +
                                          "state=\(self.state) active=\(self.continuousActive)")
                    if self.continuousActive {
                        // 用户停止/到上限时 state 已是 .finishing（正常收尾）；其余算掉线
                        self.endContinuous(reason: self.state == .finishing ? nil : "语音会话已结束：连接断开")
                    } else if self.state != .idle {
                        self.fail("语音识别连接断开：\(error.localizedDescription)")
                    }
                    return
                }
            }
        }
    }

    private func handleSocketText(_ text: String) {
        guard let data = text.data(using: .utf8),
              let msg = try? JSONDecoder().decode(VoiceSocketMessage.self, from: data) else {
            vlog("ws frame undecodable: \(String(text.prefix(80)))")
            return
        }
        switch msg.type {
        case "ready":
            vlog("ws ready: server connected to ASR")
        case "sentence":
            vlog("ws sentence: chars=\(msg.text?.count ?? 0) state=\(state) active=\(continuousActive)")
            // 证据取用即清零：留着会让上一句的勾放行下一句纯噪声
            let heardNearField = nearFieldHeard
            nearFieldHeard = false
            lastSentenceAt = Date()
            // 只在还处于 .recording 时提交：结束/到上限后的残句不提交、不朗读
            guard continuousActive, state == .recording,
                  let text = msg.text?.trimmingCharacters(in: .whitespacesAndNewlines),
                  !text.isEmpty else {
                vlog("sentence dropped: active=\(continuousActive) state=\(state)")
                return
            }
            logNearFieldProbe(chars: text.count)
            // 近场闸：远处（隔壁房间）的话不提交成一轮。本次发言期间任意时刻出现过近处人声
            // 也算通过 —— 只看到达那一刻的窗口会误杀被背景噪声拖后的句子（人声早已滚出
            // 2 秒窗，见 docs/bugs/058）。
            guard heardNearField || NearFieldGate.shouldAccept(frames: levelHistory,
                                                               sensitivity: nearFieldSensitivity) else {
                let ungated = levelHistory.filter { !$0.gated }
                vlog("sentence dropped: near-field gate frames=\(ungated.count) " +
                     "median=\(fmt3(NearFieldGate.median(ungated.map(\.level))))")
                notifyNearFieldIgnored()
                return
            }
            armIdleTimer()
            if isHolding || heldAwaitingTail {
                heldSentences.append(text)
                if heldAwaitingTail { armHeldTail() }
                vlog("sentence held: chars=\(text.count) holding=\(isHolding) awaitTail=\(heldAwaitingTail)")
                return
            }
            onSentence?(text)
        case "result":
            vlog("ws result: chars=\(msg.text?.count ?? 0) definite=\(msg.definite == true) final=\(msg.final == true)")
            if let value = msg.text { transcript = value }
            if msg.final == true { finishTurn() }
        case "voice":
            // 服务端听到非空识别结果（含未判停的 partial）才发这个：只有近处的人声才顺延空闲收口。
            // 远处噪声（隔壁说话/嗑瓜子/收拾桌子）也会被 ASR 识别成文本，不能给它续命。
            if NearFieldGate.shouldKeepAlive(frames: levelHistory, sensitivity: nearFieldSensitivity) {
                lastVoiceAt = Date()
            }
            // 松手后 partial 还在变，说明最后半句没吐完：重置 1.5s 尾巴窗口
            if heldAwaitingTail { armHeldTail() }
        case "limit":
            vlog("ws limit: \(msg.message ?? "-")")
            guard state == .recording || state == .finishing else { return }
            // 服务端到达上限后会 Finish：立刻停采集（负包之后不能再发音频），等它吐完最后一句
            if state == .recording {
                state = .finishing
                _ = detachCapture()
            }
            armFinishTimeout()
            ToastCenter.shared.show(msg.message ?? "录音达到上限，已按当前内容识别")
        case "error":
            vlog("ws error: \(msg.error ?? "-")")
            // 已点结束/到上限属收尾阶段：这时补发的错误帧（如 45000081 等包超时）不再打扰用户
            guard state == .recording else { return }
            fail(msg.error ?? "语音识别失败")
        default:
            vlog("ws unknown frame: \(msg.type)")
        }
    }

    private func finishTurn() {
        timeoutTask?.cancel()
        timeoutTask = nil
        _ = detachCapture()
        closeSocket()
        state = .idle
        let text = transcript.trimmingCharacters(in: .whitespacesAndNewlines)
        vlog("turn finished: chars=\(text.count)")
        if text.isEmpty {
            ToastCenter.shared.show("没有识别到内容")
            return
        }
        onFinalTranscript?(text)
    }

    private func fail(_ message: String) {
        vlog("fail: \(message) state=\(state) active=\(continuousActive)")
        isSwitchingMode = false
        if continuousActive {
            endContinuous(reason: message)
            return
        }
        generation += 1
        cancelTimers()
        _ = detachCapture()
        closeSocket()
        state = .idle
        ToastCenter.shared.show(message)
    }

    private func closeSocket() {
        maxTimer?.cancel()
        maxTimer = nil
        receiveTask?.cancel()
        receiveTask = nil
        socket?.cancel(with: .normalClosure, reason: nil)
        socket = nil
    }

    private func websocketURL(baseURL: String, ticket: String, continuous: Bool) -> URL? {
        guard var comps = URLComponents(string: baseURL) else { return nil }
        comps.scheme = comps.scheme == "https" ? "wss" : "ws"
        var path = comps.path
        if path.hasSuffix("/") { path.removeLast() }
        comps.path = path + "/api/voice/asr"
        var items = [URLQueryItem(name: "ticket", value: ticket)]
        if continuous { items.append(URLQueryItem(name: "mode", value: "continuous")) }
        comps.queryItems = items
        return comps.url
    }

    private func ensureMicrophonePermission() async -> Bool {
        #if os(macOS)
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized:
            return true
        case .notDetermined:
            return await withCheckedContinuation { cont in
                AVCaptureDevice.requestAccess(for: .audio) { granted in
                    cont.resume(returning: granted)
                }
            }
        default:
            return false
        }
        #else
        let session = AVAudioSession.sharedInstance()
        switch session.recordPermission {
        case .granted:
            return true
        case .undetermined:
            return await withCheckedContinuation { cont in
                session.requestRecordPermission { granted in
                    cont.resume(returning: granted)
                }
            }
        default:
            return false
        }
        #endif
    }

    /// 去掉 markdown/代码块后再朗读；纯代码/纯表格回复会被清空（改为播放一句提示）。
    static func sanitizeForSpeech(_ text: String) -> String {
        if isTableOnly(text) { return "" }
        var s = text
        s = s.replacingOccurrences(of: "```[\\s\\S]*?```", with: " ", options: .regularExpression)
        s = s.replacingOccurrences(of: "`[^`]*`", with: " ", options: .regularExpression)
        s = s.replacingOccurrences(of: "!?\\[([^\\]]*)\\]\\([^)]*\\)", with: "$1", options: .regularExpression)
        s = s.replacingOccurrences(of: "(?m)^\\s{0,3}#{1,6}\\s*", with: "", options: .regularExpression)
        s = s.replacingOccurrences(of: "(?m)^\\s*[-*+]\\s+", with: "", options: .regularExpression)
        s = s.replacingOccurrences(of: "(?m)^\\s*\\d+\\.\\s+", with: "", options: .regularExpression)
        s = s.replacingOccurrences(of: "[*_>#|]", with: "", options: .regularExpression)
        s = s.replacingOccurrences(of: "\\s+", with: " ", options: .regularExpression)
        s = s.trimmingCharacters(in: .whitespacesAndNewlines)
        guard s.count >= 2 else { return "" }
        return String(s.prefix(2000))
    }

    /// 纯表格（每行都是 |...|）逐格念出来没意义，当占位处理。
    static func isTableOnly(_ text: String) -> Bool {
        let lines = text.split(separator: "\n")
            .map { $0.trimmingCharacters(in: .whitespacesAndNewlines) }
            .filter { !$0.isEmpty }
        guard lines.count >= 2 else { return false }
        return lines.allSatisfy { $0.hasPrefix("|") && $0.hasSuffix("|") }
    }
}

private struct VoiceSocketMessage: Decodable {
    let type: String
    let text: String?
    let definite: Bool?
    let final: Bool?
    let error: String?
    let message: String?
}
