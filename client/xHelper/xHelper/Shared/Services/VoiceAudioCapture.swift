import AVFoundation
import Combine
import Foundation

/// macOS/iOS 共用的麦克风采集：AVAudioEngine → 16k/16bit/单声道 PCM。
/// 回调在音频线程触发，调用方自行切回主线程。
final class VoiceAudioCapture: @unchecked Sendable {
    private let engine = AVAudioEngine()
    private let lock = NSLock()
    private var pending = Data()
    private var converter: AVAudioConverter?
    private var outFormat: AVAudioFormat?
    private var running = false

    /// 每约 200ms 的 PCM 分片（6400 字节）。
    var onChunk: (@Sendable (Data) -> Void)?
    /// 每块音频的音量 0...1（供语音波纹使用）；在音频线程触发。
    var onLevel: (@Sendable (Float) -> Void)?
    var onError: (@Sendable (String) -> Void)?

    func start() throws {
        let input = engine.inputNode
        let inputFormat = input.inputFormat(forBus: 0)
        guard inputFormat.sampleRate > 0, inputFormat.channelCount > 0 else {
            throw NSError(domain: "VoiceAudioCapture", code: -1,
                userInfo: [NSLocalizedDescriptionKey: "没有可用的麦克风输入"])
        }
        guard let outFormat = AVAudioFormat(commonFormat: .pcmFormatInt16,
                                            sampleRate: 16000,
                                            channels: 1,
                                            interleaved: true),
              let converter = AVAudioConverter(from: inputFormat, to: outFormat) else {
            throw NSError(domain: "VoiceAudioCapture", code: -2,
                userInfo: [NSLocalizedDescriptionKey: "无法创建音频转换器"])
        }
        self.converter = converter
        self.outFormat = outFormat

        input.installTap(onBus: 0, bufferSize: 3200, format: inputFormat) { [weak self] buffer, _ in
            self?.handle(buffer)
        }
        engine.prepare()
        do {
            try engine.start()
        } catch {
            input.removeTap(onBus: 0)
            throw error
        }
        running = true
    }

    /// 停止采集，返回不足 200ms 的尾巴；调用方负责在发 end 之前把它发出去。
    @discardableResult
    func stop() -> Data? {
        guard running else { return nil }
        running = false
        engine.inputNode.removeTap(onBus: 0)
        engine.stop()
        lock.lock()
        let tail = pending
        pending = Data()
        lock.unlock()
        return tail.isEmpty ? nil : tail
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
            // 说话人的 RMS 通常在 0.02~0.2，×4 让波纹有可见幅度，上限 1
            let rms = (sum / Double(frameCount)).squareRoot()
            onLevel(Float(min(1, rms * 4)))
        }

        lock.lock()
        pending.append(data)
        var chunks: [Data] = []
        while pending.count >= 6400 {
            chunks.append(Data(pending.prefix(6400)))
            pending.removeFirst(6400)
        }
        lock.unlock()
        for chunk in chunks { onChunk?(chunk) }
    }
}
