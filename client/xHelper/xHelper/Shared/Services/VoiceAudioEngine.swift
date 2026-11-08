import AVFoundation
import Foundation

/// 采集与播放共用一个 AVAudioEngine（C3）。
///
/// VPIO 只有在「播放也走同一个 engine」时才有回声参考信号，所以 TTS 从 `AVAudioPlayer`
/// 搬到了挂在这个 engine 上的 `AVAudioPlayerNode`。初始化顺序是硬要求：
/// 接播放图 → 开 VPIO（input + output）→ 装采集 tap → start；顺序反了 AEC 会静默失效。
///
/// 麦克风门：播放期间不把真实音频发给 ASR（否则会把自己念的再识别一遍），只保留最近 600ms
/// 到 pre-roll；但上行流不能空闲——豆包 8 秒收不到音频包会断会话（45000081），所以门控期间
/// 按原节拍补等长静音保活。检测到用户打断时把 pre-roll 冲回去，开头不会丢。
final class VoiceAudioEngine: @unchecked Sendable {
    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private let lock = NSLock()

    private var pending = Data()
    private var preroll = Data()
    private let prerollLimit = 19_200 // 600ms @ 16k/16bit/mono
    private var converter: AVAudioConverter?
    private var outFormat: AVAudioFormat?
    /// 采集源（可能是 VPIO 的多声道）先混成单声道的中间格式
    private var monoFormat: AVAudioFormat?
    private var graphReady = false
    private var capturing = false
    private var gated = false
    /// TTS 是否在播。只在主线程读写（play / completion / stopPlayback / stopCapture）。
    /// 不能用 `player.isPlaying` 判断：buffer 播完后它仍是 true，engine 会永远不停，
    /// 麦克风占用也就一直不退（菜单栏一直显示麦克风模式）。
    private var playbackActive = false
    private let playbackFormat = AVAudioFormat(standardFormatWithSampleRate: 24_000, channels: 1)!

    /// 每约 200ms 的 PCM 分片（6400 字节）；门控期间发的是等长静音。
    var onChunk: (@Sendable (Data) -> Void)?
    /// 每块音频的音量 0...1（门控期间仍触发，打断检测靠它）。
    var onLevel: (@Sendable (Float) -> Void)?
    var onError: (@Sendable (String) -> Void)?
    /// 诊断日志（音频线程触发，调用方自行切主线程）。
    var onLog: (@Sendable (String) -> Void)?

    // 诊断计数（只在音频线程/启动停止时读写，仅用于日志）
    private var bufferCount = 0
    private var framesOut = 0
    private var chunkCount = 0
    private var emptyOutCount = 0
    private var lastStatLog = Date.distantPast

    func startCapture() throws {
        try ensureGraph()
        guard !capturing else {
            log("capture start ignored: already capturing")
            return
        }

        let input = engine.inputNode
        let inputFormat = input.inputFormat(forBus: 0)
        log("capture start: in=\(fmt(inputFormat)) tapFormat=\(fmt(input.outputFormat(forBus: 0))) " +
            "vpio=\(input.isVoiceProcessingEnabled) engineRunning=\(engine.isRunning)")
        guard inputFormat.sampleRate > 0, inputFormat.channelCount > 0 else {
            throw NSError(domain: "VoiceAudioEngine", code: -1,
                userInfo: [NSLocalizedDescriptionKey: "没有可用的麦克风输入"])
        }
        // VPIO 打开后输入会变成多声道（实测 5ch）；AVAudioConverter 直接 5ch→mono 会静默产出
        // 全零样本（识别永远为空），所以先按原始采样率混成 mono，再做 16k/16bit 转换。
        guard let monoFormat = AVAudioFormat(commonFormat: .pcmFormatFloat32,
                                             sampleRate: inputFormat.sampleRate,
                                             channels: 1,
                                             interleaved: false),
              let outFormat = AVAudioFormat(commonFormat: .pcmFormatInt16,
                                            sampleRate: 16000,
                                            channels: 1,
                                            interleaved: true),
              let converter = AVAudioConverter(from: monoFormat, to: outFormat) else {
            throw NSError(domain: "VoiceAudioEngine", code: -2,
                userInfo: [NSLocalizedDescriptionKey: "无法创建音频转换器"])
        }
        lock.lock()
        self.converter = converter
        self.outFormat = outFormat
        self.monoFormat = monoFormat
        pending = Data()
        preroll = Data()
        gated = false
        lock.unlock()
        bufferCount = 0
        framesOut = 0
        chunkCount = 0
        emptyOutCount = 0
        lastStatLog = .distantPast

        input.installTap(onBus: 0, bufferSize: 3200, format: inputFormat) { [weak self] buffer, _ in
            self?.handle(buffer)
        }
        do {
            try startEngineIfNeeded()
        } catch {
            input.removeTap(onBus: 0)
            log("capture start failed: \(error.localizedDescription)")
            throw error
        }
        capturing = true
        log("capture started: engineRunning=\(engine.isRunning) tapFormat=\(fmt(input.inputFormat(forBus: 0)))")
        // 看门狗：2 秒内 tap 一次都没回调 = 麦克风/引擎没出帧（VPIO 配置失败最常见）
        DispatchQueue.main.asyncAfter(deadline: .now() + 2) { [weak self] in
            guard let self, self.capturing, self.bufferCount == 0 else { return }
            self.log("capture watchdog: 2s 内 tap 未回调任何 buffer（没有音频帧进来）")
        }
    }

