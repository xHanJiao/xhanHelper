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
    private var player: AVAudioPlayer?

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
            isAvailable = cfg.enabled && cfg.ready
            speaker = cfg.speaker
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

        state = .requestingPermission
        guard await ensureMicrophonePermission() else {
            fail("没有麦克风权限，请在系统设置中允许 xhanHelper 使用麦克风")
            return
        }
        #if os(iOS)
        try? AVAudioSession.sharedInstance().setCategory(.playAndRecord, mode: .spokenAudio,
                                                         options: [.defaultToSpeaker, .allowBluetooth])
        try? AVAudioSession.sharedInstance().setActive(true)
        #endif

        do {
            let api = APIClient(baseURL: baseURL, token: token)
            let ticket = try await api.createVoiceASRTicket().ticket
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
            try capture.start()
            self.capture = capture
            state = .recording
        } catch {
            fail("无法开始语音识别：\(error.localizedDescription)")
        }
    }

    func stop() {
        guard state == .recording else { return }
        state = .finishing
        capture?.stop()
        capture = nil
        sendControl("end")
        timeoutTask?.cancel()
        timeoutTask = Task { [weak self] in
            try? await Task.sleep(for: .seconds(15))
            guard !Task.isCancelled, let self, self.state == .finishing else { return }
            self.fail("语音识别超时")
        }
    }

    func cancel() {
        capture?.stop()
        capture = nil
        sendControl("cancel")
        closeSocket()
        timeoutTask?.cancel()
        timeoutTask = nil
        state = .idle
    }

    // MARK: TTS

    /// 朗读一段回复；空文本或纯代码/表格会跳过。
    func speak(text: String, baseURL: String, token: String) async {
        let cleaned = Self.sanitizeForSpeech(text)
        guard !cleaned.isEmpty, !baseURL.isEmpty, !token.isEmpty else { return }
        stopSpeaking()
        do {
            let data = try await APIClient(baseURL: baseURL, token: token).synthesizeSpeech(text: cleaned)
            #if os(iOS)
            try? AVAudioSession.sharedInstance().setCategory(.playback, mode: .spokenAudio)
            try? AVAudioSession.sharedInstance().setActive(true)
            #endif
            let player = try AVAudioPlayer(data: data)
            player.prepareToPlay()
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
        player?.stop()
        player = nil
    }

    // MARK: 内部

    private func sendAudio(_ data: Data) {
        // finishing 时也要放行：stop() 会先停采集、把最后不到 200ms 的尾巴冲出来，再发 end
        guard state == .recording || state == .finishing else { return }
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
        case "ready":
            break
        case "result":
            if let value = msg.text { transcript = value }
            if msg.final == true { finishTurn() }
        case "error":
            fail(msg.error ?? "语音识别失败")
        default:
            break
        }
    }

    private func finishTurn() {
        timeoutTask?.cancel()
        timeoutTask = nil
        capture?.stop()
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
        timeoutTask?.cancel()
        timeoutTask = nil
        capture?.stop()
        capture = nil
        closeSocket()
        state = .idle
        ToastCenter.shared.show(message)
    }

    private func closeSocket() {
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

    /// 去掉 markdown/代码块后再朗读；纯代码/表格回复会被清空（跳过朗读）。
    static func sanitizeForSpeech(_ text: String) -> String {
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
}

private struct VoiceSocketMessage: Decodable {
    let type: String
    let text: String?
    let final: Bool?
    let error: String?
}
