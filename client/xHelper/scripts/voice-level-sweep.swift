import AVFoundation
import Foundation

/// 电平标定探针：按 app 的口径（RMS×4，截到 1）打 100ms 一帧的电平，并每秒算一次
/// p20 / peak —— p20 就是 VoiceChatService 里的 residualBaseline，peak 是当秒最大值。
/// 用来标定「近场电平闸」的阈值：近距离说话 vs 隔壁房间外放会议声。
///
/// 用法：
///   xcrun swiftc -O -o /tmp/level-sweep client/xHelper/scripts/voice-level-sweep.swift \
///     -sdk "$(xcrun --show-sdk-path --sdk macosx)" -target arm64-apple-macos26.3
///   /tmp/level-sweep [plain|vpio] [秒数] > /tmp/level.log
///
/// plain = 不播报时的采集路径（对应 iOS 的 .spokenAudio）
/// vpio  = 播报时的采集路径（对应 iOS 的 .voiceChat，带 AGC）
@main
struct LevelSweep {
    static func main() {
        setvbuf(stdout, nil, _IOLBF, 0)
        let args = CommandLine.arguments
        let mode = args.count > 1 ? args[1] : "plain"
        let seconds = args.count > 2 ? (Double(args[2]) ?? 30) : 30

        guard requestMicrophone() else {
            print("mic permission denied — 系统设置 → 隐私与安全性 → 麦克风 给终端授权后重跑")
            exit(2)
        }

        let engine = AVAudioEngine()
        let input = engine.inputNode
        if mode == "vpio" {
            do { try input.setVoiceProcessingEnabled(true) } catch {
                print("vpio enable failed: \(error.localizedDescription)")
                exit(3)
            }
        }
        let format = input.inputFormat(forBus: 0)
        guard format.sampleRate > 0 else {
            print("没有可用的麦克风输入")
            exit(3)
        }
        print("# mode=\(mode) input=\(Int(format.sampleRate))Hz/\(format.channelCount)ch")

        let start = Date()
        let lock = NSLock()
        var frames: [Float] = []
        var buckets: [[Float]] = []
        var printed = 0

        let framesPerBucket = 10 // 100ms 一帧 → 10 帧 = 1 秒
        let tapSize = AVAudioFrameCount(format.sampleRate / 10)

        input.installTap(onBus: 0, bufferSize: tapSize, format: format) { buffer, _ in
            guard let channel = buffer.floatChannelData?[0] else { return }
            var sum = 0.0
            for i in 0..<Int(buffer.frameLength) {
                let v = Double(channel[i])
                sum += v * v
            }
            let rms = (sum / Double(max(1, buffer.frameLength))).squareRoot()
            let level = Float(min(1, rms * 4))

            lock.lock()
            frames.append(level)
            let ms = Int(Date().timeIntervalSince(start) * 1000)
            print(String(format: "RAW %6d %.4f", ms, level))
            while frames.count >= framesPerBucket {
                buckets.append(Array(frames.prefix(framesPerBucket)))
                frames.removeFirst(framesPerBucket)
            }
            while printed < buckets.count {
                let b = buckets[printed]
                let sorted = b.sorted()
                let p20 = sorted[(sorted.count - 1) / 5]
                let peak = sorted.last ?? 0
                let mean = b.reduce(0, +) / Float(b.count)
                let t = printed + 1
                print(String(format: "SEC %4d p20=%.4f peak=%.4f mean=%.4f", t, p20, peak, mean))
                printed += 1
            }
            lock.unlock()
        }

        engine.prepare()
        do { try engine.start() } catch {
            print("engine.start failed: \(error.localizedDescription)")
            exit(3)
        }
        print("# capturing \(Int(seconds))s ...")
        Thread.sleep(forTimeInterval: seconds)
        input.removeTap(onBus: 0)
        engine.stop()

        lock.lock()
        let all = buckets.flatMap { $0 }.sorted()
        if !all.isEmpty {
            let p = { (q: Double) in all[min(all.count - 1, Int(Double(all.count) * q))] }
            print(String(format: "# overall p05=%.4f p20=%.4f p50=%.4f p90=%.4f p99=%.4f",
                         p(0.05), p(0.20), p(0.50), p(0.90), p(0.99)))
        }
        lock.unlock()
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
