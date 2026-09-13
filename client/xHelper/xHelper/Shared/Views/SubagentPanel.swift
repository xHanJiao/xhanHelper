import SwiftUI
import MarkdownUI

// MARK: - 子任务活动 UI（SubagentPanel）
//
// 折叠态：输入栏上方一行状态条（SubagentStatusBar）——聚合图标 + 计数 + 最近动作 + 展开按钮。
// 展开态：由 ChatView 以浮层形式覆盖在消息列表上（SubagentDetailPanel），点收起/遮罩收回。
// 详情数据来源：subagent SSE 事件（状态行）+ GET messages（展开详情）。

// MARK: - SubagentStatusBar — 折叠态一行状态条
struct SubagentStatusBar: View {
    let sessions: [SubagentSession]
    let expanded: Bool
    let onToggle: () -> Void

    private var running: Int { sessions.filter { $0.state == "started" }.count }
    private var completed: Int { sessions.filter { $0.state == "completed" }.count }
    private var failed: Int { sessions.filter { $0.state == "timeout" || $0.state == "failed" }.count }

    private var icon: String {
        if failed > 0 { return "exclamationmark.triangle.fill" }
        if running > 0 { return "gearshape" }
        return "checkmark.circle.fill"
    }

    private var color: Color {
        if failed > 0 { return .orange }
        if running > 0 { return .accentColor }
        return .green
    }

    private var summary: String {
        var parts: [String] = []
        if running > 0 { parts.append("\(running) 个子任务运行中") }
        if completed > 0 { parts.append("\(completed) 完成") }
        if failed > 0 { parts.append("\(failed) 失败") }
        return parts.isEmpty ? "子任务" : parts.joined(separator: " · ")
    }

    private var latestAction: String? {
        sessions.last(where: { $0.state == "started" })?.latestAction
    }

    var body: some View {
        Button {
            onToggle()
        } label: {
            HStack(spacing: 6) {
                Image(systemName: icon)
                    .font(.caption)
                    .foregroundColor(color)
                Text(summary)
                    .font(.caption).bold()
                if let action = latestAction {
                    Text(action)
                        .font(.caption2)
                        .foregroundColor(.secondary)
                        .lineLimit(1)
                }
                Spacer(minLength: 4)
                Text(expanded ? "收起" : "展开")
                    .font(.caption2)
                    .foregroundColor(.secondary)
                Image(systemName: expanded ? "chevron.down" : "chevron.right")
                    .font(.caption2)
                    .foregroundColor(.secondary)
            }
            .padding(.horizontal, 10).padding(.vertical, 7)
            .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
            .contentShape(Rectangle())
        }
        .buttonStyle(.plain)
    }
}

