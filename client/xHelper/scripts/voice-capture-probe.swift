import AVFoundation
import Foundation

/// 采集探针：用和 app 相同的 VoiceAudioEngine 抓麦克风，打印音量/分片/字节数。
/// 用来区分「麦克风没出帧」和「ASR/服务端没结果」——语音环不动时先跑它。
///
/// 用法：
///   xcrun swiftc -o /tmp/voice-probe \
///     client/xHelper/scripts/voice-capture-probe.swift \
///     client/xHelper/xHelper/Shared/Services/VoiceAudioEngine.swift \
///     client/xHelper/xHelper/Shared/Services/VoiceAudioLease.swift \
///     -sdk "$(xcrun --show-sdk-path --sdk macosx)" -target arm64-apple-macos26.3
///   /tmp/voice-probe [vpio|plain] [秒数]
///
/// vpio  = app 现在的实现（VPIO 回声消除）
/// plain = C3a 之前的实现（不开 VPIO），用来对比
@main
struct VoiceCaptureProbe {
    static func main() {
        let args = CommandLine.arguments
        let mode = args.count > 1 ? args[1] : "vpio"
        let seconds = args.count > 2 ? (Double(args[2]) ?? 5) : 5

        print("mic authorizationStatus=\(AVCaptureDevice.authorizationStatus(for: .audio).rawValue)")
        guard requestMicrophone() else {
            print("mic permission denied — 请到 系统设置 → 隐私与安全性 → 麦克风 给终端授权后重跑")
            exit(2)
        }
        print("mode=\(mode) seconds=\(seconds)")

        switch mode {
        case "plain": runPlain(seconds: seconds)
        case "vpio-raw": runVPIORaw(seconds: seconds)
        default: runVPIO(seconds: seconds)
        }
    }

    /// 只开 VPIO、直接看原始多声道：确认麦克风到底落在哪个声道。
    static func runVPIORaw(seconds: Double) {
        let engine = AVAudioEngine()
        let input = engine.inputNode
        do { try input.setVoiceProcessingEnabled(true) } catch {
            print("vpio input failed: \(error.localizedDescription)")
            exit(3)
        }
        do { try engine.outputNode.setVoiceProcessingEnabled(true) } catch {
            print("vpio output failed: \(error.localizedDescription)")
        }
        let format = input.inputFormat(forBus: 0)
        print("vpio-raw input=\(format.sampleRate)Hz/\(format.channelCount)ch/\(format.isInterleaved ? "interleaved" : "planar")")
        guard format.sampleRate > 0 else { exit(3) }

        var peaks = [Float](repeating: 0, count: Int(format.channelCount))
        var buffers = 0
        input.installTap(onBus: 0, bufferSize: 3200, format: format) { buffer, _ in
            buffers += 1
            if let data = buffer.floatChannelData {
                for ch in 0..<Int(buffer.format.channelCount) {
                    let samples = data[ch]
                    var sum = 0.0
                    for i in 0..<Int(buffer.frameLength) {
                        let v = Double(samples[i])
                        sum += v * v
                    }
                    let rms = (sum / Double(max(1, buffer.frameLength))).squareRoot()
                    peaks[ch] = max(peaks[ch], Float(min(1, rms * 4)))
                }
            }
        }
        engine.prepare()
        do { try engine.start() } catch {
            print("vpio-raw engine.start failed: \(error.localizedDescription)")
            exit(3)
        }
        for step in 1...Int(seconds) {
            Thread.sleep(forTimeInterval: 1)
            let text = peaks.enumerated().map { "ch\($0.offset)=\(String(format: "%.3f", $0.element))" }.joined(separator: " ")
            print("t=\(step)s buffers=\(buffers) \(text)")
        }
        input.removeTap(onBus: 0)
        engine.stop()
    }

