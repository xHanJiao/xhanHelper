import AVFoundation
import Combine
import Foundation

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
    @Published private(set) var continuousActive = false
    /// 麦克风音量 0...1（全屏语音环用）
    @Published private(set) var level: Double = 0
    /// TTS 正在播放（全屏语音环「说」态）
    @Published private(set) var isSpeaking = false
    /// 空闲多久没听到完整分句就结束会话（秒）
    private let continuousIdleSeconds = 30
    private var speakEndTask: Task<Void, Never>?
    private var gateTask: Task<Void, Never>?
    /// 麦克风门是否生效（播报期间 + 播报结束后的 600ms 尾音窗口）
    private var micGated = false
    /// 打断判定：连续 3 帧（tap 3200 帧 @48k，约 200ms）超过阈值才算真的开口
    private var loudFrames = 0
    private let bargeInFrames = 3
    /// 打断判定的绝对地板（`onLevel` 是 RMS×4 的刻度）：真静音时的数字噪声不该触发
    private let bargeInFloor: Double = 0.03
    /// 用户要明显高于「残响本底」才算开口。远场（0.5~1m）说话只有 0.04~0.08，钉死绝对
    /// 阈值就永远打不断（051 的教训），所以阈值跟着本机残响走：漏音大的音箱自己抬阈值。
    private let bargeInRatio: Double = 2.2
    /// 播报期间门控电平的最近若干帧：p20 当残响本底，短暂人声抬不动它
    private var levelWindow: [Double] = []
    private let levelWindowSize = 30
    /// 门刚打开的一小段先不听：播放起始的爆音不算打断
    private var gateStartedAt = Date.distantPast
    /// 最近一次确认用户在说话的时间（服务端 `voice` 事件 / 新分句）：空闲收口看它，
    /// 别在人家说话中途把会话收掉
    private var lastVoiceAt = Date()

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
            return
        }
        guard !baseURL.isEmpty, !token.isEmpty else {
            vlog("start ignored: empty baseURL/token")
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
            continuousActive = continuous
            sentBytes = 0
            sentChunks = 0
            droppedChunks = 0
            lastSendLog = .distantPast
            lastLevelLog = .distantPast
            peakLevel = 0
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
        // 平滑一下，避免波纹抖成噪点
        level = level * 0.6 + value * 0.4
        peakLevel = max(peakLevel, value)

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
    func stopContinuous() {
        vlog("stopContinuous requested state=\(state) active=\(continuousActive)")
        guard continuousActive else { return }
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
            self.stopContinuous()
            ToastCenter.shared.show("语音会话已到上限（\(self.sessionSeconds / 60) 分钟），已结束")
        }
    }

    /// 空闲收口：一直没人说话就别占着豆包并发、白扣音频时长。
    private func armIdleTimer() {
        idleTask?.cancel()
        idleTask = Task { [weak self] in
            guard let self else { return }
            try? await Task.sleep(for: .seconds(self.continuousIdleSeconds))
            guard !Task.isCancelled, self.continuousActive else { return }
            // AI 正在回答/朗读、或刚刚还在说话（长句没判停）也算「会话仍在进行」，顺延而不是收口
            if self.isSpeaking || (self.isChatBusy?() ?? false)
                || Date().timeIntervalSince(self.lastVoiceAt) < TimeInterval(self.continuousIdleSeconds) {
                self.armIdleTimer()
                return
            }
            LogBuffer.shared.diag("[voice] idle limit reached (\(self.continuousIdleSeconds)s), stopping")
            self.stopContinuous()
            ToastCenter.shared.show("\(self.continuousIdleSeconds) 秒没有听到说话，语音会话已结束")
        }
    }

    /// 停止采集，返回不足 200ms 的尾巴（调用方负责在 end 之前发出）。
    private func detachCapture() -> Data? {
        micGated = false
        audio.setGated(false)
        let tail = audio.stopCapture()
        level = 0
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
        cancelTimers()
        _ = detachCapture()
        closeSocket()
        state = .idle
        if let reason { ToastCenter.shared.show(reason) }
    }

    // MARK: TTS

    /// 朗读一段回复；纯代码/表格回复改播一句提示。
    func speak(text: String, baseURL: String, token: String) async {
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !baseURL.isEmpty, !token.isEmpty else { return }
        let cleaned = Self.sanitizeForSpeech(text)
        let speech = cleaned.isEmpty ? "这条回复主要是代码或表格，我放在屏幕上了。" : cleaned
        vlog("tts request: reply=\(text.count) chars speech=\(speech.count) chars")
        stopSpeaking()
        speakEpoch += 1
        let epoch = speakEpoch
        do {
            let data = try await APIClient(baseURL: baseURL, token: token).synthesizeSpeech(text: speech)
            vlog("tts ok: bytes=\(data.count)")
            guard epoch == speakEpoch else {
                vlog("tts dropped: epoch \(epoch) != \(speakEpoch)")
                return
            }
            #if os(iOS)
            // AEC 场景必须用 .voiceChat；.spokenAudio 下系统不会给回声参考
            try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .voiceChat,
                                                             options: [.defaultToSpeaker, .allowBluetooth])
            try? AVAudioSession.sharedInstance().setActive(true)
            #endif
            configureAudio()
            // 播报期间压住麦克风（AEC 只削掉大部分回声，尾音还会漏进来）
            beginMicGate()
            isSpeaking = true
            vlog("tts play begin")
            do {
                try audio.play(data) { [weak self] in
                    Task { @MainActor [weak self] in
                        guard let self, epoch == self.speakEpoch else { return }
                        self.finishSpeaking()
                    }
                }
            } catch {
                vlog("tts play failed: \(error.localizedDescription)")
                isSpeaking = false
                endMicGate(flushPreroll: false)
                throw error
            }
        } catch {
            LogBuffer.shared.error("voice tts failed: \(error.localizedDescription)")
            ToastCenter.shared.show("语音播放失败")
        }
    }

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
        speakEndTask?.cancel()
        speakEndTask = nil
        let wasSpeaking = isSpeaking
        isSpeaking = false
        audio.stopPlayback()
        // 只要门还关着就开：仅在 wasSpeaking 时开会让「TTS 尾音门 600ms 窗口内启动新一轮」
        // 永久卡在门控里（一帧都不会发给 ASR，表现为麦克风亮着但毫无反应）
        if wasSpeaking || micGated { endMicGate(flushPreroll: false) }
    }

    // MARK: 内部

    private func sendAudio(_ data: Data) {
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
            // 只在还处于 .recording 时提交：结束/到上限后的残句不提交、不朗读
            guard continuousActive, state == .recording,
                  let text = msg.text?.trimmingCharacters(in: .whitespacesAndNewlines),
                  !text.isEmpty else {
                vlog("sentence dropped: active=\(continuousActive) state=\(state)")
                return
            }
            armIdleTimer()
            onSentence?(text)
        case "result":
            vlog("ws result: chars=\(msg.text?.count ?? 0) definite=\(msg.definite == true) final=\(msg.final == true)")
            if let value = msg.text { transcript = value }
            if msg.final == true { finishTurn() }
        case "voice":
            // 服务端听到非空识别结果（含未判停的 partial）才发这个：空闲收口据此顺延。
            // 不用麦克风电平猜——环境噪声会一直续命，远场长句又会被误收。
            lastVoiceAt = Date()
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
