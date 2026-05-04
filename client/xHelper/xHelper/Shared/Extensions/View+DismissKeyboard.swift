import SwiftUI
#if os(iOS)
import UIKit
#endif

extension View {
    func dismissKeyboardOnTap() -> some View {
        #if os(iOS)
        self.onTapGesture {
            UIApplication.shared.sendAction(#selector(UIResponder.resignFirstResponder), to: nil, from: nil, for: nil)
        }
        #else
        self
        #endif
    }
}
