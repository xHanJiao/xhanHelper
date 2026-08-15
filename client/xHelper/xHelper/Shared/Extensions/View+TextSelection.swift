import SwiftUI

extension View {
    /// macOS 划词选词（与助手气泡一致）；iOS 保持不可选（长按菜单与选中冲突）
    func macosTextSelection() -> some View {
        #if os(macOS)
        textSelection(.enabled)
        #else
        self
        #endif
    }
}