    /// 不开 VPIO 的原始采集（C3a 之前的实现），只统计音量与分片。
    static func runPlain(seconds: Double) {
        let engine = AVAudioEngine()
        let input = engine.inputNode
        let format = input.inputFormat(forBus: 0)
        print("plain input=\(format.sampleRate)Hz/\(format.channelCount)ch tap=\(input.outputFormat(forBus: 0).sampleRate)Hz")
        guard format.sampleRate > 0 else {
            print("plain: 没有可用的麦克风输入")
            exit(3)
        }
        var buffers = 0
        var peak: Float = 0
        input.installTap(onBus: 0, bufferSize: 3200, format: format) { buffer, _ in
            buffers += 1
            guard let channel = buffer.floatChannelData?[0] else { return }
            var sum = 0.0
            for i in 0..<Int(buffer.frameLength) {
                let v = Double(channel[i])
                sum += v * v
            }
            let rms = (sum / Double(max(1, buffer.frameLength))).squareRoot()
            peak = max(peak, Float(min(1, rms * 4)))
        }
        engine.prepare()
        do { try engine.start() } catch {
            print("plain engine.start failed: \(error.localizedDescription)")
            exit(3)
        }
        print("plain engine started, running=\(engine.isRunning)")
        report(buffers: { buffers }, peak: { peak }, seconds: seconds)
        input.removeTap(onBus: 0)
        engine.stop()
    }

    /// app 现在的实现：VoiceAudioEngine（VPIO + 16k 转换 + 门控）。
    static func runVPIO(seconds: Double) {
        final class Stats: @unchecked Sendable {
            private let lock = NSLock()
            private var chunks = 0
            private var bytes = 0
            private var peak: Float = 0
            func addChunk(_ count: Int) {
                lock.lock(); chunks += 1; bytes += count; lock.unlock()
            }
            func observe(_ level: Float) {
                lock.lock(); peak = max(peak, level); lock.unlock()
            }
            func snapshot() -> (chunks: Int, bytes: Int, peak: Float) {
                lock.lock(); defer { lock.unlock() }
                return (chunks, bytes, peak)
            }
        }
        let stats = Stats()
        let audio = VoiceAudioEngine.shared
        let owner = ObjectIdentifier(stats)
        VoiceAudioEngine.logSink = { print("[engine] \($0)") }
        do {
            try audio.startCapture(
                owner: owner,
                onChunk: { stats.addChunk($0.count) },
                onLevel: { stats.observe($0) },
                onError: { print("[engine error] \($0)") }
            )
        } catch {
            print("vpio startCapture failed: \(error.localizedDescription)")
            exit(3)
        }
        report(buffers: { stats.snapshot().chunks }, peak: { stats.snapshot().peak }, seconds: seconds)
        _ = audio.stopCapture(owner: owner)
        let final = stats.snapshot()
        print("total: chunks=\(final.chunks) bytes=\(final.bytes) peak=\(String(format: "%.3f", final.peak))")
    }

    /// 每秒打一行：没回调 = 采集没工作；有回调但 peak≈0 = 采到的是静音。
    static func report(buffers: @escaping () -> Int, peak: @escaping () -> Float, seconds: Double) {
        let start = Date()
        while Date().timeIntervalSince(start) < seconds {
            Thread.sleep(forTimeInterval: 1)
            let elapsed = Date().timeIntervalSince(start)
            print(String(format: "t=%.0fs buffers/chunks=%d peak=%.3f", elapsed, buffers(), peak()))
        }
    }

    static func requestMicrophone() -> Bool {
        switch AVCaptureDevice.authorizationStatus(for: .audio) {
        case .authorized:
            return true
        case .notDetermined:
            let semaphore = DispatchSemaphore(value: 0)
            var granted = false
            AVCaptureDevice.requestAccess(for: .audio) { ok in
                granted = ok
                semaphore.signal()
            }
            _ = semaphore.wait(timeout: .now() + 20)
            return granted
        default:
            return false
        }
    }
}
