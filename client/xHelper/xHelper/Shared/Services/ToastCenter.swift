import Combine
import Foundation
import SwiftUI
#if os(iOS)
import UIKit
#endif

@MainActor
final class ToastCenter: ObservableObject {
    static let shared = ToastCenter()

    @Published var message: String?

    private init() {}

    func show(_ msg: String) {
        #if os(iOS)
        guard UIApplication.shared.applicationState == .active else {
            LogBuffer.shared.info("toast dropped (background): \(msg)")
            return
        }
        #endif
        message = msg
    }

    static func friendlyErrorText(_ error: Error, action: String) -> String {
        let nsError = error as NSError
        if nsError.domain == NSURLErrorDomain {
            switch nsError.code {
            case NSURLErrorTimedOut:
                return "请求超时，请重试"
            case NSURLErrorNotConnectedToInternet,
                 NSURLErrorCannotConnectToHost,
                 NSURLErrorCannotFindHost,
                 NSURLErrorDNSLookupFailed:
                return "网络连接失败，请检查网络"
            default:
                break
            }
        }
        return "\(action)失败：\(nsError.localizedDescription)"
    }
}
