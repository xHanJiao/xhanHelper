import Foundation

/// 近场电平闸：只把「离麦克风够近」的话当成用户输入。
///
/// 刻度与 `VoiceAudioEngine.onLevel` 一致（`min(1, RMS × 4)`）。阈值来自
/// `docs/design/voice-nearfield/design.md` 实测：隔壁房间会议声 2s 窗中位数 0.006，
/// 本人 50cm 0.21，差 35 倍。
///
/// 纯逻辑（无音频/网络依赖），供 `swift test` 覆盖；接线在 `VoiceChatService`。
enum NearFieldGate {
    enum Sensitivity: String, CaseIterable, Identifiable {
        case off
        case loose
        case standard
        case near

        var id: String { rawValue }

        var label: String {
            switch self {
            case .off: return "关闭"
            case .loose: return "宽松"
            case .standard: return "标准"
            case .near: return "近场"
            }
        }

        /// nil = 闸门关闭（与加闸之前的行为完全一致）。
        var threshold: Double? {
            switch self {
            case .off: return nil
            case .loose: return 0.02
            case .standard: return 0.05
            case .near: return 0.10
            }
        }
    }

    struct Frame: Equatable {
        let at: Date
        let level: Double
        /// 播报期间麦克风被门控，这时读到的是 TTS 漏音残留，不算用户说话。
        let gated: Bool
    }

    /// 整段判定的三态：'accept' / 'reject' / 'tooShort'（数据不足，判不了）。
    enum Verdict: Equatable {
        case accept
        case reject
        case tooShort
    }

    static let windowSeconds: TimeInterval = 2
    /// 窗口里至少要有 0.8 秒未门控数据才判：不够就放行（误拒比误收难受）。
    static let minSpanSeconds: TimeInterval = 0.8
    /// 续命判定窗口：voice 是 partial（开口后一小段就来），不能用提交闸的 2s 窗。
    static let keepAliveSeconds: TimeInterval = 0.4
    /// 剔除静音后至少要有 0.15 秒有声帧：单帧尖峰（碰一下麦克风 0.3~0.55）不算「有人说话」。
    static let minVoicedSeconds: TimeInterval = 0.15
    /// 噪声本底剔除：floor = min(cap, max(0.004, 本窗 p20 × 2))，cap = min(0.04, 阈值/2)
    private static let noiseBase = 0.004
    private static let noiseMultiplier = 2.0
    private static let noiseCap = 0.04

    static func median(_ values: [Double]) -> Double {
        guard !values.isEmpty else { return 0 }
        let sorted = values.sorted()
        return sorted[(sorted.count - 1) / 2]
    }

    static func percentile(_ values: [Double], _ q: Double) -> Double {
        guard !values.isEmpty else { return 0 }
        let sorted = values.sorted()
        let index = min(sorted.count - 1, Int(Double(sorted.count - 1) * q))
        return sorted[index]
    }

    static func shouldAccept(frames: [Frame], sensitivity: Sensitivity,
                             now: Date = Date()) -> Bool {
        shouldAccept(frames: frames, threshold: sensitivity.threshold, now: now)
    }

    /// true = 放行（流式判停分句的提交闸）。数据不足放行——误拒比误收难受。
    ///
    /// 三处刻意设计：
    /// - 只用未门控帧：播报期间读到的是 TTS 漏音残留，不是用户说话。
    /// - 先剔掉静音帧再取中位数：sentence 到达时窗口尾部有 0.8 秒以上的判停静音，
    ///   直接取中位数会把 1 秒出头的短句拉到阈值以下（真机数据模拟过）。静音帧不携带
    ///   距离信息，按本窗 p20 估个噪声本底剔掉；剔完一个不剩说明这段本来就没人靠近说。
    /// - 有声帧要覆盖 0.15 秒以上：否则一次碰麦尖峰（单帧 0.3~0.55）就能把判定翻成放行。
    static func shouldAccept(frames: [Frame], threshold: Double?, now: Date) -> Bool {
        guard let threshold else { return true }
        let recent = frames.filter { !$0.gated && now.timeIntervalSince($0.at) <= windowSeconds }
        return judge(recent, threshold: threshold) != .reject
    }

    static func judgeHoldSpan(frames: [Frame], sensitivity: Sensitivity) -> Verdict {
        judgeHoldSpan(frames: frames, threshold: sensitivity.threshold)
    }

    /// 长按说话的整段判定：整段音频都在手上，帧不够说明本来就没说够 → `.tooShort`，
    /// 不能像流式那样 fail-open（见 docs/design/voice-hold-mode/design.md）。
    static func judgeHoldSpan(frames: [Frame], threshold: Double?) -> Verdict {
        guard let threshold else { return .accept }
        return judge(frames.filter { !$0.gated }, threshold: threshold)
    }

    private static func judge(_ frames: [Frame], threshold: Double) -> Verdict {
        guard frames.count >= 4,
              let first = frames.first, let last = frames.last,
              last.at.timeIntervalSince(first.at) >= minSpanSeconds else { return .tooShort }
        let levels = frames.map(\.level)
        let cap = min(noiseCap, threshold / 2)
        let floor = min(cap, max(noiseBase, percentile(levels, 0.2) * noiseMultiplier))
        let voiced = frames.filter { $0.level >= floor }
        guard voiced.count >= 2,
              let voicedFirst = voiced.first, let voicedLast = voiced.last,
              voicedLast.at.timeIntervalSince(voicedFirst.at) >= minVoicedSeconds else { return .reject }
        return median(voiced.map(\.level)) >= threshold ? .accept : .reject
    }

    static func shouldKeepAlive(frames: [Frame], sensitivity: Sensitivity,
                                now: Date = Date()) -> Bool {
        shouldKeepAlive(frames: frames, threshold: sensitivity.threshold, now: now)
    }

    /// 空闲收口续命判定：只有近处的人声才算「还在说话」。
    ///
    /// 服务端只要 ASR 识别出非空文本就发 voice，隔壁嗑瓜子/收拾桌子也会被识别成文本。
    /// 以前 voice 无条件续命，远处噪声一直来就把 30 秒空闲收口无限推后。这里用最近
    /// 400ms 的未门控帧中位数判距离；帧不足 2 条不续命（播报期门控帧被排除后为空，
    /// 本来就有 speaking 保护）。
    static func shouldKeepAlive(frames: [Frame], threshold: Double?, now: Date) -> Bool {
        guard let threshold else { return true }
        let recent = frames.filter { !$0.gated && now.timeIntervalSince($0.at) <= keepAliveSeconds }
        guard recent.count >= 2 else { return false }
        return median(recent.map(\.level)) >= threshold
    }
}
