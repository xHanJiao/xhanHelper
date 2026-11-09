# 语音声纹（只响应我的声音）设计

> 状态：讨论稿，**结论：开发成本大、收益不明确，暂不实现**（2026-11-09）
> 分支：`feat/voiceprint-probe`
> 关联：`docs/research/voiceprint-probe.md`（豆包 `ssd_mode=2` 实测）、`docs/design/voice-chat/design.md`（语音链路）、`docs/design/voice-hold/design.md`（按住说）
> 探针代码：`server/internal/voice/asr.go`、`server/internal/config/voice.go`、`server/cmd/voice-smoke/main.go`（已改，未提交）

## 结论（2026-11-09）

**技术上可行，但开发成本大、收益不明确，暂不实现。**

- 探针证明 `ssd_mode=2` 可用，本人/他人能稳定区分；但完整落地不是“加个参数”：
  设置开关、每设备录入、云端中转、设备标签、打断认声纹、跨设备准确率、误拒恢复，串起来约 5–7 天。
- 为了“打断也认声纹”还要引入第二个厂商（Picovoice Eagle）和授权；不引入就只能接受约 1 秒的打断延迟。
- 收益主要是“避免 CJ/背景人声被提交、被当成一轮”，而现有的**按住说**已经能以接近 0 的成本覆盖大部分场景：
  按住时才提交，松手前不打断；CJ 说话不会进入暂存。
- 剩余收益（完全免手、只认本人）不足以支撑上面的成本和新风险（公网 relay、跨设备误拒、声纹生物特征、
  豆包未公开参数未来变更）。
- 决定：**先不做**。保留探针代码、实测记录和本设计稿；如果以后“按住说”仍不够用，再回来按本文档实施。

## 估算（开工）

- 12 面 / 5 缝（2 条需先建探针）/ 3 次真机往返 / 5 个待拍决定
- C1（端侧 Eagle + 压低音量）：约 5–7 天
- C2（云端验证）：约 3–5 天

## 背景

现有连续语音对话里，豆包 ASR 按 800ms 静音判停，客户端收到 `sentence` 就**直接提交一轮**。
屋里其他人（CJ）说话、电视人声、背景人声都会被当成用户输入；macOS 的“开口打断”又只看音量，
CJ 大声说话还会把 TTS 停掉。

豆包流式 ASR 自带「短音频声纹识别（`ssd_mode=2`）」，官方定位就是“AI 认主”：
传入一个已注册声纹（或声纹组），每句 `definite` 结果带 `speaker_id`。匹配成功返回注册时的
`SpeakerName`，未匹配返回普通说话人分离 ID（`"1"` / `"2"`）。

## 目标

1. 设置里可开关「只响应我的声音」。
2. 录入时标记设备（MacBook 内置麦 / iPhone / 耳机等），同一用户可录多台。
3. 开启后：只有已录入设备的声音会提交成一轮；其他人的话丢弃，不打断、不提交。
4. 关闭后：完全回到现状（不传声纹参数）。
5. 打断（barge-in）前也认一次声纹，避免 CJ 停朗读。

## 非目标

- 不做声纹降噪（把别人的声音从音频里抹掉）；音频照常转写，只是不提交。
- 不做多用户共享设备、按说话人个性化；当前是单用户场景。
- 不改 ASR/TTS 供应商；仍然走豆包。
- 不做 PWA 的自动打断（浏览器 AEC 不可靠，现状就是手动打断）。

## 已确认事实

### 探针（详见 `docs/research/voiceprint-probe.md`）

- `ssd_mode=2` + `enable_speaker_info=true` + `voiceprints` 可用。
- 匹配返回 **`SpeakerName`**，不是 SpeakID；未匹配返回 `"1"` / `"2"`。
- **必须同时开 `enable_speaker_info=true`**，否则本人也返回空串。
- 声纹组可用：`voiceprints:[{"id":"","group_id":"..."}]`，组内成员都能匹配。
- 注册 API 返回 HTTP 307，需跟 `-L`；响应字段 `Result.SpeakID`（大写 D）。
- 注册接口**只接受 `AudioUrl`**；`AudioBase64` 实测返回 `InvalidParameter`，公网 URL 绕不开。

### 计费

- 豆包流式语音识别模型 2.0（`volc.seedasr.sauc.duration`）：后付费 **1 元/小时**；
  预付费 30 小时 28 元（约 0.93 元/小时）。