    /// 停止采集，返回不足 200ms 的尾巴；调用方负责在发 end 之前把它发出去。
    @discardableResult
    func stopCapture() -> Data? {
        guard capturing else { return nil }
        capturing = false
        engine.inputNode.removeTap(onBus: 0)
        lock.lock()
        let tail = pending
        pending = Data()
        preroll = Data()
        lock.unlock()
        stopEngineIfIdle()
        log("capture stop: buffers=\(bufferCount) frames_out=\(framesOut) chunks=\(chunkCount) " +
            "tail=\(tail.count)B engineRunning=\(engine.isRunning)")
        return tail.isEmpty ? nil : tail
    }

    /// 播放期间（以及播放结束后的一段）门控麦克风。
    /// `flushPreroll=true` 用于打断：把门控期间攒下的真实音频立即冲给 ASR。
    func setGated(_ value: Bool, flushPreroll: Bool = false) {
        lock.lock()
        let changed = gated != value
        gated = value
        var flush: Data?
        let prerollBytes = preroll.count
        if !value {
            if flushPreroll { flush = preroll }
            preroll = Data()
        }
        lock.unlock()
        if changed || flush != nil {
            log("gate=\(value) changed=\(changed) flush=\(flush?.count ?? 0)B preroll=\(prerollBytes)B")
        }
        if let flush, !flush.isEmpty { onChunk?(flush) }
    }

    /// 播放整段 mp3；`completion` 在播放结束（或被 stopPlayback 打断）时回到主线程。
    func play(_ data: Data, completion: @escaping @Sendable () -> Void) throws {
        try ensureGraph()
        let url = FileManager.default.temporaryDirectory
            .appendingPathComponent("voice-tts-\(UUID().uuidString).mp3")
        try data.write(to: url)
        defer { try? FileManager.default.removeItem(at: url) }

        let file = try AVAudioFile(forReading: url)
        let buffer = try Self.readBuffer(file: file, target: playbackFormat)
        try startEngineIfNeeded()
        player.stop()
        playbackActive = true
        player.scheduleBuffer(buffer, at: nil, options: []) { [weak self] in
            Task { @MainActor in
                self?.markPlaybackFinished()
                completion()
            }
        }
        player.play()
        log("play start: mp3=\(data.count)B frames=\(buffer.frameLength) engineRunning=\(engine.isRunning)")
    }

    /// 播完一个 buffer：清播放标志并释放 engine（采集还开着时保持运行）。
    /// 不在这里调 `player.stop()`：stop 会再次触发完成回调，造成递归。
    private func markPlaybackFinished() {
        playbackActive = false
        stopEngineIfIdle()
    }

    func stopPlayback() {
        let wasPlaying = playbackActive
        playbackActive = false
        player.stop()
        stopEngineIfIdle()
        if wasPlaying { log("play stop: engineRunning=\(engine.isRunning)") }
    }

