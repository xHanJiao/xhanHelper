import SwiftUI
#if os(iOS)
import UIKit
#else
import AppKit
#endif

struct ChatBubble: View {
    let text: String
    let isUser: Bool

    var body: some View {
        HStack {
            if isUser { Spacer() }

            Text(text.isEmpty && !isUser ? " " : text)
                .padding(.horizontal, 14)
                .padding(.vertical, 10)
                .background(isUser ? Color.blue : Color.gray.opacity(0.15))
                .foregroundColor(isUser ? .white : .primary)
                .cornerRadius(18)

            if !isUser { Spacer() }
        }
        .padding(.horizontal)
    }
}