- 豆包语音合成模型 2.0：后付费 **3 元/万字符**；预付费 10 万字 28 元。
- 官方计费页**没有单列**“声纹识别/说话人分离”，按文档看含在 ASR 时长费里；
  扣子（Coze）的“声纹识别 0.005 元/次”是另一个产品，不适用。
- 声纹不省 ASR 钱：CJ 的话照样转写、照样按时长计费，只是不提交。
- 注意：`docs/design/voice-chat/design.md` 里写的“ASR ≈ 4.4 元/小时”是旧的大模型流式识别价格，
  当前 2.0 小时版是 1 元/小时，后续做实现时顺手更正。

## 已拍板

- **录入走云端中转（B）**：本地 Mac 录音 → 本地 server → 云端 relay → 豆包。
- **打断前认声纹（C）**：具体 C1/C2 待定（见下）。
- 设备名自动带出当前输入设备，可手动改。
- 开关关闭 = 完全回到现状。
- 被忽略时不回传文本，只发一个不带文本的 `ignored` 事件。

## 待拍板

| 编号 | 决策 | 选项 | 建议 |
|---|---|---|---|
| D1 | 开关范围 | 全局（服务端 DB）/ 每台设备（客户端本地） | **每台设备**：本地和云端是两个 DB，全局不会真同步；每台设备更简单、更灵活 |
| D2 | 打断验证 | C1 端侧 Eagle + 压低音量 / C2 云端验证 / C3 按住才允许打断 | **C1**：唯一能把延迟压到 100–300ms 的方案；代价是 Picovoice 授权 |
| D3 | 设备列表 | 云端 DB（跨端一致）/ 仅本机 | **云端 DB**：Mac 和 PWA 看到同一份列表 |
| D4 | relay 鉴权 | 共享 token（`X-Relay-Token`） | 是 |
| D5 | 被忽略提示 | 轻提示 + 一键临时关闭 / 只日志 | **轻提示 + 一键关闭**：误拒时用户知道发生了什么 |

## 架构

### 录入（B：云端中转）

```
Mac 设置页录 10s PCM（16k/16bit/mono）
  → 本地 server  POST /api/voice/voiceprint/devices
  → 云端 relay    POST /api/voice/voiceprint/relay/enroll（X-Relay-Token）
  → 云端把 wav 挂到临时公开 URL（随机 token，5 分钟 TTL）
  → 豆包 UpdateVoiceprint(Action=0, GroupId, SpeakerName=owner-xxx, AudioUrl)
  → 返回 SpeakID / SpeakerName / GroupId
  → 云端写 VoiceprintDevice（设备标签）
  → 本地 server 缓存 GroupId，返回客户端
```

- 云端是唯一持有公网 URL 的地方；本地只负责录音和转发。
- 注册完立刻删临时 wav；不把音频落库。
- 云端 relay 限制：单次 ≤ 2MB、只接受 16k/16bit/mono PCM、不记音频内容。

### 对话（服务端过滤）

```
客户端 POST /api/voice/asr-ticket {voiceprint:true}（或 WS query）
  → WS /api/voice/asr?ticket=...
  → 服务端读用户 GroupId + 设备名集合
  → DialASR(ssd_mode=2, enable_speaker_info=true, voiceprints:[{group_id}])
  → 每条 definite utterance:
        speaker_id ∈ owner-*  → Commit + 下发 sentence
        否则                  → 丢弃 + 下发 {"type":"ignored"}
```

- `ssd_mode=2` 时 `DialASR` 强制 `EnableSpeakerInfo=true`（已落代码）。
- 关闭声纹：请求不带 `ssd_mode` / `voiceprints`，回到现状。
- `voice` 事件（partial 有变化）仍然照发：partial 没有 speaker_id，用于空闲收口顺延；
  CJ 说话会延长会话，但不会提交。

## 数据与接口草案

### 云端 DB

```
VoiceprintGroup
  user_id      PK
  group_id     豆包声纹组 ID
  created_at

VoiceprintDevice
  speak_id     豆包 SpeakID（PK）
  user_id
  speaker_name 注册时的 SpeakerName（ASCII，如 owner-mac）
  device_label 中文显示名（如 MacBook 内置麦克风）
  created_at
```

- 组首次使用时 `CreateVoiceprintGroup(GroupName=xhan-<user_id>)`，GroupId 存 `VoiceprintGroup`。
- 设备标签只存云端；本地 server 通过 relay 拉列表。
- 本地 DB 只需缓存 `group_id`（可复用同一张 `VoiceprintGroup` 表），用于 ASR 建连。
- 如果本地还没有 GroupId：首次开启声纹时调 `GET /api/voice/voiceprint/relay/config` 拉取并缓存。

