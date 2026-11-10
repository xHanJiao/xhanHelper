import AVFoundation
import CoreAudio
import Foundation

/// VPIO 释放探针：判定「关掉 VPIO 之后设备能不能原地还原」。
/// 这决定修法是「会话结束时关 VPIO」还是「整只丢掉 engine」。
///
/// 用法：
///   xcrun swiftc -o /tmp/vpio-probe client/xHelper/scripts/vpio-release-probe.swift \
///     -sdk "$(xcrun --show-sdk-path --sdk macosx)" -target arm64-apple-macos26.3
///   /tmp/vpio-probe
///
/// 输出里只要 `关掉 vpio 后重开 engine: 成功` 且 `第二轮 重新开 vpio + 启动 engine: 成功`，
/// 就走最小改（空闲关 VPIO）。注意内置扬声器的 nominal 采样率本来就是 44100，
/// **不能拿它当「设备被改坏」的证据**；若关不掉或重开失败，才得「会话结束销毁 engine」。

func defaultOutputNominalRate() -> Float64? {
    var addr = AudioObjectPropertyAddress(
        mSelector: kAudioHardwarePropertyDefaultOutputDevice,
        mScope: kAudioObjectPropertyScopeGlobal,
        mElement: kAudioObjectPropertyElementMain)
    var device = AudioDeviceID(0)
    var size = UInt32(MemoryLayout<AudioDeviceID>.size)
    guard AudioObjectGetPropertyData(
        AudioObjectID(kAudioObjectSystemObject), &addr, 0, nil, &size, &device) == noErr
    else { return nil }

    var rateAddr = AudioObjectPropertyAddress(
        mSelector: kAudioDevicePropertyNominalSampleRate,
        mScope: kAudioObjectPropertyScopeGlobal,
        mElement: kAudioObjectPropertyElementMain)
    var rate = Float64(0)
    var rateSize = UInt32(MemoryLayout<Float64>.size)
    guard AudioObjectGetPropertyData(device, &rateAddr, 0, nil, &rateSize, &rate) == noErr
    else { return nil }
    return rate
}

func report(_ label: String) {
    let rate = defaultOutputNominalRate().map { "\(Int($0))Hz" } ?? "?"
    print("[\(label)] output=\(rate)")
}

func run() {
        report("before")
        let engine = AVAudioEngine()
        let input = engine.inputNode
        let output = engine.outputNode
        // 和 app 一样：播放节点挂在 mainMixer 上（探「有连线时能不能关掉再开」）
        let player = AVAudioPlayerNode()
        engine.attach(player)
        engine.connect(player, to: engine.mainMixerNode,
                       format: AVAudioFormat(standardFormatWithSampleRate: 24_000, channels: 1))

        do { try input.setVoiceProcessingEnabled(true) }
        catch { print("input vpio 失败: \(error.localizedDescription)") }

        var outputVPIO = true
        do { try output.setVoiceProcessingEnabled(true) }
        catch {
            outputVPIO = false
            print("output vpio 失败: \(error.localizedDescription)")
        }
        print("vpio: input=\(input.isVoiceProcessingEnabled) output=\(output.isVoiceProcessingEnabled) (设置了 output=\(outputVPIO))")

        engine.prepare()
        do { try engine.start() }
        catch {
            print("engine.start 失败: \(error.localizedDescription) — 无法复现，退出")
            exit(3)
        }
        Thread.sleep(forTimeInterval: 2)
        report("running")

        engine.stop()
        Thread.sleep(forTimeInterval: 1)
        report("stopped")

        do { try input.setVoiceProcessingEnabled(false) }
        catch { print("关 input vpio 失败: \(error.localizedDescription)") }
        if outputVPIO {
            do { try output.setVoiceProcessingEnabled(false) }
            catch { print("关 output vpio 失败: \(error.localizedDescription)") }
        }
        Thread.sleep(forTimeInterval: 1)
        report("disabled")

        engine.prepare()
        var restart = "成功"
        do { try engine.start() } catch { restart = "失败: \(error.localizedDescription)" }
        print("关掉 vpio 后重开 engine: \(restart)")
        engine.stop()

        // 第二次会话：重新开 VPIO 能否还能用（真机生命周期就是 开会话→释放→再开会话）
        do {
            try input.setVoiceProcessingEnabled(true)
            try output.setVoiceProcessingEnabled(true)
            engine.prepare()
            try engine.start()
            Thread.sleep(forTimeInterval: 1)
            print("第二轮 重新开 vpio + 启动 engine: 成功")
            engine.stop()
            do {
                try input.setVoiceProcessingEnabled(false)
                try output.setVoiceProcessingEnabled(false)
            } catch { print("第二轮 释放失败: \(error.localizedDescription)") }
        } catch {
            print("第二轮 重新开 vpio 失败: \(error.localizedDescription)")
        }

        report("final(进程还活着)")
}

run()
