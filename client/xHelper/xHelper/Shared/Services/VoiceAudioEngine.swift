import AVFoundation
import Foundation

/// 采集与播放共用一个 AVAudioEngine（C3）。
///
/// VPIO 只有在「播放也走同一个 engine」时才有回声参考信号，所以 TTS 从 `AVAudioPlayer`
/// 搬到了挂在这个 engine 上的 `AVAudioPlayerNode`。初始化顺序是硬要求：
/// 接播放图 → 开 VPIO（input + output）→ 装采集 tap → start；顺序反了 AEC 会静默失效。
///
/// 麦克风门：播放期间分片不发给 ASR（否则会把自己念的再识别一遍），只保留最近 600ms
/// 到 pre-roll；检测到用户打断时把 pre-roll 冲回去，开头不会丢。
final class VoiceAudioEngine: @unchecked Sendable {
    private let engine = AVAudioEngine()
    private let player = AVAudioPlayerNode()
    private let lock = NSLock()

    private var pending = Data()
    private var preroll = Data()
    private let prerollLimit = 19_200 // 600ms @ 16k/16bit/mono
    private var converter: AVAudioConverter?
    private var outFormat: AVAudioFormat?
    private var graphReady = false
    private var capturing = false
    private var gated = false
    private let playbackFormat = AVAudioFormat(standardFormatWithSampleRate: 24_000, channels: 1)!

    /// 每约 200ms 的 PCM 分片（6400 字节）；门控期间不触发。
    var onChunk: (@Sendable (Data) -> Void)?
    /// 每块音频的音量 0...1（门控期间仍触发，打断检测靠它）。
    var onLevel: (@Sendable (Float) -> Void)?
    var onError: (@Sendable (String) -> Void)?

    func startCapture() throws {
        try ensureGraph()
        guard !capturing else { return }

        let input = engine.inputNode
        let inputFormat = input.inputFormat(forBus: 0)
        guard inputFormat.sampleRate > 0, inputFormat.channelCount > 0 else {
            throw NSError(domain: "VoiceAudioEngine", code: -1,
                userInfo: [NSLocalizedDescriptionKey: "没有可用的麦克风输入"])
        }
        guard let outFormat = AVAudioFormat(commonFormat: .pcmFormatInt16,
                                            sampleRate: 16000,
                                            channels: 1,
                                            interleaved: true),
              let converter = AVAudioConverter(from: inputFormat, to: outFormat) else {
            throw NSError(domain: "VoiceAudioEngine", code: -2,
                userInfo: [NSLocalizedDescriptionKey: "无法创建音频转换器"])
        }
        lock.lock()
        self.converter = converter
        self.outFormat = outFormat
        pending = Data()
        preroll = Data()
        gated = false
        lock.unlock()

        input.installTap(onBus: 0, bufferSize: 3200, format: inputFormat) { [weak self] buffer, _ in
            self?.handle(buffer)
        }
        do {
            try startEngineIfNeeded()
        } catch {
            input.removeTap(onBus: 0)
            throw error
        }
        capturing = true
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
        return tail.isEmpty ? nil : tail
    }

    /// 播放期间（以及播放结束后的一段）门控麦克风。
    /// `flushPreroll=true` 用于打断：把门控期间攒下的真实音频立即冲给 ASR。
    func setGated(_ value: Bool, flushPreroll: Bool = false) {
        lock.lock()
        gated = value
        var flush: Data?
        if !value {
            if flushPreroll { flush = preroll }
            preroll = Data()
        }
        lock.unlock()
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
        player.scheduleBuffer(buffer, at: nil, options: []) {
            Task { @MainActor in completion() }
        }
        player.play()
    }

    func stopPlayback() {
        player.stop()
        stopEngineIfIdle()
    }

    // MARK: 内部

    private func ensureGraph() throws {
        guard !graphReady else { return }
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode, format: playbackFormat)
        // VPIO 必须在装 tap 之前开，且 input/output 都开
        do {
            try engine.inputNode.setVoiceProcessingEnabled(true)
        } catch {
            throw NSError(domain: "VoiceAudioEngine", code: -3, userInfo: [
                NSLocalizedDescriptionKey: "无法开启回声消除：\(error.localizedDescription)"
            ])
        }
        do {
            try engine.outputNode.setVoiceProcessingEnabled(true)
        } catch {
            // 只有 input 开也能工作，但残留回声会更多；记日志，不让整个语音功能不可用
            onError?("输出端回声消除不可用：\(error.localizedDescription)")
        }
        graphReady = true
    }

    private func startEngineIfNeeded() throws {
        guard !engine.isRunning else { return }
        engine.prepare()
        try engine.start()
    }

    private func stopEngineIfIdle() {
        guard !capturing, !player.isPlaying, engine.isRunning else { return }
        engine.stop()
    }

    private func handle(_ buffer: AVAudioPCMBuffer) {
        guard let converter, let outFormat else { return }
        let ratio = outFormat.sampleRate / buffer.format.sampleRate
        let capacity = AVAudioFrameCount(Double(buffer.frameLength) * ratio) + 32
        guard let out = AVAudioPCMBuffer(pcmFormat: outFormat, frameCapacity: capacity) else { return }

        var supplied = false
        var error: NSError?
        let status = converter.convert(to: out, error: &error) { _, outStatus in
            if supplied {
                outStatus.pointee = .noDataNow
                return nil
            }
            supplied = true
            outStatus.pointee = .haveData
            return buffer
        }
        if status == .error {
            if let error { onError?(error.localizedDescription) }
            return
        }
        guard out.frameLength > 0, let samples = out.int16ChannelData?[0] else { return }

        let frameCount = Int(out.frameLength)
        let data = Data(bytes: samples, count: frameCount * MemoryLayout<Int16>.size)

        if let onLevel {
            var sum = 0.0
            for i in 0..<frameCount {
                let v = Double(samples[i]) / 32768
                sum += v * v
            }
            // 说话人的 RMS 通常在 0.02~0.2，×4 让波纹与打断检测都有可用幅度
            let rms = (sum / Double(frameCount)).squareRoot()
            onLevel(Float(min(1, rms * 4)))
        }

        lock.lock()
        if gated {
            preroll.append(data)
            if preroll.count > prerollLimit {
                preroll.removeFirst(preroll.count - prerollLimit)
            }
            lock.unlock()
            return
        }
        pending.append(data)
        var chunks: [Data] = []
        while pending.count >= 6400 {
            chunks.append(Data(pending.prefix(6400)))
            pending.removeFirst(6400)
        }
        lock.unlock()
        for chunk in chunks { onChunk?(chunk) }
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