    // MARK: 内部

    private func ensureGraph() throws {
        guard !graphReady else { return }
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: playbackFormat)
        // VPIO 必须在装 tap 之前开，且 input/output 都开
        do {
            try engine.inputNode.setVoiceProcessingEnabled(true)
            log("vpio input enabled")
        } catch {
            throw NSError(domain: "VoiceAudioEngine", code: -3, userInfo: [
                NSLocalizedDescriptionKey: "无法开启回声消除：\(error.localizedDescription)"
            ])
        }
        do {
            try engine.outputNode.setVoiceProcessingEnabled(true)
            log("vpio output enabled")
        } catch {
            // 只有 input 开也能工作，但残留回声会更多；记日志，不让整个语音功能不可用
            log("vpio output failed: \(error.localizedDescription)")
            onError?("输出端回声消除不可用：\(error.localizedDescription)")
        }
        graphReady = true
        log("graph ready: in=\(fmt(engine.inputNode.inputFormat(forBus: 0))) out=\(fmt(engine.outputNode.inputFormat(forBus: 0)))")
    }

    private func startEngineIfNeeded() throws {
        guard !engine.isRunning else { return }
        engine.prepare()
        try engine.start()
        log("engine started: running=\(engine.isRunning) in=\(fmt(engine.inputNode.inputFormat(forBus: 0)))")
    }

    private func stopEngineIfIdle() {
        guard !capturing, !playbackActive, engine.isRunning else { return }
        engine.stop()
        log("engine stopped")
    }

    private func handle(_ buffer: AVAudioPCMBuffer) {
        guard let converter, let outFormat, let monoFormat else { return }
        bufferCount += 1
        let source: AVAudioPCMBuffer
        if buffer.format.channelCount > 1 {
            guard let mono = Self.downmixToMono(buffer, format: monoFormat) else {
                statsLog(level: nil, gated: nil)
                return
            }
            source = mono
        } else {
            source = buffer
        }
        let ratio = outFormat.sampleRate / source.format.sampleRate
        let capacity = AVAudioFrameCount(Double(source.frameLength) * ratio) + 32
        guard let out = AVAudioPCMBuffer(pcmFormat: outFormat, frameCapacity: capacity) else {
            statsLog(level: nil, gated: nil)
            return
        }

        var supplied = false
        var error: NSError?
        let status = converter.convert(to: out, error: &error) { _, outStatus in
            if supplied {
                outStatus.pointee = .noDataNow
                return nil
            }
            supplied = true
            outStatus.pointee = .haveData
            return source
        }
        if status == .error {
            if let error {
                log("convert error: \(error.localizedDescription) (buffers=\(bufferCount))")
                onError?(error.localizedDescription)
            }
            statsLog(level: nil, gated: nil)
            return
        }
        guard out.frameLength > 0, let samples = out.int16ChannelData?[0] else {
            emptyOutCount += 1
            statsLog(level: nil, gated: nil)
            return
        }

        let frameCount = Int(out.frameLength)
        framesOut += frameCount
        let data = Data(bytes: samples, count: frameCount * MemoryLayout<Int16>.size)

        var levelValue: Float = 0
        if let onLevel {
            var sum = 0.0
            for i in 0..<frameCount {
                let v = Double(samples[i]) / 32768
                sum += v * v
            }
            // 说话人的 RMS 通常在 0.02~0.2，×4 让波纹与打断检测都有可用幅度
            let rms = (sum / Double(frameCount)).squareRoot()
            levelValue = Float(min(1, rms * 4))
            onLevel(levelValue)
        }

        lock.lock()
        let isGated = gated
        if gated {
            preroll.append(data)
            if preroll.count > prerollLimit {
                preroll.removeFirst(preroll.count - prerollLimit)
            }
            // 门控期不能断流：豆包 8 秒收不到音频包会主动断开（错误码 45000081），
            // 表现为播报到一半「识别失败」。这里按原节拍补等长静音保活。
            pending.append(Data(count: data.count))
        } else {
            pending.append(data)
        }
        var chunks: [Data] = []
        while pending.count >= 6400 {
            chunks.append(Data(pending.prefix(6400)))
            pending.removeFirst(6400)
        }
        lock.unlock()
        chunkCount += chunks.count
        for chunk in chunks { onChunk?(chunk) }
        statsLog(level: levelValue, gated: isGated)
    }

    /// 多声道（VPIO 输入）混成单声道 float。直接丢给 AVAudioConverter 做 5ch→mono 会得到全零。
    /// shortcut: 逐声道取平均（本机 VPIO 的 5 路是同一路信号）；若某些设备各声道内容不同导致音量偏小，
    /// 改成「取能量最大的那一路」。
    private static func downmixToMono(_ buffer: AVAudioPCMBuffer, format: AVAudioFormat) -> AVAudioPCMBuffer? {
        let frames = Int(buffer.frameLength)
        let channels = Int(buffer.format.channelCount)
        guard frames > 0, channels > 0,
              let out = AVAudioPCMBuffer(pcmFormat: format, frameCapacity: buffer.frameLength),
              let dst = out.floatChannelData?[0] else { return nil }
        out.frameLength = buffer.frameLength
        let scale = 1 / Float(channels)
        if let src = buffer.floatChannelData {
            for i in 0..<frames {
                var sum: Float = 0
                for ch in 0..<channels { sum += src[ch][i] }
                dst[i] = sum * scale
            }
        } else if let src = buffer.int16ChannelData {
            for i in 0..<frames {
                var sum: Float = 0
                for ch in 0..<channels { sum += Float(src[ch][i]) / 32768 }
                dst[i] = sum * scale
            }
        } else {
            return nil
        }
        return out
    }

    /// 每 2 秒一行采集统计：区分「tap 没回调」「转换出 0 帧」「门控把帧吃了」。
    private func statsLog(level: Float?, gated: Bool?) {
        let now = Date()
        guard now.timeIntervalSince(lastStatLog) >= 2 else { return }
        lastStatLog = now
        let levelText = level.map { String(format: "%.3f", $0) } ?? "-"
        let gatedText = gated.map(String.init) ?? "-"
        log("capture stats: buffers=\(bufferCount) frames_out=\(framesOut) chunks=\(chunkCount) " +
            "empty=\(emptyOutCount) level=\(levelText) gated=\(gatedText)")
    }

    private func log(_ message: String) {
        onLog?(message)
    }

    private func fmt(_ format: AVAudioFormat) -> String {
        "\(Int(format.sampleRate))Hz/\(format.channelCount)ch/\(format.isInterleaved ? "interleaved" : "planar")"
    }

    /// 把解码后的音频整段读进内存，必要时重采样到播放格式。
    private static func readBuffer(file: AVAudioFile, target: AVAudioFormat) throws -> AVAudioPCMBuffer {
        let source = file.processingFormat
        guard let input = AVAudioPCMBuffer(pcmFormat: source,
                                           frameCapacity: AVAudioFrameCount(file.length)) else {
            throw NSError(domain: "VoiceAudioEngine", code: -4,
                userInfo: [NSLocalizedDescriptionKey: "无法创建音频缓冲"])
        }
        try file.read(into: input)
        if source == target { return input }

        guard let converter = AVAudioConverter(from: source, to: target) else {
            throw NSError(domain: "VoiceAudioEngine", code: -5,
                userInfo: [NSLocalizedDescriptionKey: "无法转换语音格式"])
        }
        let ratio = target.sampleRate / source.sampleRate
        guard let output = AVAudioPCMBuffer(pcmFormat: target,
                                            frameCapacity: AVAudioFrameCount(Double(input.frameLength) * ratio) + 64) else {
            throw NSError(domain: "VoiceAudioEngine", code: -6,
                userInfo: [NSLocalizedDescriptionKey: "无法创建播放缓冲"])
        }
        var supplied = false
        var error: NSError?
        converter.convert(to: output, error: &error) { _, status in
            if supplied {
                status.pointee = .endOfStream
                return nil
            }
            supplied = true
            status.pointee = .haveData
            return input
        }
        if let error { throw error }
        return output
    }
}
