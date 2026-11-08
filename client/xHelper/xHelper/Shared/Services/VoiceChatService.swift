import AVFoundation
import Combine
import Foundation

/// 语音聊天客户端：麦克风采集 → 自家后端 WebSocket → 豆包流式 ASR；
/// 回复文本 → 自家后端 TTS → AVAudioPlayer 播放。
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

    private var socket: URLSessionWebSocketTask?
    private var capture: VoiceAudioCapture?
    private var receiveTask: Task<Void, Never>?
    private var timeoutTask: Task<Void, Never>?
    private var maxTimer: Task<Void, Never>?
    private var player: AVAudioPlayer?
    /// start/cancel 竞态代次：cancel 或新 start 会让旧 start 的续体失效
    private var generation = 0
    /// TTS 请求代次：stopSpeaking 后迟到的合成结果不再播放
    private var speakEpoch = 0
    private var maxSeconds = 60

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
        } catch {
            // 未配置/网络失败时静默隐藏按钮，不影响文字聊天
            isAvailable = false
            LogBuffer.shared.info("voice config unavailable: \(error.localizedDescription)")
        }
    }

    // MARK: 录音

    func start(baseURL: String, token: String) async {
        guard state == .idle else { return }
        guard !baseURL.isEmpty, !token.isEmpty else { return }

        generation += 1
        let gen = generation
        state = .requestingPermission
        guard await ensureMicrophonePermission() else {
            if gen == generation { fail("没有麦克风权限，请在系统设置中允许 xhanHelper 使用麦克风") }
            return
        }
        guard gen == generation, state == .requestingPermission else { return }
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .spokenAudio,
                                                         options: [.defaultToSpeaker, .allowBluetooth])
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif

        do {
            let api = APIClient(baseURL: baseURL, token: token)
            let ticket = try await api.createVoiceASRTicket().ticket
            guard gen == generation, state == .requestingPermission else { return }
            guard let url = websocketURL(baseURL: baseURL, ticket: ticket) else {
                fail("服务地址无效")
                return
            }
            stopSpeaking()
            transcript = ""
            let task = URLSession.shared.webSocketTask(with: url)
            socket = task
            task.resume()
            startReceiveLoop(task)

            let capture = VoiceAudioCapture()
            capture.onChunk = { [weak self] data in
                Task { @MainActor [weak self] in
                    self?.sendAudio(data)
                }
            }
            capture.onError = { [weak self] message in
                Task { @MainActor [weak self] in
                    self?.fail("麦克风采集失败：\(message)")
                }
            }
            self.capture = capture
            // 立刻开录：服务端要先连豆包（几百毫秒）才会 ready，等它会把开头吞掉；
            // 早于 ready 的帧由 socket 缓冲，服务端连上后照常读。
            startCapture()
        } catch {
            if gen == generation { fail("无法开始语音识别：\(error.localizedDescription)") }
        }
    }

    /// 开始推 PCM。
    private func startCapture() {
        guard state == .requestingPermission, let capture else { return }
        do {
            try capture.start()
            state = .recording
            maxTimer?.cancel()
            maxTimer = Task { [weak self] in
                guard let self else { return }
                let seconds = max(1, self.maxSeconds - 2)
                try? await Task.sleep(for: .seconds(seconds))
                guard !Task.isCancelled, self.state == .recording else { return }
                self.stop()
            }
        } catch {
            fail("麦克风采集失败：\(error.localizedDescription)")
        }
    }

    func stop() {
        guard state == .recording else { return }
        state = .finishing
        maxTimer?.cancel()
        maxTimer = nil
        let tail = capture?.stop()
        capture = nil
        sendTailThenEnd(tail)
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
        generation += 1
        _ = capture?.stop()
        capture = nil
        sendControl("cancel")
        closeSocket()
        timeoutTask?.cancel()
        timeoutTask = nil
        state = .idle
    }

    // MARK: TTS

    /// 朗读一段回复；纯代码/表格回复改播一句提示。
    func speak(text: String, baseURL: String, token: String) async {
        guard !text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty,
              !baseURL.isEmpty, !token.isEmpty else { return }
        let cleaned = Self.sanitizeForSpeech(text)
        let speech = cleaned.isEmpty ? "这条回复主要是代码或表格，我放在屏幕上了。" : cleaned
        stopSpeaking()
        speakEpoch += 1
        let epoch = speakEpoch
        do {
            let data = try await APIClient(baseURL: baseURL, token: token).synthesizeSpeech(text: speech)
            guard epoch == speakEpoch else { return }
            #if os(iOS)
            try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .spokenAudio)
            try? AVAudioSession.sharedInstance().setActive(true)
            #endif
            let player = try AVAudioPlayer(data: data)
            player.prepareToPlay()
            guard epoch == speakEpoch else { return }
            guard player.play() else {
                fail("语音播放失败")
                return
            }
            self.player = player
        } catch {
            LogBuffer.shared.error("voice tts failed: \(error.localizedDescription)")
            ToastCenter.shared.show("语音播放失败")
        }
    }

    func stopSpeaking() {
        speakEpoch += 1
        player?.stop()
        player = nil
    }

    // MARK: 内部

    private func sendAudio(_ data: Data) {
        // 只接受 recording：stop() 之后的音频帧（含排队中的 Task）一律丢弃，
        // 尾巴由 stop() 通过 sendTailThenEnd 保证排在 end 之前。
        guard state == .recording else { return }
        socket?.send(.data(data)) { error in
            guard let error else { return }
            Task { @MainActor in
                LogBuffer.shared.error("voice asr send failed: \(error.localizedDescription)")
            }
        }
    }

    private func sendControl(_ type: String) {
        guard let socket else { return }
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
                    if self.state != .idle {
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
            return
        }
        switch msg.type {
        case "result":
            if let value = msg.text { transcript = value }
            if msg.final == true { finishTurn() }
        case "limit":
            // 服务端到达录音上限后会 Finish；这里停止采集，等 final 回来
            if state == .recording {
                state = .finishing
                maxTimer?.cancel()
                maxTimer = nil
                _ = capture?.stop()
                capture = nil
            }
            armFinishTimeout()
            ToastCenter.shared.show(msg.message ?? "录音达到上限，已按当前内容识别")
        case "error":
            fail(msg.error ?? "语音识别失败")
        default:
            break
        }
    }

    private func finishTurn() {
        timeoutTask?.cancel()
        timeoutTask = nil
        _ = capture?.stop()
        capture = nil
        closeSocket()
        state = .idle
        let text = transcript.trimmingCharacters(in: .whitespacesAndNewlines)
        if text.isEmpty {
            ToastCenter.shared.show("没有识别到内容")
            return
        }
        onFinalTranscript?(text)
    }

    private func fail(_ message: String) {
        generation += 1
        timeoutTask?.cancel()
        timeoutTask = nil
        _ = capture?.stop()
        capture = nil
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

    private func websocketURL(baseURL: String, ticket: String) -> URL? {
        guard var comps = URLComponents(string: baseURL) else { return nil }
        comps.scheme = comps.scheme == "https" ? "wss" : "ws"
        var path = comps.path
        if path.hasSuffix("/") { path.removeLast() }
        comps.path = path + "/api/voice/asr"
        comps.queryItems = [URLQueryItem(name: "ticket", value: ticket)]
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
            .map { $0.trimmingCharacters(in: .whitespaces) }
            .filter { !$0.isEmpty }
        guard lines.count >= 2 else { return false }
        return lines.allSatisfy { $0.hasPrefix("|") && $0.hasSuffix("|") }
    }
}

private struct VoiceSocketMessage: Decodable {
    let type: String
    let text: String?
    let final: Bool?
    let error: String?
    let message: String?
}
