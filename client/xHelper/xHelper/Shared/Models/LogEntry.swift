import Foundation

enum LogLevel: String, Codable, CaseIterable {
    case debug
    case info
    case warning
    case error

    var icon: String {
        switch self {
        case .debug:   return "◌"
        case .info:    return "ℹ"
        case .warning: return "⚠"
        case .error:   return "✕"
        }
    }
}

struct LogEntry: Identifiable, Codable {
    let id: UUID
    let timestamp: Date
    let level: LogLevel
    let message: String
    let file: String
    let line: Int

    init(level: LogLevel, message: String, file: String = #file, line: Int = #line) {
        self.id = UUID()
        self.timestamp = Date()
        self.level = level
        self.message = message
        self.file = file
        self.line = line
    }

    var formattedTime: String {
        let f = DateFormatter()
        f.dateFormat = "HH:mm:ss.SSS"
        return f.string(from: timestamp)
    }

    var shortFile: String {
        file.components(separatedBy: "/").last ?? file
    }
}
