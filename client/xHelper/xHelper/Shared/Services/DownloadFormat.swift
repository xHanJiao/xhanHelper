import Foundation

/// 下载格式化工具（大小 / 速度 / 时长），下载记录页与浏览器顶部提示条共用。
enum DownloadFormat {
    /// 字节大小：B / KB / MB / GB
    static func byteString(_ bytes: Int) -> String {
        let units = ["B", "KB", "MB", "GB"]
        var value = Double(bytes)
        var unit = 0
        while value >= 1024 && unit < units.count - 1 {
            value /= 1024
            unit += 1
        }
        return String(format: "%.1f %@", value, units[unit])
    }

    /// 速度：B/s / KB/s / MB/s / GB/s
    static func speedString(_ bytesPerSec: Double) -> String {
        guard bytesPerSec > 0 else { return "0 B/s" }
        let units = ["B/s", "KB/s", "MB/s", "GB/s"]
        var value = bytesPerSec
        var unit = 0
        while value >= 1024 && unit < units.count - 1 {
            value /= 1024
            unit += 1
        }
        return String(format: "%.1f %@", value, units[unit])
    }

    /// 时长：m:ss 或 h:mm:ss
    static func durationString(_ seconds: Double) -> String {
        let s = max(Int(seconds.rounded()), 0)
        let h = s / 3600
        let m = (s % 3600) / 60
        let sec = s % 60
        if h > 0 { return String(format: "%d:%02d:%02d", h, m, sec) }
        return String(format: "%d:%02d", m, sec)
    }
}
