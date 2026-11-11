import Foundation

/// `VoiceAudioEngine` 的设备所有权租约。
///
/// 引擎是进程级单例（一个 `AVAudioEngine`、一份 VPIO），而 VPIO 是**设备级**资源：
/// 按实例记账会互相踩 —— 一份实例空闲 8 秒后关 VPIO，会把另一份正在采集的引擎一起停掉
/// （见 `docs/bugs/063`）。所以采集 / 播放 / 语音条 pin 都必须显式登记 owner，
/// 判断只走这一处。
///
/// 纯逻辑、不依赖 AVFoundation：`swift test` 直接覆盖（Tests/VoiceAudioLeaseCoreTests）。
struct VoiceAudioLease {
    enum CaptureBegin: Equatable {
        /// 新的采集开始
        case started
        /// 同一个 owner 重复开：幂等（沿用「capture start ignored」语义）
        case alreadyMine
        /// 别人正在采：后到者被拒
        case busy
    }

    private(set) var captureOwner: ObjectIdentifier?
    private(set) var playbackOwner: ObjectIdentifier?
    private var voiceBarOwners: Set<ObjectIdentifier> = []

    /// 语音条是否有任何持有者（多 owner 做 OR：ptt 退出不能清掉视图的 pin）
    var voiceBarOpen: Bool { !voiceBarOwners.isEmpty }

    /// 设备空闲：可以关 VPIO（不关会一直压着整机音量，见 docs/bugs/057）
    var canReleaseDevice: Bool {
        captureOwner == nil && playbackOwner == nil && !voiceBarOpen
    }

    // MARK: 采集（独占）

    mutating func beginCapture(_ owner: ObjectIdentifier) -> CaptureBegin {
        if let current = captureOwner {
            return current == owner ? .alreadyMine : .busy
        }
        captureOwner = owner
        return .started
    }

    /// 返回 false 表示不是自己的采集，调用方不得动设备
    mutating func endCapture(_ owner: ObjectIdentifier) -> Bool {
        guard captureOwner == owner else { return false }
        captureOwner = nil
        return true
    }

    // MARK: 播放（抢占）

    /// 登记新播放，返回被抢占的上一个 owner（同一 owner 重播不算抢占）
    mutating func beginPlayback(_ owner: ObjectIdentifier) -> ObjectIdentifier? {
        let previous = playbackOwner == owner ? nil : playbackOwner
        playbackOwner = owner
        return previous
    }

    /// 返回 false 表示不是自己的播放，调用方不得停别人的音频
    mutating func endPlayback(_ owner: ObjectIdentifier) -> Bool {
        guard playbackOwner == owner else { return false }
        playbackOwner = nil
        return true
    }

    // MARK: 语音条 pin（多 owner）

    mutating func setVoiceBar(_ owner: ObjectIdentifier, open: Bool) {
        if open {
            voiceBarOwners.insert(owner)
        } else {
            voiceBarOwners.remove(owner)
        }
    }
}
