# 044 · SSE 块切在多字节字符中间丢事件，子任务白等 240s

## 问题表现

派 `task` 子任务后，子任务面板长时间停在原地（如「第 4 轮」），主对话没有新思考过程，约 4 分钟后才自己继续。实测一次聊天 `POST /api/chat` 持续 **629s**（10.5 分钟），其中 4 分钟完全静默。

## 根因

一条链路，两个缺陷叠加：

1. **客户端 SSE 解码按网络块做**（`SSEClient.didReceive data`）：
   `String(data:encoding:.utf8)` 对每块单独解码。URLSession 的块边界是任意的，会切在中文（3 字节）/ emoji（4 字节）中间 → 整块解码失败 → **整块被 `return` 丢掉**（无日志、无重试）。缺口落在 `event: client_tool_call` 或它的 `data:` 行上时，事件要么被 `currentEvent=""` 分发后 `default: return` 吞掉，要么 JSON 被撕断。
2. **服务端对客户端工具只等不重试**：`clientToolCaller.Call` 等满 `client_tool_timeout_secs`（240s）才返回超时。事件没到客户端时，服务端无从知晓，只能干等。

同时，等待期间服务端每 15s 发的心跳 `status: 正在等待客户端执行 fs.grep…` 被子任务面板的 `parseSubagentRound()` 过滤掉（解析不出「第 N/M 轮」= nil），界面一个像素都不动 —— 所以用户「不知道停在哪里」。

工具输出里真实存在的非法 UTF-8（见 dev 日志 `Error 1366 Incorrect string value: '\xE9' for column 'content'`）也会触发同样的整块丢弃，是同一 bug 的第二条触发路径。

## 证据（2026-10-31 云端）

`events.db`（只读）里 `tool.bridge.dispatched → returned` 耗时 240.0s 的 4 条记录，全部 `client.tool.exec.started` 缺失（客户端从没开始执行）：

```
240.0s tool=shell__run client_started=False
240.0s tool=fs__write  client_started=False
240.0s tool=fs__edit   client_started=False
240.0s tool=fs__grep   client_started=False
```

同一条 SSE 连接在超时之后仍能正常收到/执行后续 `shell.run`，排除断连与代理；服务端 `[clientToolCaller] client tool timeout after 4m0s` 与之一一对应。

## 修复

- 新增 `client/.../Shared/Services/SSELineDecoder.swift`：**按字节缓冲到完整行再解码**；行内合法 UTF-8 正常解，非法字节用 `String(decoding:as:)` 替换成 U+FFFD（不丢行），并回报 `hadInvalidUTF8` 供调用方记日志。`SSEClient` 改用它，删掉逐块解码与 `lineBuffer`。
- `ChatViewModel.handleSubagentEvent` 的 `status`：解析不出轮次时把内容写进 `latestAction`，心跳「正在等待客户端执行…」可见。
- 服务端 `clientToolCaller` 派发成功打一行 `dispatched call_id/tool/owner/turn`，便于和客户端 `client.tool.exec.started` 对账。
- 同一缺陷的第二处实例：`FileSystemToolProvider` 抓 `grep` 输出时也是逐块 `String(data:encoding:) ?? ""`（后果不是卡死，而是漏匹配 + 残行与下一块拼成假匹配），一并换成 `SSELineDecoder`。
- 第二条触发路径（工具输出里的非法 UTF-8）在服务端补齐：`normalizeClientToolData` 在**预算判定之后**做 `utf8.Valid` 检查并替换坏字节（放后面是因为替换会 1→3 字节变长，可能把刚好在预算内的结果顶过阈值、打出误导日志）。**注意 Go 的 `json.Valid` 不校验 UTF-8**，坏字节能混过原判断直达消息内容（MySQL 1366）。

**本次未覆盖**：根因二「服务端只等不重试、最长白等 240s」仍在。`dispatched` 日志只让问题可查，不保证送达。要根治需客户端 ack（收到 `client_tool_call` 即回一帧）或首段等待缩短 + 重发按 `call_id` 去重，单列跟进。

## 验证

- `swift test --filter SSELineDecoderTests`：含「把一条含中文+emoji 的 SSE 流按每一字节切开重放」与「非法字节只替换不丢行」，6 条全绿（另有 `\r` 留给调用方 trim 的分工用例）。
- `go test ./internal/agent/ -run 'NormalizeClientToolData|ClientTool|SubAgent'`：含「未超预算 + 非法 UTF-8」与「超预算 + 非法 UTF-8」（走截断分支，截断点可能切在字符中间）两条用例，输出都要求 `utf8.Valid` + `json.Valid`；`go build ./...`、`xcrun swiftc -typecheck`（Shared + mac 源集，带 `-default-isolation MainActor`）均通过：**本次改动文件 0 error 0 warning**（项目其余文件仍有存量 warning，与本次无关）。注意 `swift test` 不带 `-default-isolation MainActor`，**不能**用「测试全绿」代替这条检查（`SSELineDecoder` 漏标 `nonisolated` 就是这样漏掉的）。
- 真机复测：跑一次 `file_explore` 子任务，`events.db` 不应再出现 `dispatched→returned` 约 240s 且 `client_started=False` 的记录。

## 下次怎么快速定位

1. 远端只读查 `events.db`：`tool.bridge.dispatched` 与 `returned` 时间差 > 60s，再看该 `call_id` 有没有 `client.tool.exec.started`。
   - 没有 → 指令没到客户端（本 bug / 断连）；
   - 有但无 `client.tool.exec.ended` → 客户端工具执行本身卡住。
2. 服务端日志对 `[clientToolCaller] dispatched call_id=…`；客户端 release 日志看 Console 的 `clientToolCall` print。
