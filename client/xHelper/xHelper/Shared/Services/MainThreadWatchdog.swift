import Foundation
import os
import Darwin

// MARK: - 信号级陷阱抓取器（async-signal-safe）

/// 预计算的陷阱日志文件路径（信号 handler 内只做裸 C 调用：open/write/backtrace/close）
private let xhanTrapLogPath: UnsafeMutablePointer<CChar> = {
    let logsDir = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
        .appendingPathComponent("Logs", isDirectory: true)
    let path = logsDir.appendingPathComponent("xhanHelper-trap.log").path
    let bytes = Array(path.utf8CString)
    let p = UnsafeMutablePointer<CChar>.allocate(capacity: max(bytes.count, 1))
    p.initialize(from: bytes, count: bytes.count)
    return p
}()

/// 固定头（无动态格式化，全程非变参安全）
private let xhanTrapHeader: UnsafeMutablePointer<CChar> = {
    let bytes = Array("=== xhanHelper trap ===\n".utf8CString)
    let p = UnsafeMutablePointer<CChar>.allocate(capacity: bytes.count)
    p.initialize(from: bytes, count: bytes.count)
    return p
}()

/// 栈帧缓冲（128 帧够用）
private let xhanTrapStack: UnsafeMutablePointer<UnsafeMutableRawPointer?> = {
    let p = UnsafeMutablePointer<UnsafeMutableRawPointer?>.allocate(capacity: 128)
    p.initialize(repeating: nil, count: 128)
    return p
}()

/// 主线程看门狗：检测主线程长时间停摆（beachball 僵尸态）并自动抓取全进程线程栈。
///
/// 机制（双通道）：
/// 1. 心跳定时器（主 run loop，0.5s）：主线程执行长同步操作/卡死时不服务 run loop → 心跳停摆。
///    阈值 1.5s（低于 macOS 未响应判定 ~2s），保证在 WindowServer 标记前抓到阻塞本体。
/// 2. 信号级陷阱抓取（SIGTRAP/SIGABRT/SIGSEGV/SIGILL）：Swift fatalError/precondition/
///    强制解包/崩溃 直接把当前线程真实栈写到 ~/Library/Logs/xhanHelper-trap-*.txt
///    （.ips 的故障线程常常没有帧，这里能拿到活栈）。
/// 两者都用 `/usr/bin/sample` 或 backtrace 抓栈，独立于 app 生命周期。
final class MainThreadWatchdog {
    static let shared = MainThreadWatchdog()

    private static let tickInterval: TimeInterval = 0.5
    private static let stallThreshold: TimeInterval = 1.5
    private static let dumpCooldown: TimeInterval = 60

    private let heartbeat: OSAllocatedUnfairLock<Date>
    private let observerQueue = DispatchQueue(label: "xhan.mainthread.watchdog")
    private var observerSource: DispatchSourceTimer?
    private var lastDumpAt: Date = .distantPast
    private var started = false

    private init() {
        heartbeat = OSAllocatedUnfairLock(initialState: Date())
    }

    /// 启动（主线程调用）：挂主 run loop 心跳定时器 + 后台检查队列 + 信号陷阱抓取。
    func start() {
        guard !started else { return }
        started = true

        let timer = Timer(timeInterval: Self.tickInterval, repeats: true) { [weak self] _ in
            self?.heartbeat.withLock { $0 = Date() }
        }
        RunLoop.main.add(timer, forMode: .common)

        let source = DispatchSource.makeTimerSource(queue: observerQueue)
        source.schedule(deadline: .now() + Self.tickInterval, repeating: Self.tickInterval)
        source.setEventHandler { [weak self] in
            self?.checkStall()
        }
        source.resume()
        observerSource = source

        Self.installTrapDumper()
        #if DEBUG
        startFlightRecorder()
        #endif
    }

    // MARK: - 飞行记录器（DEBUG only）

