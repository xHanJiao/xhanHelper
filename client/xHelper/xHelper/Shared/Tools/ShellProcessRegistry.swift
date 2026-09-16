#if os(macOS)
import Foundation
import Darwin

/// 线程安全布尔标志（看门狗 ↔ 读取循环）。
nonisolated final class ShellAtomicFlag: @unchecked Sendable {
    private let lock = NSLock()
    private var value = false
    func set() { lock.withLock { value = true } }
    var isSet: Bool { lock.withLock { value } }
}

/// 运行中的 shell 进程组登记。
///
/// 用户点"停止"时 `ChatViewModel.stopStreaming()` 调 `killAll()` 按进程组杀，
/// 否则本地命令会一直跑到自己的超时（stopStreaming 原本只断 SSE + 通知服务端）。
nonisolated final class ShellProcessRegistry: @unchecked Sendable {
    static let shared = ShellProcessRegistry()

    private let lock = NSLock()
    private var jobs: [UUID: [pid_t]] = [:]
    private var cancelled: Set<UUID> = []

    func register(_ id: UUID, pids: [pid_t]) {
        guard !pids.isEmpty else { return }
        lock.withLock { jobs[id] = pids }
    }

    func unregister(_ id: UUID) {
        lock.withLock { _ = jobs.removeValue(forKey: id) }
    }

    /// 整条命令执行结束（清 pids 与取消标记）。
    func finish(_ id: UUID) {
        lock.withLock {
            _ = jobs.removeValue(forKey: id)
            cancelled.remove(id)
        }
    }

    func isCancelled(_ id: UUID) -> Bool {
        lock.withLock { cancelled.contains(id) }
    }

    /// 杀掉所有在跑的进程组，并把这些 job 标记为已取消（后续 pipeline 不再启动）。
    func killAll() {
        let snapshot = lock.withLock { () -> [UUID: [pid_t]] in
            for id in jobs.keys { cancelled.insert(id) }
            return jobs
        }
        for (_, pids) in snapshot {
            for pid in pids where pid > 0 { kill(-pid, SIGKILL) }
        }
    }
}

/// shell 并发闸：主循环客户端工具串行，但 task 子 agent 可并发触发多个 shell，限流避免同时压机器。
actor ShellConcurrencyGate {
    static let shared = ShellConcurrencyGate()
    private let limit = 2
    private var active = 0
    private var waiters: [CheckedContinuation<Void, Never>] = []

    func acquire() async {
        if active < limit {
            active += 1
            return
        }
        await withCheckedContinuation { waiters.append($0) }
    }

    func release() {
        if waiters.isEmpty {
            active = max(0, active - 1)
        } else {
            waiters.removeFirst().resume()
        }
    }
}
#endif
