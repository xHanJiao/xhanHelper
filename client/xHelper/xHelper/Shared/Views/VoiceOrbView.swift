import SwiftUI

/// 输入框里的语音音波线：一根正弦曲线，原地呼吸（相位锁定，只有振幅在变）。
/// 文件名仍叫 VoiceOrbView.swift：改文件名要动 Xcode 工程，而原全屏语音环已整体移除。
#if os(macOS)
struct VoiceWaveLine: View {
    enum Phase {
        case listening
        case thinking
        case speaking

        var word: String {
            switch self {
            case .listening: return "听"
            case .thinking: return "想"
            case .speaking: return "说"
            }
        }

        var accessibility: String {
            switch self {
            case .listening: return "语音：正在听"
            case .thinking: return "语音：正在回答"
            case .speaking: return "语音：正在朗读，点一下打断"
            }
        }
    }

    /// 定稿参数（调参页见 docs/design/voice-chat/mockup.html）：幅度 0.8 / 响应 2.0 / 波包 1.15
    private static let ampScale = 0.8
    private static let response = 2.0
    private static let packetScale = 1.15
    /// 横向波数：2.2 个完整正弦
    private static let waves = 2.2
    private static let lineWidth = 1.6

    let phase: Phase
    /// 麦克风电平（`onLevel` 的刻度：RMS×4，0...1）
    let level: Double
    var onInterrupt: () -> Void = {}

    @Environment(\.accessibilityReduceMotion) private var reduceMotion
    @State private var anim = AnimState()

    var body: some View {
        HStack(spacing: 8) {
            Text(phase.word)
                .font(.system(size: 12))
                .foregroundStyle(.secondary)
                .frame(width: 22, alignment: .leading)

            TimelineView(.animation(minimumInterval: 1.0 / 30.0)) { timeline in
                Canvas { ctx, size in
                    draw(ctx, size: size, date: timeline.date)
                }
            }
            .frame(height: 36)

            if phase == .speaking {
                Text("点一下打断")
                    .font(.system(size: 11))
                    .foregroundStyle(.secondary)
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
    }

    private func draw(_ ctx: GraphicsContext, size: CGSize, date: Date) {
        let mid = size.height / 2
        let t = reduceMotion ? 0 : date.timeIntervalSinceReferenceDate
        let dt = anim.advance(t)

        // 振幅快起慢落：响应 2.0 → 起 15ms / 落 60ms
        let target = targetHalfAmplitude(at: t)
        let tau = (target > anim.half ? 0.03 : 0.12) / Self.response
        anim.half += (target - anim.half) * (1 - exp(-dt / tau))

        let k = 2 * Double.pi * Self.waves
        let n = 220
        var path = Path()
        for i in 0...n {
            let x = Double(i) / Double(n)
            let y: Double
            if phase == .thinking {
                // 一道正弦波包从左穿到右，走过就恢复平静
                let period = 3.4 / Self.packetScale
                let pos = reduceMotion
                    ? 0.5
                    : -0.16 + (t.truncatingRemainder(dividingBy: period) / period) * 1.32
                let g = exp(-pow((x - pos) / 0.19, 2))
                let wave = sin((x - pos) * k - (reduceMotion ? 0 : t * 1.4 * Self.packetScale))
                y = 12.5 * Self.ampScale * g * wave
            } else {
                let env = pow(sin(.pi * x), 0.55)
                y = anim.half * env * sin(k * (x - 0.5))
            }
            let py = mid + min(mid - 1, max(-(mid - 1), y))
            let point = CGPoint(x: x * size.width, y: py)
            if i == 0 { path.move(to: point) } else { path.addLine(to: point) }
        }

        let base = phase == .speaking ? Color("accentSecondary") : Color("appAccent")
        var layer = ctx
        if phase == .speaking {
            layer.addFilter(.shadow(color: Color("accentSecondary").opacity(0.4), radius: 5))
        }
        layer.stroke(path,
                     with: .color(phase == .thinking ? base.opacity(0.55) : base),
                     style: StrokeStyle(lineWidth: Self.lineWidth, lineCap: .round, lineJoin: .round))
    }

    private func targetHalfAmplitude(at t: Double) -> Double {
        switch phase {
        case .thinking:
            return 0
        case .speaking:
            // 两条不可通约的正弦相乘：像说话的重音，而不是节拍器
            let cadence = (0.5 + 0.5 * sin(t * 3.6)) * (0.3 + 0.7 * abs(sin(t * 1.7 + 0.7)))
            return (5 + 9 * cadence) * Self.ampScale
        case .listening:
            let a = sqrt(min(1, max(0, level / 0.22)))
            return (2 + 12 * a) * Self.ampScale + 0.5 * sin(t * 0.7)
        }
    }

    /// Canvas 的绘制闭包里没有 @State 的更新通道，逐帧状态放在引用类型里推进
    private final class AnimState {
        var half: Double = 2
        var last: Double?

        func advance(_ t: Double) -> Double {
            defer { last = t }
            guard let last else { return 0 }
            return min(0.05, max(0.001, t - last))
        }
    }
}

#Preview("听 · 静") {
    VoiceWaveLine(phase: .listening, level: 0)
        .padding()
        .background(Color("chatBackground"))
}

#Preview("听 · 说话") {
    VoiceWaveLine(phase: .listening, level: 0.18)
        .padding()
        .background(Color("chatBackground"))
}

#Preview("想") {
    VoiceWaveLine(phase: .thinking, level: 0)
        .padding()
        .background(Color("chatBackground"))
}

#Preview("说") {
    VoiceWaveLine(phase: .speaking, level: 0)
        .padding()
        .background(Color("chatBackground"))
}
#endif
