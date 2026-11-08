import SwiftUI

/// C 模式全屏语音环：听 / 想 / 说 三态 + 麦克风音量。
/// 纯展示视图，状态与动作都由调用方注入，便于预览靶直接渲染。
#if os(macOS)
struct VoiceOrbView: View {
    enum Phase {
        case listening
        case thinking
        case speaking

        var title: String {
            switch self {
            case .listening: return "听"
            case .thinking: return "想"
            case .speaking: return "说"
            }
        }

        var hint: String {
            switch self {
            case .listening: return "说完停顿一下就会提交"
            case .thinking: return "正在整理回答"
            case .speaking: return "直接开口就能打断，点「结束」是收会话"
            }
        }

        var symbol: String {
            switch self {
            case .listening: return "mic"
            case .thinking: return "ellipsis"
            case .speaking: return "speaker.wave.2.fill"
            }
        }
    }

    let phase: Phase
    /// 麦克风音量 0...1（只在 listening 时有意义）
    let level: Double
    var onClose: () -> Void

    var body: some View {
        ZStack {
            // 不用 .ultraThinMaterial：非不透明窗 + 右侧 WKWebView 逐帧重合成会偶发灰闪
            // （同 docs/bugs/030）。这里用接近不透明的底色，观感稳定且仍能看到背景轮廓。
            Color("chatBackground")
                .opacity(0.97)
                .ignoresSafeArea()

            VStack(spacing: 24) {
                orb
                Text(phase.title)
                    .font(.system(size: 24, weight: .medium))
                Text(phase.hint)
                    .font(.callout)
                    .foregroundStyle(.secondary)
            }

            VStack {
                Spacer()
                Button("结束", action: onClose)
                    .buttonStyle(.borderedProminent)
                    .padding(.bottom, 36)
            }
        }
        .transition(.opacity)
    }

    private var orb: some View {
        ZStack {
            // 三层静态环做呼吸；thinking 时整体缓慢脉动
            ForEach(0..<3) { i in
                Circle()
                    .stroke(Color("appAccent").opacity(0.34 - Double(i) * 0.09), lineWidth: 1.5)
                    .frame(width: 180 + CGFloat(i) * 34, height: 180 + CGFloat(i) * 34)
            }
            Circle()
                .fill(Color("appAccent").opacity(0.16))
                .frame(width: 180, height: 180)
                .scaleEffect(1 + effectiveLevel * 0.45)
            Image(systemName: phase.symbol)
                .font(.system(size: 34, weight: .light))
                .foregroundStyle(Color("appAccent"))
        }
        .frame(width: 260, height: 260)
        .animation(.easeOut(duration: 0.12), value: effectiveLevel)
        .animation(.easeInOut(duration: 1.2).repeatForever(autoreverses: true), value: phase)
    }

    private var effectiveLevel: Double {
        phase == .listening ? min(1, max(0, level)) : 0.25
    }
}

#Preview("听") {
    VoiceOrbView(phase: .listening, level: 0.4, onClose: {})
}

#Preview("想") {
    VoiceOrbView(phase: .thinking, level: 0, onClose: {})
}

#Preview("说") {
    VoiceOrbView(phase: .speaking, level: 0, onClose: {})
}
#endif