### 接口

| 端 | 方法/路径 | 说明 |
|---|---|---|
| 云端 relay | `POST /api/voice/voiceprint/relay/enroll` | `{pcm_base64, sample_rate, device_label}` → `{speak_id, speaker_name, group_id}`；`X-Relay-Token` |
| 云端 relay | `GET /api/voice/voiceprint/relay/devices` | 设备列表（只返回 speak_id / speaker_name / label / created_at） |
| 云端 relay | `DELETE /api/voice/voiceprint/relay/devices/:speak_id` | 删除声纹（`Action=2`）+ 删行 |
| 云端 relay | `GET /api/voice/voiceprint/relay/config` | 返回 `group_id`（本地首次同步用） |
| 本地 | `GET /api/voice/voiceprint/devices` | 代理云端列表 |
| 本地 | `POST /api/voice/voiceprint/devices` | 代理录入 |
| 本地 | `DELETE /api/voice/voiceprint/devices/:speak_id` | 代理删除 |

### 开关

- 建议存客户端本地：macOS/iOS `UserDefaults`、Web `localStorage`。
- 开语音会话时带 `voiceprint=1`（ticket 请求体或 WS query）。
- 服务端不存开关；只按请求决定是否带声纹参数。
- 如果最终选全局，则改为 `UserVoiceprintSetting { user_id, enabled }` + 设置 API，ASR 建连时读 DB。

## 服务端过滤细节

当前 continuous 逻辑（`server/internal/controller/voice.go`）是：

```go
text := committer.Commit(result)
if text == "" { return }
send(sentence)
```

声纹开启后改为按分句处理：

```go
for _, u := range result.Utterances {
    if !u.Definite || u.Text == "" { continue }
    if !isOwner(u.SpeakerID) { ignored++; continue }
    if text := committer.Commit(ASRResult{
        Text: u.Text, Definite: true, EndTime: u.EndTime,
    }); text != "" {
        send(gin.H{"type": "sentence", "text": text})
    }
}
```

- `isOwner`：`speaker_id` 在该用户的设备名集合里（或按 `owner-` 前缀）。
- 非本人：不提交、不打断、不发文本；只发 `{"type":"ignored"}`。
- `ignored` 限频（例如 10 秒一次），避免 CJ 一直说话时刷屏。
- 关闭声纹 / 未录入设备：走原逻辑。
- 单元测试：分句过滤、混合说话人分句、ignored 限频、关闭时行为不变。

## 客户端 / UI

### Web（`front-end/src/views/SettingsView.vue`）

- 「声纹」卡片：开关 + 设备列表 + 「录入新设备」+ 隐私说明。
- 录入弹层：设备名（`enumerateDevices()` 预填，可改）、10 秒录音 + 电平/倒计时、重录、保存。
- 复用 `voice-pcm-worklet.js` 采集 16k PCM；单独一个 `useVoiceprintRecorder`，不复用连续对话的状态机。

### macOS / iOS（`client/xHelper/.../SettingsView.swift`）

- 同样的「声纹」卡片，走 `SettingsCard` 风格。
- 设备名：macOS `AVCaptureDevice.default(for: .audio)?.localizedName`；iOS `AVAudioSession.currentRoute.inputs.first?.portName`。
- 录入：`AVAudioEngine` tap → 16k/16bit/mono PCM，10 秒；不复用 `VoiceAudioEngine` 的播放/门控逻辑。
- 设备列表从本地 server 拉（server 再代理云端 relay）。
- 关闭开关 / 误拒时给一个「临时关闭声纹」入口。

### 被忽略提示

- 语音条上轻提示「已忽略不是你的声音」，限频。
- 不显示 CJ 的文本，不写日志内容。
- 服务端日志只记计数和原因（`speaker_mismatch`），不记文本。

## 打断（C）

### 为什么不能直接用豆包声纹

豆包 ASR 的 `definite` 大约在用户停止说话后 1 秒才出（800ms 判停 + 两遍识别）。
如果等它回来再决定停不停 TTS，用户打断后 AI 还会多念约 1 秒，手感明显变差。
所以打断需要**端侧、低延迟**的声纹。

### C1：端侧 Eagle + 压低音量（建议）

1. 音量触发打断候选 → 先把 TTS 音量压到 20%（不直接停）。
2. 用 Picovoice Eagle 对最近约 1 秒音频打分（约 100–300ms）。
3. 是本人 → 停朗读 + 走现有 barge-in 流程；不是 → 恢复音量，继续念。

