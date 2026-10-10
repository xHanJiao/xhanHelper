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

// MARK: - SubagentDetailPanel — 展开态（内联在底部栈里）
// 标题栏 + 单个外层滚动区；每个子任务行内不再单独滚动，展开详情由外层滚动承载。
// 内联进 bottomComposer（包内容的 VStack）→ ScrollView 在 ideal 阶段返回内容高，
// 面板自动按内容自适应；高度上限由外部 .frame(maxHeight:) 封顶，超出则内部滚动。
struct SubagentDetailPanel: View {
    let sessions: [SubagentSession]
    let onToggleSession: (String) -> Void
    let onCollapse: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 8) {
            // 整条标题栏可点：点击任意位置收起
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

#if DEBUG
// MARK: - Preview（内联底部栈：计划便签 → 子任务面板 → 输入栏占位）

/// 展开态的面板内联在底部栈里，复核它与计划便签/输入栏的上下位置与自适应高度。
private struct SubagentInlineStackPreview: View {
    private let sessions: [SubagentSession] = [
        SubagentSession(convID: "s1", description: "核实 MCP 生态来源现状", state: "started",
                        round: 1, latestAction: "browser.agent_browser_open", summary: nil),
        SubagentSession(convID: "s2", description: "查第三方 MCP 目录与接入", state: "completed",
                        round: 2, latestAction: nil, summary: "已列出 3 个候选",
                        expanded: true, messages: [
                            Message(id: "m1", conversationId: "c", role: "user",
                                    content: "查一下第三方 MCP 目录", toolCallsJSON: nil,
                                    createdAt: "2026-10-10T12:00:00Z"),
                            Message(id: "m2", conversationId: "c", role: "assistant",
                                    content: "已找到 3 个候选：mcp.so、Glama、PulseMCP。",
                                    toolCallsJSON: nil, createdAt: "2026-10-10T12:01:00Z"),
                        ]),
    ]

    private let steps: [PlanStep] = [
        PlanStep(content: "确认测试目标与范围", status: "completed"),
        PlanStep(content: "执行测试步骤并记录结果", status: "in_progress"),
        PlanStep(content: "汇总结果并输出结论", status: "pending"),
    ]

    var body: some View {
        ZStack {
            Color("chatBackground").ignoresSafeArea()
            VStack(spacing: 0) {
                Spacer()
                PlanNoteView(goal: "回归测试计划", steps: steps, expanded: true,
                             onToggle: {}, onDismiss: {})
                    .frame(maxWidth: 360, alignment: .trailing)
                    .frame(maxWidth: .infinity, alignment: .trailing)
                    .padding(.bottom, 6)
                SubagentDetailPanel(sessions: sessions, onToggleSession: { _ in }, onCollapse: {})
                    .frame(maxWidth: 720, alignment: .center)
                    .frame(maxHeight: 380)
                    .padding(.bottom, 6)
                inputPlaceholder
            }
            .frame(maxWidth: 720)
            .padding(.horizontal, 24)
            .padding(.bottom, 24)
        }
    }

    private var inputPlaceholder: some View {
        HStack {
            Text("输入消息…").font(.system(size: 15)).foregroundColor(.secondary)
            Spacer()
            Image(systemName: "paperplane.fill").foregroundColor(Color("appAccent"))
        }
        .padding(.horizontal, 14).padding(.vertical, 16)
        .background(.regularMaterial, in: RoundedRectangle(cornerRadius: 20, style: .continuous))
    }
}

#Preview("子任务面板 · 内联") {
    SubagentInlineStackPreview()
        .environmentObject(FontSizeManager.shared)
        .frame(width: 820, height: 760)
}
#endif
