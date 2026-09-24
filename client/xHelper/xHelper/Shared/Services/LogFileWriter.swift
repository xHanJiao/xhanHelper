import Foundation
import OSLog
#if os(iOS)
import UIKit
#elseif os(macOS)
import AppKit
#endif

final class LogFileWriter {
    private static let log = Logger(subsystem: "com.xhan.xhanHelper", category: "logwriter")

    private let logDir: URL
    private let maxFileSize = 1_000_000
    private let maxHistoryFiles = 3
    private let queue = DispatchQueue(label: "com.xhan.logwriter", qos: .utility)
    private var buffer: [LogEntry] = []
    private var flushTimer: DispatchSourceTimer?

    init() {
        let caches = FileManager.default.urls(for: .cachesDirectory, in: .userDomainMask).first!
        logDir = LocalPaths.appLogDir
        do {
            try FileManager.default.createDirectory(at: logDir, withIntermediateDirectories: true)
        } catch {
            Self.log.error("failed to create log directory: \(error)")
        }
        startFlushTimer()
        observeAppLifecycle()
    }

    func write(_ entry: LogEntry) {
        queue.async {
            self.buffer.append(entry)
            if self.buffer.count >= 50 {
                self.flush()
            }
        }
    }

    func historyFiles() -> [URL] {
        guard let files = try? FileManager.default.contentsOfDirectory(
            at: logDir,
            includingPropertiesForKeys: [.contentModificationDateKey],
            options: .skipsHiddenFiles
        ) else { return [] }
        return files
            .filter { $0.pathExtension == "log" }
            .sorted { (a, b) in
                let da = (try? a.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
                let db = (try? b.resourceValues(forKeys: [.contentModificationDateKey]))?.contentModificationDate ?? .distantPast
                return da > db
            }
    }

    func readFile(_ url: URL) -> String {
        (try? String(contentsOf: url, encoding: .utf8)) ?? ""
    }

    func forceFlush() {
        queue.async { self.flush() }
    }

    private func startFlushTimer() {
        let timer = DispatchSource.makeTimerSource(queue: queue)
        timer.schedule(deadline: .now() + 5, repeating: 5)
        timer.setEventHandler { [weak self] in
            self?.flush()
        }
        timer.resume()
        flushTimer = timer
    }

    private func observeAppLifecycle() {
        #if os(iOS)
        NotificationCenter.default.addObserver(
            forName: UIApplication.willResignActiveNotification,
            object: nil,
            queue: nil
        ) { [weak self] _ in
            self?.forceFlush()
        }
        #elseif os(macOS)
        NotificationCenter.default.addObserver(
            forName: NSApplication.willResignActiveNotification,
            object: nil,
            queue: nil
        ) { [weak self] _ in
            self?.forceFlush()
        }
        #endif
    }

    private func flush() {
        guard !buffer.isEmpty else { return }
        let batch = buffer
        buffer.removeAll()

        let lines = batch.map { serialize($0) }.joined(separator: "\n") + "\n"
        guard let data = lines.data(using: .utf8) else {
            Self.log.error("failed to encode log lines")
            return
        }
        let currentFile = logDir.appendingPathComponent("current.log")

        if shouldRotate(currentFile) {
            rotate(currentFile)
        }

        if let handle = try? FileHandle(forWritingTo: currentFile) {
            handle.seekToEndOfFile()
            handle.write(data)
            handle.closeFile()
        } else if (try? data.write(to: currentFile)) == nil {
            Self.log.error("failed to write log to \(currentFile.path)")
        }
    }

    private func serialize(_ entry: LogEntry) -> String {
        let obj: [String: Any] = [
            "t": ISO8601DateFormatter().string(from: entry.timestamp),
            "l": entry.level.rawValue,
            "m": entry.message,
            "f": entry.shortFile,
            "ln": entry.line
        ]
        guard let data = try? JSONSerialization.data(withJSONObject: obj, options: .sortedKeys),
              let str = String(data: data, encoding: .utf8) else {
            Self.log.error("failed to serialize entry: \(entry.shortFile):\(entry.line)")
            return ""
        }
        return str
    }

    private func shouldRotate(_ file: URL) -> Bool {
        guard let attrs = try? FileManager.default.attributesOfItem(atPath: file.path),
              let size = attrs[.size] as? Int else { return false }
        return size >= maxFileSize
    }

    private func rotate(_ current: URL) {
        let df = DateFormatter()
        df.dateFormat = "yyyy-MM-dd_HHmmss"
        let target = logDir.appendingPathComponent("\(df.string(from: Date())).log")
        do {
            try FileManager.default.moveItem(at: current, to: target)
        } catch {
            Self.log.error("failed to rotate log: \(error)")
        }
        purgeOldFiles()
    }

    private func purgeOldFiles() {
        let files = historyFiles()
        guard files.count > maxHistoryFiles else { return }
        for file in files.suffix(from: maxHistoryFiles) {
            try? FileManager.default.removeItem(at: file)
        }
    }
}
