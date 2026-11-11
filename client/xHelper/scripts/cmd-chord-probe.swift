// 「左右 Cmd 一起按」检测探针 —— 验证两件事：
//   1. `CGEventSource.keyState` 能不能在**没有辅助功能授权**的情况下读到左右 Cmd（键码 55 / 54）
//   2. `CGEventSource.flagsState` 同时按两个 Cmd 时给的是什么（只有 .maskCommand，分不出左右）
//
// 用法：xcrun swiftc -O -o /tmp/cmd-chord-probe client/xHelper/scripts/cmd-chord-probe.swift \
//         -framework AppKit && /tmp/cmd-chord-probe 60
//       跑起来后按一下左 Cmd、右 Cmd、再两个一起按。日志里看 L/R 两列。
//
// 输出只有状态跳变（不刷屏），同时打一行 AXIsProcessTrusted 说明当前授权状态。

import AppKit
import ApplicationServices
import CoreGraphics

let leftCmd: CGKeyCode = 55
let rightCmd: CGKeyCode = 54

let seconds = CommandLine.arguments.count > 1 ? (Double(CommandLine.arguments[1]) ?? 30) : 30

print("AXIsProcessTrusted = \(AXIsProcessTrusted())")
print("轮询 \(Int(seconds))s：请依次按 左Cmd → 右Cmd → 左右一起 → 松开。L/R 两列就是 keyState 的结果。")

let start = Date()
var lastL = false
var lastR = false
var lastFlags: UInt64 = .max

func stamp() -> String {
    String(format: "%6.2fs", Date().timeIntervalSince(start))
}

while Date().timeIntervalSince(start) < seconds {
    let l = CGEventSource.keyState(.combinedSessionState, key: leftCmd)
    let r = CGEventSource.keyState(.combinedSessionState, key: rightCmd)
    let flags = CGEventSource.flagsState(.combinedSessionState).rawValue
    if l != lastL || r != lastR || flags != lastFlags {
        print("\(stamp())  L=\(l ? 1 : 0)  R=\(r ? 1 : 0)  flags=0x\(String(flags, radix: 16))")
        fflush(stdout)
        lastL = l
        lastR = r
        lastFlags = flags
    }
    usleep(20_000) // 50Hz
}
print("结束")
