import Foundation
#if os(iOS)
import UIKit
#else
import AppKit
#endif

/// 复制文本到剪贴板（macOS / iOS 两端共用）。
/// macOS 的 `NSPasteboard` 必须先 `clearContents()` 再写，否则可能保留旧的类型条目。
func copyToPasteboard(_ text: String) {
    #if os(macOS)
    let pasteboard = NSPasteboard.general
    pasteboard.clearContents()
    pasteboard.setString(text, forType: .string)
    #else
    UIPasteboard.general.string = text
    #endif
}
