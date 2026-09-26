import Foundation

/// 上传传输：把一段事件发给服务端，返回水位应答；失败抛 `ActivityUploadError`。
typealias ActivityTransport = @Sendable (_ segmentID: String, _ events: [ActivityEvent]) async throws -> ActivityUploadAck

/// 上传失败分类：4xx（除 401）= 段本身有问题，重投无解 → 隔离；其余（网络/5xx/503/401）= 可重试。
enum ActivityUploadError: Error {
    case client(Error?)
    case retryable(Error?)

    var isClient: Bool {
        if case .client = self { return true }
        return false
    }
}

/// 客户端采集的编排者：把事件写进本地落盘队列，并周期性地按段搬运到服务端（设计篇 5.2）。
///
/// 生产者只调 `record(_:)`；`event_id`/`occurred_at`/客户端身份在这里补。
/// 采集开关由 app 层决定（本地模式才 `configure` + `start`；云端/ iOS 不启动）。
actor ActivityRecorder {
    private let store: ActivityStore
    private let clientInstanceID = ActivityIdentity.clientInstanceID
    private let clientBuild = ActivityIdentity.clientBuild
    private let osVersion = ActivityIdentity.osVersion

    private var transport: ActivityTransport?
    private var loop: Task<Void, Never>?

    /// 上传节拍（秒）。事件本地即时落盘，节拍只管什么时候把已封段搬走。
    private static let uploadIntervalSeconds = 30

    init(directory: URL) {
        self.store = ActivityStore(directory: directory)
    }

    /// 注入传输实现（token 就绪后由 app 层调用）。
    func configure(transport: @escaping ActivityTransport) {
        self.transport = transport
    }

    /// 启动周期搬运（幂等）。启动时立刻跑一轮，把上次遗留的段补投（离线缓冲）。
    func start() {
        guard loop == nil, transport != nil else { return }
        loop = Task { [weak self] in
            await self?.uploadNow()
            while !Task.isCancelled {
                try? await Task.sleep(for: .seconds(ActivityRecorder.uploadIntervalSeconds))
                await self?.uploadNow()
            }
        }
    }

    /// 停止搬运（登出/关闭采集）。
    func stop() {
        loop?.cancel()
        loop = nil
        transport = nil
    }

    /// 生产者入口：补公共字段后落盘。写失败只记日志，不影响业务（fire-and-forget）。
    func record(_ event: ActivityEvent) async {
        await record([event])
    }

    /// 批量入口：一次 actor 边界内按序落盘。成对/相邻事件（如 `system.sleep` + `boundary`）必须走它，
    /// 否则各起一个非结构化 `Task` 时理论可乱序，boundary 的 `occurred_at` 可能早于它要截断的事件。
    func record(_ events: [ActivityEvent]) async {
        let stamped = events.map { event -> ActivityEvent in
            var e = event
            if e.eventId.isEmpty { e.eventId = UUID().uuidString }
            if e.occurredAt.isEmpty { e.occurredAt = ActivityIdentity.isoMillis() }
            if e.clientInstanceId == nil { e.clientInstanceId = clientInstanceID }
            if e.clientBuild == nil { e.clientBuild = clientBuild }
            if e.osVersion == nil { e.osVersion = osVersion }
            if e.launchId == nil { e.launchId = ActivityIdentity.launchID }
            return e
        }
        guard !stamped.isEmpty else { return }
        do {
            try await store.append(stamped)
        } catch {
            await store.noteFailure("append \(stamped.map(\.type).joined(separator: "+")): \(error)")
        }
    }

    /// 封当前段并逐段搬运；成功删段，4xx 隔离，可重试错误停在本轮留着下次。
    func uploadNow() async {
        guard let transport else { return }
        do {
            _ = try await store.sealCurrent()
        } catch {
            await store.noteFailure("seal: \(error)")
            return
        }
        let segments: [URL]
        do {
            segments = try await store.pendingSegments()
        } catch {
            await store.noteFailure("list segments: \(error)")
            return
        }
        for url in segments {
            let segmentID = ActivityStore.segmentID(url)
            let events: [ActivityEvent]
            do {
                events = try await store.load(url)
            } catch {
                await store.noteFailure("load \(segmentID): \(error)")
                continue
            }
            if events.isEmpty {
                await store.remove(url)
                continue
            }
            do {
                _ = try await transport(segmentID, events)
                await store.remove(url)
            } catch let e as ActivityUploadError where e.isClient {
                await store.quarantine(url)
                await store.noteFailure("quarantine \(segmentID): \(e)")
            } catch {
                await store.noteFailure("upload \(segmentID): \(error)")
                break
            }
        }
    }
}