// MARK: - SubagentDetailPanel — 展开态浮层（覆盖消息区）
// 标题栏 + 单个外层滚动区；每个子任务行内不再单独滚动，展开详情由外层滚动承载。
struct SubagentDetailPanel: View {
    let sessions: [SubagentSession]
    let onToggleSession: (String) -> Void
    let onCollapse: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // 整条标题栏可点：点击任意位置收起浮层
            Button {
                onCollapse()
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: "gearshape.2")
                        .font(.caption)
                        .foregroundColor(.secondary)
                    Text("子任务 (\(sessions.count))")
                        .font(.caption).bold()
                    Spacer(minLength: 4)
                    Text("收起").font(.caption2).foregroundColor(.secondary)
                    Image(systemName: "chevron.down").font(.caption2).foregroundColor(.secondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            ScrollView {
                VStack(spacing: 6) {
                    ForEach(sessions) { session in
                        SubagentSessionRow(session: session) {
                            onToggleSession(session.convID)
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(10)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 12))
        .overlay(
            RoundedRectangle(cornerRadius: 12)
                .stroke(Color.primary.opacity(0.08), lineWidth: 1)
        )
    }
}

struct SubagentSessionRow: View {
    let session: SubagentSession
    let onToggle: () -> Void
    @EnvironmentObject var fontSizeManager: FontSizeManager

    // 子会话最后一条能解析出结构化结果的 assistant 消息（子任务完成后的 XML 结论）。
    // 命中则展开区只渲染渲染后的结果，隐藏过程消息；否则回退原始消息列表。
    private var finalResult: SubagentResult? {
        for msg in session.messages.reversed() where msg.role == "assistant" {
            if let r = SubagentResultParser.parse(msg.content) { return r }
        }
        return nil
    }

    private var stateIcon: String {
        switch session.state {
        case "timeout", "failed": return "exclamationmark.triangle.fill"
        case "completed": return "checkmark.circle.fill"
        default: return "gearshape"
        }
    }

    private var stateColor: Color {
        switch session.state {
        case "timeout", "failed": return .orange
        case "completed": return .green
        default: return .accentColor
        }
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 6) {
            // 整条 header 可点：点击切换该子任务详情展开/收起
            Button {
                onToggle()
            } label: {
                HStack(spacing: 6) {
                    Image(systemName: stateIcon)
                        .font(.caption)
                        .foregroundColor(stateColor)
                    Text(session.description)
                        .font(.caption).bold()
                        .lineLimit(1)
                    if let round = session.round {
                        Text("第 \(round) 轮")
                            .font(.caption2)
                            .foregroundColor(.secondary)
                    }
                    if let action = session.latestAction, session.state == "started" {
                        Text(action)
                            .font(.caption2)
                            .foregroundColor(.secondary)
                            .lineLimit(1)
                    }
                    if let summary = session.summary, session.state != "started" {
                        Text(SubagentResultParser.cleanSummary(summary))
                            .font(.caption2)
                            .foregroundColor(.secondary)
                            .lineLimit(1)
                    }
                    Spacer(minLength: 4)
                    Image(systemName: session.expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .contentShape(Rectangle())
            }
            .buttonStyle(.plain)

            if session.expanded {
                VStack(alignment: .leading, spacing: 4) {
                    if let result = finalResult {
                        Markdown(result.markdown)
                            .markdownTheme(.subagentResult(fontSize: fontSizeManager.scaledSize))
                            .markdownCodeSyntaxHighlighter(.plainText)
                            .frame(maxWidth: .infinity, alignment: .leading)
                            .macosTextSelection()
                    } else {
                        ForEach(session.messages) { msg in
                            SubagentMessageRow(msg: msg)
                        }
                    }
                }
                .frame(maxWidth: .infinity, alignment: .leading)
            }
        }
        .padding(.horizontal, 10).padding(.vertical, 8)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 10))
    }
}

// MARK: - SubagentMessageRow — 子会话消息轻量行（不复用主聊天气泡：窄卡片场景简化渲染）
struct SubagentMessageRow: View {
    let msg: Message

    private var icon: String {
        switch msg.role {
        case "user": return "person.circle"
        case "tool": return "wrench.and.screwdriver"
        default: return "sparkles"
        }
    }

    private var displayText: String {
        switch msg.role {
        case "tool":
            let content = msg.content.trimmingCharacters(in: .whitespacesAndNewlines)
            return content.count > 120 ? String(content.prefix(120)) + "…" : content
        default:
            return msg.content
        }
    }

    var body: some View {
        HStack(alignment: .top, spacing: 6) {
            Image(systemName: icon)
                .font(.caption2)
                .foregroundColor(.secondary)
                .padding(.top, 1)
            Text(displayText)
                .font(.caption)
                .foregroundColor(.primary)
                .textSelection(.enabled)
                .frame(maxWidth: .infinity, alignment: .leading)
        }
    }
}

// MARK: - 子任务结果主题
// 与主对话 chatBubble 主题区分：正文更小、次要灰；小节标题用主题橙独立成行。
private extension Theme {
    static func subagentResult(fontSize: CGFloat) -> Theme {
        Theme.gitHub
            .text {
                ForegroundColor(.secondary)
                FontSize(fontSize * 0.85)
            }
            .heading3 { configuration in
                configuration.label
                    .markdownMargin(top: 0, bottom: 6)
                    .markdownTextStyle {
                        ForegroundColor(Color("appAccent"))
                        FontWeight(.semibold)
                        FontSize(fontSize * 0.8)
                    }
            }
    }
}
