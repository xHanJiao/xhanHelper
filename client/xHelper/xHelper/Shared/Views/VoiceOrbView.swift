import SwiftUI

/// 输入框里的语音波形带：以中线镜像填充的一根刺状波形，原地呼吸（只变振幅）。
/// 文件名仍叫 VoiceOrbView.swift：改文件名要动 Xcode 工程，而原全屏语音环已整体移除。
#if os(macOS)
struct VoiceWaveLine: View {
    enum Phase {
        case listening
        case thinking
        case speaking

        var accessibility: String {
            switch self {
            case .listening: return "语音：正在听"
            case .thinking: return "语音：正在回答"
            case .speaking: return "语音：正在朗读，点一下打断"
            }
        }
    }

    /// 定稿参数（调参页见 docs/design/voice-chat/mockup.html）：
    /// 听 / 说 = 幅度 1.00 / 响应 2.00 / 波包 1.15 / 静息 1.00 / 横向 0.05；
    /// 想 = 同一组参数、幅度 ×0.8。
    private static let amp: Double = 1.0
    private static let response: Double = 2.0
    private static let packet: Double = 1.15
    private static let wobble: Double = 1.0
    /// 横向流动：0.05 时波形带约 10~50 秒扫过一屏（跟着音节跑的那种速度已废弃）
    private static let drift: Double = 0.05
    private static let thinkScale: Double = 0.8

    let phase: Phase
    /// 麦克风瞬时电平（`onWaveLevel` 的刻度：RMS×4，0...1）。
    /// 传闭包是为了每帧取最新值，又不把 60Hz 的电平当成 @Published 去刷新整个聊天页。
    let level: () -> Double
    var onInterrupt: () -> Void = {}

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var anim = AnimState()

