import SwiftUI
import MarkdownUI

// MARK: - SubagentPanel — 子任务活动面板
//
// 输入栏上方（AskUserBar 之下），展示每个运行/已完成子任务的状态行：
// 状态图标 + 描述 + 轮次 + 最新动作；点击展开为整宽卡片，内部渲染子会话消息
// 列表（DB 拉取，轻量行），限高滚动。多任务垂直堆叠。
// 数据来源：subagent SSE 事件（状态行）+ GET messages（展开详情）。

struct SubagentPanel: View {
    let sessions: [SubagentSession]
    let onToggle: (String) -> Void

    var body: some View {
        VStack(spacing: 6) {
            ForEach(sessions) { session in
                SubagentSessionRow(session: session) {
                    onToggle(session.convID)
                }
            }
        }
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
                Button {
                    onToggle()
                } label: {
                    Image(systemName: session.expanded ? "chevron.down" : "chevron.right")
                        .font(.caption2)
                        .foregroundColor(.secondary)
                }
                .buttonStyle(.plain)
            }

            if session.expanded {
                ScrollView {
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
                .frame(maxHeight: 300)
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
