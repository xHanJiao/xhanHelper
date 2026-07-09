import SwiftUI
import Combine

enum AppColorScheme: String, CaseIterable {
    case system
    case light
    case dark

    var colorScheme: ColorScheme? {
        switch self {
        case .system: return nil
        case .light:  return .light
        case .dark:   return .dark
        }
    }

    var label: String {
        switch self {
        case .system: return "跟随系统"
        case .light:  return "浅色"
        case .dark:   return "深色"
        }
    }
}

class ColorSchemeManager: ObservableObject {
    @Published var selectedScheme: AppColorScheme {
        didSet {
            UserDefaults.standard.set(selectedScheme.rawValue, forKey: "color_scheme")
        }
    }

    init() {
        let raw = UserDefaults.standard.string(forKey: "color_scheme") ?? ""
        self.selectedScheme = AppColorScheme(rawValue: raw) ?? .system
    }
}