    var body: some View {
        TimelineView(.animation(minimumInterval: 1.0 / 30.0)) { timeline in
            Canvas { ctx, size in
                draw(ctx, size: size, date: timeline.date)
            }
        }
        .frame(height: 36)
        .contentShape(Rectangle())
        .onTapGesture {
            if phase == .speaking { onInterrupt() }
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel(phase.accessibility)
        .accessibilityAddTraits(phase == .speaking ? .isButton : [])
        .accessibilityAction {
            if phase == .speaking { onInterrupt() }
        }
    }

    private func draw(_ ctx: GraphicsContext, size: CGSize, date: Date) {
        let mid = size.height / 2
        let now = date.timeIntervalSinceReferenceDate
        let t = reduceMotion ? 0 : now
        // dt 用真实时间：reduced-motion 只冻结波形相位，振幅该多快还是多快
        let dt = anim.advance(now)
        anim.track(phase, now: t, reduceMotion: reduceMotion)

        // 振幅快起慢落：响应 2.0 → 起 15ms / 落 60ms
        let target = targetAmp(at: t)
        let tau = (target > anim.amp ? 0.03 : 0.12) / Self.response
        anim.amp += (target - anim.amp) * (1 - exp(-dt / tau))

        // 换态时两套波形交叉淡化，避免公式硬切造成的跳变；
        // 稳态下 prev == phase（99% 时间），只算一套
        let ease = anim.ease(at: t)
        let prev = anim.prev
        let mixing = prev != phase && ease < 1
        let n = 240
        if anim.halves.count != n + 1 {
            anim.halves = [Double](repeating: 0, count: n + 1)
        }
        for i in 0...n {
            let x = Double(i) / Double(n)
            let current = bandHalf(phase, x: x, t: t)
            let half = mixing ? bandHalf(prev, x: x, t: t) * (1 - ease) + current * ease : current
            anim.halves[i] = clampHalf(half, mid: mid)
        }

        var path = Path()
        for i in 0...n {
            let point = CGPoint(x: Double(i) / Double(n) * size.width, y: mid - anim.halves[i])
            if i == 0 { path.move(to: point) } else { path.addLine(to: point) }
        }
        // 下半边镜像回去，闭合填充成一条有厚度的波形带
        for i in stride(from: n, through: 0, by: -1) {
            path.addLine(to: CGPoint(x: Double(i) / Double(n) * size.width, y: mid + anim.halves[i]))
        }
        path.closeSubpath()

        let base = phase == .speaking ? Color("accentSecondary") : Color("appAccent")
        var layer = ctx
        if phase == .speaking {
            layer.addFilter(.shadow(color: Color("accentSecondary").opacity(0.4), radius: 5))
        }
        layer.fill(path, with: .color(phase == .thinking ? base.opacity(0.55) : base))
    }

    /// 单点的半高（中线到边缘），两端用 sin^0.55 收口。
    private func bandHalf(_ p: Phase, x: Double, t: Double) -> Double {
        let env = pow(sin(.pi * x), 0.55)
        if p == .thinking {
            // 一道小鼓包从左穿到右，走过恢复平静
            let period = 2.4 / Self.packet
            let pos = reduceMotion
                ? 0.5
                : -0.14 + (t.truncatingRemainder(dividingBy: period) / period) * 1.28
            let g = exp(-pow((x - pos) / 0.16, 2))
            let nse = noise(x, t: reduceMotion ? 0 : t * 3.6 * Self.response * Self.drift, seed: 3.1)
            return 7 * Self.amp * Self.thinkScale * g * (0.3 + 0.7 * nse)
        }
        let a = sqrt(min(1, max(0, level() / 0.22)))
        // 声音越大，纹理变化越快（跟随音节）；横向整体流动由 drift 控制
        let sp = p == .listening ? 1.4 + 3.2 * a : 2 + 3.5 * cadence(t)
        let nse = noise(x, t: reduceMotion ? 0 : t * sp * Self.response * Self.drift,
                        seed: p == .speaking ? 5.7 : 2.3)
        return anim.amp * env * (0.08 + 0.92 * nse)
    }

    private func targetAmp(at t: Double) -> Double {
        switch phase {
        case .thinking:
            return 0
        case .speaking:
            // 两条不可通约的正弦相乘：像说话的重音，而不是节拍器
            return (8 + 12 * cadence(t)) * Self.amp
        case .listening:
            let a = sqrt(min(1, max(0, level() / 0.22)))
            let idle = 2.6 + 0.8 * Self.wobble * sin(t * 0.7)
            let active = 2 + 12 * a
            // 静息呼吸与说话电平平滑衔接（a ≥ 0.2 完全走电平）
            let w = min(1, a / 0.2)
            return (idle * (1 - w) + active * w) * Self.amp
        }
    }

    private func clampHalf(_ v: Double, mid: Double) -> Double {
        min(mid - 1, max(0.55, v)) // 0.55 保证静音时也有一条细线
    }

    private func cadence(_ t: Double) -> Double {
        (0.5 + 0.5 * sin(t * 3.6)) * (0.3 + 0.7 * abs(sin(t * 1.7 + 0.7)))
    }

    // 值噪声：平滑插值的随机序列，颗粒尺度约 6~15px，做出真实录音那种稀疏尖峰
    private func noise(_ x: Double, t: Double, seed: Double) -> Double {
        let a = vnoise(x * 70 + t, seed: seed)
        let b = vnoise(x * 190 - t * 0.9, seed: seed + 7.3)
        let n = pow(0.7 * a + 0.3 * b, 2.0)
        let swell = 0.55 + 0.45 * vnoise(x * 6 + t * 0.55, seed: seed + 3)
        return n * swell
    }

    private func vnoise(_ v: Double, seed: Double) -> Double {
        let i = floor(v)
        let f = v - i
        let u = f * f * (3 - 2 * f)
        return hash1(i, seed: seed) * (1 - u) + hash1(i + 1, seed: seed) * u
    }

    private func hash1(_ n: Double, seed: Double) -> Double {
        let v = sin(n * 127.1 + seed * 311.7) * 43758.5453
        return v - floor(v)
    }

    /// Canvas 的绘制闭包里没有 @State 的更新通道，逐帧状态放在引用类型里推进
    private final class AnimState {
        var amp: Double = 2
        var halves: [Double] = []
        private(set) var prev: VoiceWaveLine.Phase = .listening
        private var cur: VoiceWaveLine.Phase?
        private var phaseAt: Double = -999
        private var last: Double?

        func advance(_ t: Double) -> Double {
            defer { last = t }
            guard let last else { return 0 }
            return min(0.05, max(0.001, t - last))
        }

        func track(_ phase: VoiceWaveLine.Phase, now: Double, reduceMotion: Bool) {
            guard let current = cur else {
                cur = phase
                prev = phase
                return
            }
            guard phase != current else { return }
            prev = current
            cur = phase
            phaseAt = reduceMotion ? -999 : now
        }

        func ease(at t: Double) -> Double {
            let m = min(1, max(0, (t - phaseAt) / 0.22))
            return m < 0.5 ? 2 * m * m : 1 - pow(-2 * m + 2, 2) / 2
        }
    }
}

#Preview("听 · 静") {
    VoiceWaveLine(phase: .listening, level: { 0 })
        .padding()
        .background(Color("chatBackground"))
}

#Preview("听 · 说话") {
    VoiceWaveLine(phase: .listening, level: { 0.18 })
        .padding()
        .background(Color("chatBackground"))
}

#Preview("想") {
    VoiceWaveLine(phase: .thinking, level: { 0 })
        .padding()
        .background(Color("chatBackground"))
}

#Preview("说") {
    VoiceWaveLine(phase: .speaking, level: { 0 })
        .padding()
        .background(Color("chatBackground"))
}
#endif