    /// 后台每 5s 用 /usr/bin/sample 滚动抓一次全进程快照，保留最近 ~30s（6 个文件轮换）。
    /// 挂死发生时（无论是否卡主线程、无论看门狗是否触发），预挂样本直接显示卡住前/时的状态。
    private var flightSource: DispatchSourceTimer?
    private let flightQueue = DispatchQueue(label: "xhan.flight.recorder")
    private var flightSampleInProgress = false
    private var flightCounter = 0

    private func startFlightRecorder() {
        let source = DispatchSource.makeTimerSource(queue: flightQueue)
        source.schedule(deadline: .now() + 5, repeating: 5)
        source.setEventHandler { [weak self] in
            self?.recordFlightSample()
        }
        source.resume()
        flightSource = source
    }

    private func recordFlightSample() {
        guard !flightSampleInProgress else { return }
        flightSampleInProgress = true
        let pid = ProcessInfo.processInfo.processIdentifier
        let logsDir = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Logs", isDirectory: true)
        let idx = flightCounter % 6
        flightCounter += 1
        let out = logsDir.appendingPathComponent("xhanHelper-flight-\(idx).txt").path

        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/sample")
        p.arguments = ["\(pid)", "1", "-file", out]
        p.terminationHandler = { [weak self] _ in
            self?.flightQueue.async { self?.flightSampleInProgress = false }
        }
        do {
            try p.run()
        } catch {
            flightSampleInProgress = false
        }
    }

    // MARK: - 心跳停摆检测（抓主线程长阻塞）

    private func checkStall() {
        let now = Date()
        let lastTick = heartbeat.withLock { $0 }
        let stalled = now.timeIntervalSince(lastTick)
        guard stalled > Self.stallThreshold else { return }
        guard now.timeIntervalSince(lastDumpAt) > Self.dumpCooldown else { return }
        lastDumpAt = now
        print("[MainThreadWatchdog] main thread stalled \(Int(stalled))s — sampling all threads")
        dumpSample()
    }

    private func dumpSample() {
        let pid = ProcessInfo.processInfo.processIdentifier
        let logsDir = FileManager.default.urls(for: .libraryDirectory, in: .userDomainMask)[0]
            .appendingPathComponent("Logs", isDirectory: true)
        let out = logsDir
            .appendingPathComponent("xhanHelper-hang-\(Self.fileStamp(from: Date()))")
            .appendingPathExtension("txt")
            .path

        let p = Process()
        p.executableURL = URL(fileURLWithPath: "/usr/bin/sample")
        p.arguments = ["\(pid)", "3", "-file", out]
        do {
            try p.run()
            print("[MainThreadWatchdog] sample -> \(out)")
        } catch {
            print("[MainThreadWatchdog] sample launch failed: \(error)")
        }
    }

    // MARK: - 信号级陷阱抓取（抓 SIGTRAP/SIGABRT/SIGSEGV 的活栈）

    private static var trapInstalled = false

    private static func installTrapDumper() {
        guard !trapInstalled else { return }
        trapInstalled = true

        let handler: @convention(c) (Int32) -> Void = { sig in
            // 只允许 async-signal-safe 的 C 调用（无变参/无格式化/无分配）
            let fd = open(xhanTrapLogPath, O_WRONLY | O_CREAT | O_APPEND, 0o644)
            if fd >= 0 {
                write(fd, xhanTrapHeader, strlen(xhanTrapHeader))
                let n = backtrace(xhanTrapStack, 128)
                backtrace_symbols_fd(xhanTrapStack, n, fd)
                close(fd)
            }
            signal(sig, SIG_DFL)
            raise(sig)
        }

        signal(SIGTRAP, handler)  // Swift fatalError / precondition / 强制解包
        signal(SIGABRT, handler)
        signal(SIGSEGV, handler)
        signal(SIGILL, handler)
    }

    private static func fileStamp(from date: Date) -> String {
        let f = DateFormatter()
        f.dateFormat = "yyyyMMdd_HHmmss"
        return f.string(from: date)
    }
}