- Eagle 跑在本地，音频不出设备。
- 录入时同一段 10 秒音频：一份注册豆包声纹（云端组），一份生成 Eagle 本地 profile
  （Keychain / UserDefaults，按 `speak_id` 存）。
- 只在有自动打断的客户端需要（macOS/iOS）；PWA 不涉及。
- 代价：Picovoice 账号/AccessKey + 授权；Eagle SDK 集成；约 +2–4 天。
- 注意：TTS 漏音可能进麦克风；VPIO AEC 会压，且 TTS 音色与本人不同，理论上不会误判，需真机验证。

### C2：云端验证（不加客户端 SDK）

- 音量触发 → 压低音量 → 把最近 1 秒 + 后续音频发到服务端 → 复用豆包声纹判断 → 停/恢复。
- 延迟约 0.6–1 秒；后端多一个短音频验证接口和音频缓存；约 +1–2 天。
- 优点：不加新厂商；缺点：手感比 C1 慢。

### C3：按住才允许打断

- 不做声纹，只有按住说/空格时才允许打断。最省，但不是免手。

## 计费

| 用量 | ASR 时长/月 | ASR 费 | TTS 假设 | TTS 费 | 合计 |
|---|---|---|---|---|---|
| 轻度：每天 10 分钟 | 5 小时 | 5 元 | 每天 10 轮 × 120 字 = 3.6 万字 | 10.8 元 | ~16 元/月 |
| 中度：每天 30 分钟 | 15 小时 | 15 元 | 每天 30 轮 = 10.8 万字 | 32.4 元 | ~47 元/月 |
| 重度：每天 1 小时 | 30 小时 | 30 元 | 每天 60 轮 = 21.6 万字 | 64.8 元 | ~93 元/月 |

- 声纹本身没有单列费用；以账单为准。
- 声纹不降低 ASR 费用；要省只能缩短会话或客户端不发音频。

## 风险与未验证

- **真实人声/远场**：探针是合成音色；真人 0.5–1m、不同麦克风/耳机、感冒等音色变化下的误拒率未测。
- **误接受**：CJ 的声音被认成本人；只能靠注册质量和阈值，豆包 `ssd_mode=2` 没有暴露阈值参数。
- **跨设备**：官方建议注册与使用同设备；多设备必须用声纹组，各录一份。
- **`SpeakerName` 约束**：字符集/长度未确认；建议 ASCII slug（`owner-mac`、`owner-iphone`），中文标签只做显示。
- **云端 relay**：新增公网入口，必须 token 保护、限大小、限 TTL、不记音频；本地离线时录入不可用。
- **本地/云端 GroupId 同步**：本地首次使用需要从 relay 拉取；relay 不可用时降级为“声纹关闭”。
- **Eagle 授权**：AccessKey/授权方式、模型体积、macOS/iOS 集成成本未确认；个人免费额度/商用授权需先查。
- **打断延迟**：C1 的“压低音量 → 验证 → 停”手感必须真机调；C2 约 1 秒，可能不可接受。
- **豆包接口稳定性**：`ssd_mode` / `voiceprints` 只在“最佳实践”文档出现，不在双向流式 API 参考参数表；
  已实测可用，但未来可能变更。
- **计费**：声纹是否永久不单独计费未确认；上线后看一两天账单。
- **隐私**：注册音频会传到豆包（转 PCM 存 TOS）；Eagle profile 存在本地。属于生物特征，需在设置页说明。

## 实施顺序（已暂缓，保留备查）

1. **服务端过滤 MVP**：配置 GroupId + 设备名集合，只过滤 `sentence`，不改 UI；真机验证 CJ 被丢。
2. **云端 relay + 本地录入**：relay 鉴权、临时 URL、设备标签、本地代理。
3. **设置页**：Web → macOS/iOS；开关（每台设备）+ 设备列表 + 录入弹层。
4. **打断 C1**：Eagle 接入 + 压低音量 + 真机调延迟；或按拍板选 C2。
5. **真机验收**：本人/远场/不同设备/误拒/误接受/打断手感。

## 后续变更

- 2026-11-09：讨论稿建立。已确认探针结论、计费口径、B 云端中转、C 打断方向；
  D1（开关范围）、D2（C1/C2）、D3（设备列表跨端）待拍板。
- 2026-11-09：**结论改为“暂不实现”**。理由：完整落地约 5–7 天，且要引入云端 relay / 第二个厂商 /
  跨设备准确率等新风险；现有“按住说”已覆盖大部分收益，声纹的边际收益不明确。
  探针代码与本文档保留，后续需要时再回来。
