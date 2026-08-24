import SwiftUI
#if os(macOS)
import AppKit
#else
import UIKit
#endif

/// 浏览器诊断浮动卡片：右上角非阻塞展示，一键复制结构化 debug 信息，12s 自动消失。
struct BrowserDiagnosticsView: View {
    @ObservedObject private var center = BrowserDiagnosticsCenter.shared
    @State private var copied = false

    var body: some View {
        if let event = center.current {
            VStack(alignment: .leading, spacing: 10) {
                HStack {
                    Image(systemName: "exclamationmark.triangle.fill")
                        .foregroundColor(.orange)
                    Text("浏览器诊断")
                        .font(.headline)
                    Spacer()
                    Button {
                        center.dismiss()
                    } label: {
                        Image(systemName: "xmark")
                            .font(.caption)
                            .foregroundColor(.secondary)
                    }
                    .buttonStyle(.plain)
                }
                Text(event.copyText)
                    .font(.caption.monospaced())
                    .textSelection(.enabled)
                    .frame(maxWidth: .infinity, alignment: .leading)
                HStack {
                    Button {
                        copyText(event.copyText)
                    } label: {
                        Label(copied ? "已复制" : "复制", systemImage: "doc.on.doc")
                    }
                    .buttonStyle(.borderedProminent)
                    .controlSize(.small)
                    Spacer()
                    Button("关闭") {
                        center.dismiss()
                    }
                    .buttonStyle(.bordered)
                    .controlSize(.small)
                }
            }
            .padding(12)
            .frame(width: 420)
            .background(.ultraThinMaterial, in: RoundedRectangle(cornerRadius: 12))
            .overlay(RoundedRectangle(cornerRadius: 12).stroke(Color.orange.opacity(0.35), lineWidth: 1))
            .shadow(radius: 10)
            .padding(.trailing, 16)
            .padding(.top, 16)
            .task(id: event.timestamp) {
                try? await Task.sleep(for: .seconds(12))
                if center.current?.timestamp == event.timestamp {
                    center.dismiss()
                }
            }
        }
    }

    private func copyText(_ text: String) {
        #if os(macOS)
        let pb = NSPasteboard.general
        pb.clearContents()
        pb.setString(text, forType: .string)
        #else
        UIPasteboard.general.string = text
        #endif
        copied = true
        Task {
            try? await Task.sleep(for: .seconds(1.5))
            copied = false
        }
    }
}

struct BrowserDiagnosticsModifier: ViewModifier {
    func body(content: Content) -> some View {
        content.overlay(alignment: .topTrailing) {
            BrowserDiagnosticsView()
        }
        .animation(.easeInOut(duration: 0.2), value: BrowserDiagnosticsCenter.shared.current?.timestamp)
    }
}

extension View {
    func browserDiagnostics() -> some View {
        modifier(BrowserDiagnosticsModifier())
    }
}
