import Combine
import SwiftUI

@MainActor
class LogBuffer: ObservableObject {
    static let shared = LogBuffer()

    @Published private(set) var entries: [LogEntry] = []

    private let maxEntries = 200
    private let fileWriter = LogFileWriter()

    func error(_ msg: String, file: String = #file, line: Int = #line) {
        let entry = LogEntry(level: .error, message: msg, file: file, line: line)
        append(entry)
    }

    func warning(_ msg: String, file: String = #file, line: Int = #line) {
        let entry = LogEntry(level: .warning, message: msg, file: file, line: line)
        append(entry)
    }

    func info(_ msg: @autoclosure () -> String, file: String = #file, line: Int = #line) {
        #if DEBUG
        let entry = LogEntry(level: .info, message: msg(), file: file, line: line)
        append(entry)
        #endif
    }

    func debug(_ msg: @autoclosure () -> String, file: String = #file, line: Int = #line) {
        #if DEBUG
        let entry = LogEntry(level: .debug, message: msg(), file: file, line: line)
        append(entry)
        #endif
    }

    private func append(_ entry: LogEntry) {
        entries.append(entry)
        if entries.count > maxEntries {
            entries.removeFirst(entries.count - maxEntries)
        }
        fileWriter.write(entry)
    }

    func clearSession() {
        entries.removeAll()
    }

    func historyFiles() -> [URL] {
        fileWriter.historyFiles()
    }

    func readHistoryFile(_ url: URL) -> String {
        fileWriter.readFile(url)
    }

    func removeEntry(at index: Int) {
        guard entries.indices.contains(index) else { return }
        entries.remove(at: index)
    }
}
