# 语音音色（设置页切换 TTS 音色）

> 状态：已实现（待真机验收）
> 分支：`feat/voice-chat`
> 关联：`docs/design/voice-chat/design.md`（C1 连续对话、C2b 句级朗读）、`docs/design/voice-hold/design.md`（按住说）
> 预览：`mockup.html`（本目录，单文件自包含，带控制条）

## 估算（定稿）

- 3 面（后端音色白名单与下发 / PWA 设置页与合成请求 / macOS 设置页与合成请求）
- 2 缝（试听与语音会话抢音频输出、本地音色失效回落）
- 2 次真机往返（PWA 手机浅色、macOS 设置窗）
- 0 个待拍决定
- 约 4–6 小时

## 背景

音色现在只有一处可配：

- `server/conf/config.yaml` 的 `voice.tts.speaker`（默认 `zh_female_vv_uranus_bigtts`，Vivi 2.0）；
- `server/internal/voice/tts.go` 每次合成把它写进 `req_params.speaker`；
- `GET /api/voice/config` 已经把 `speaker` 下发给客户端，但两端都只是存着，没有使用。

结果是想换音色只能改 yaml + 重启后端，且**没法试听**——音色是听感选择，看名字选不出来。

豆包语音合成 2.0（`resource_id: seed-tts-2.0`）官方开放 93 个中文音色，其中 S2S 主打的是
Vivi / 小何 / 云舟 / 小天 / 团子。全部铺进设置页对手机不友好，先精选 12 个覆盖男女声与风格。

## 目标

1. 设置里能切换音色，选中即保存，从**下一句**回复开始生效。
2. 能就地试听当前音色（复用现有 TTS 接口）。
3. PWA 与 macOS 行为、取值、失败处理逐项一致。
4. 不改 ASR / WS 协议，不改句级 TTS 流水线与 `SentenceCommitter`。

## 非目标

- 不做服务端用户级同步：音色存在客户端本地（和主题、字号、长回复提示音一致）。跨设备同步需要新增用户偏好表 + 迁移 + 读写接口，另立需求。
- 不做语速 / 音量 / 情感 / 语气标注参数（豆包支持，但这次只换音色）。
- 不做「恢复默认音色」入口：没选过就是默认，选过之后只能换。真要加，是在列表顶部加一项，语义会多一层。
- 不做每智能体音色。
- 不做全量 93 个音色的搜索列表：先 12 个，加一行 Go 代码就能扩充。

## 关键决策

| 决策 | 选择 | 理由 |
|---|---|---|
| 存储位置 | 客户端本地（macOS `UserDefaults` / PWA `localStorage`） | 与现有个人偏好一致；不用建表、不用迁移 |
| 音色列表 | 服务端白名单，随 `/api/voice/config` 下发 | 客户端不硬编码；合成参数在服务端有校验 |
| 取值路径 | 客户端每次合成请求带可选 `speaker` | 试听不用改服务端状态；旧客户端不带也能工作 |
| 非法音色 | **记日志并回落配置默认**，不返回 400 | 音色不是安全边界；音色下架时不该让整段朗读失败 |
| 试听 | 复用 `/api/voice/tts`，固定一句约 20 字 | 不新增接口，不新增计费链路 |
| 选择控件 | 行内下拉（A），不做弹层列表（B） | 换音色是低频操作；只多一行，不引入弹层/焦点管理 |
| 试听交互 | 整行可点，播放 → 合成中 → 停止 | 手机上 24px 圆钮太难点 |

## 数据流

```
设置页选音色 ──▶ 写本地（UserDefaults / localStorage）
                        │
每次合成 ── POST /api/voice/tts { text, speaker? }
                        │
                        ├─ speaker 为空 ──▶ 用 config 的 voice.tts.speaker
                        ├─ speaker 在白名单 ──▶ 用它
                        └─ speaker 不在白名单 ──▶ log + 回落默认
                        ▼
                  豆包 TTS ──▶ mp3 ──▶ 客户端按句播放（流水线不变）
```

`/api/voice/config` 额外返回 `speakers: [{id, name}]`。`speaker` 字段保留，语义变成「默认音色」。

## 后端设计

### 音色白名单（新增 `server/internal/voice/speakers.go`）

```go
type Speaker struct {
    ID   string `json:"id"`
    Name string `json:"name"`
}

// 豆包语音合成 2.0（*_uranus_bigtts），与 resource_id: seed-tts-2.0 绑定。
var curatedSpeakers = []Speaker{
    {"zh_female_vv_uranus_bigtts", "Vivi 2.0"},
    {"zh_female_xiaohe_uranus_bigtts", "小何 2.0"},
    {"zh_male_m191_uranus_bigtts", "云舟 2.0"},
    {"zh_male_taocheng_uranus_bigtts", "小天 2.0"},
    {"zh_female_tuanzi_uranus_bigtts", "团子 2.0"},
    {"zh_male_liufei_uranus_bigtts", "刘飞 2.0"},
    {"zh_female_sophie_uranus_bigtts", "魅力苏菲 2.0"},
    {"zh_female_qingxinnvsheng_uranus_bigtts", "清新女声 2.0"},
    {"zh_male_ruyaqingnian_uranus_bigtts", "儒雅青年 2.0"},
    {"zh_female_wenroushunv_uranus_bigtts", "温柔淑女 2.0"},
    {"zh_female_zhixingnv_uranus_bigtts", "知性女声 2.0"},
    {"zh_male_shenyeboke_uranus_bigtts", "深夜播客 2.0"},
}

// SpeakerList 返回给客户端的列表：精选 + 配置默认（若不在精选里，作为「默认（配置）」补在首位）。
func SpeakerList(defaultID string) []Speaker

// ResolveSpeaker 解析本次请求该用哪个音色：
//   requested == ""      → defaultID
//   requested 在白名单    → requested
//   defaultID 在白名单    → defaultID（同时给调用方一个 fallback 标记，用于打日志）
func ResolveSpeaker(defaultID, requested string) (speaker string, fallback bool)
```

- 白名单 = `curatedSpeakers` 的 ID ∪ `cfg.TTS.Speaker`。配置里填了复刻音色也能被选中，
  并在列表里显示为「默认（配置）」。
- 精选顺序保持稳定，Vivi 在第一位（当前默认）。

### TTS 客户端（`server/internal/voice/tts.go`）

`Synthesize(ctx, text string)` → `Synthesize(ctx, text, speaker string)`：

- `speaker == ""` 时仍用 `cfg.Speaker`（保持函数自身的健壮性，handler 已解析过）；
- 其余逻辑（计费、chunk 解码、20MB 上限）不变。

### Handler（`server/internal/controller/voice.go`）

`TTS` 请求体：

```json
{ "text": "……", "speaker": "zh_male_m191_uranus_bigtts" }
```

- `speaker` 可选；解析走 `voice.ResolveSpeaker(h.cfg.TTS.Speaker, req.Speaker)`。
- `fallback == true` 时 `log.Printf("[voice] unknown speaker %q, fallback to %q", ...)` 后继续合成。
  这是一次**有意的降级**：音色下架不应打断整段朗读。
- `Status` 响应增加 `speakers`（`voice.SpeakerList(h.cfg.TTS.Speaker)`）。

请求体继续 `MaxBytesReader 64KB`；空文本仍由 `Synthesize` 返回错误。

### 配置

`server/conf/config.yaml` 的 `voice.tts.speaker` 注释改为「默认音色（客户端未选择时使用）」。
不新增配置字段，不改加载逻辑。

### 测试

- `voice/tts_test.go`：`Synthesize` 传入 speaker 时请求体使用该值；传空时使用配置默认。
- `voice/speakers_test.go`（新增）：`ResolveSpeaker` 的三种分支；`SpeakerList` 在默认音色不在精选里时补首位且不重复。
- `controller/voice_test.go`：带 `speaker` 的请求透传到上游；未知 `speaker` 回落默认且仍 200；
  `Status` 返回的 `speakers` 包含配置默认。

## PWA 前端

### 本地偏好（新增 `front-end/src/utils/voicePref.js`）

照 `utils/chatFont.js` 的写法：

```js
const KEY = 'voice.speaker'

export function readVoiceSpeaker() {
  try { return localStorage.getItem(KEY) || '' } catch (e) { console.error(...); return '' }
}

export function setVoiceSpeaker(id) {
  try {
    if (id) localStorage.setItem(KEY, id)
    else localStorage.removeItem(KEY)
  } catch (e) { console.error(...) }
}
```

空串 = 没选过 = 用服务端默认。

### 设置页（`front-end/src/views/SettingsView.vue`）

新增分组「语音」，位置在外观之后、会话之前：

```
音色          [ Vivi 2.0        ▾ ]     ← 行内下拉，选中即存
试听当前音色                    ▶ 播放  ← 整行可点，播放 / 合成中 / 停止
只影响语音回复的朗读；切换后下一句生效。
```

- `onMounted` 并发拉 `/api/voice/config`（和 `loadArchived` 一起发，不串行等）。
  失败或 `ready=false` → 整组不渲染，不显示禁用态。
- 本地值 `readVoiceSpeaker()`；为空则展示 `cfg.speaker`，但**不回写**。
- 本地值不在 `cfg.speakers` 里 → 显示 `cfg.speaker`，`setVoiceSpeaker('')` 清掉，并在组下显示一行：
  `「深夜播客 2.0」已不可用，已切回 Vivi 2.0`（用 `--color-warning`）。提示只在本页本次显示。
- 选择控件用原生 `<select>`（iOS 直接弹系统选择器，零额外组件）；`.value` 只允许列表里的 id。
- 试听状态机：`idle | loading | playing`。
  - 点击：`stopPreview()` → `loading` → `api.postBlob('/api/voice/tts', { text: PREVIEW_TEXT, speaker })`
    → `new Audio(URL.createObjectURL(blob))` → `playing`（`onended` 回 `idle`）。
  - 再点 / 切换音色 / 离开页面（`onBeforeUnmount`）→ 停播 + `revokeObjectURL`。
  - 失败：`message.error('试听失败')`，回 `idle`，不改选择。
  - 固定文案 `PREVIEW_TEXT = '你好，我是你的聊天助手，今天过得怎么样？'`（约 21 字）。
- 不和朗读叠音：离开聊天页会走 `ChatView` 的 `onBeforeUnmount`（`cancelVoice` + `stopSpeaking`），
  进设置页时语音会话已经收掉，不需要额外处理。

### 合成请求（`front-end/src/composables/useVoiceChat.js`）

`freezeSentence` 里请求体加 `speaker`：

```js
const speaker = readVoiceSpeaker()
api.postBlob('/api/voice/tts', speaker ? { text: speech, speaker } : { text: speech })
```

空值时**不传字段**（少一个分支在服务端，也兼容旧后端）。其余句级流水线、断句、字幕跟随不动。

## macOS 客户端

### 偏好 key（`Shared/Services/LocalPaths.swift`）

在 `LongReplyAlert` 旁边加：

```swift
enum VoicePreference {
    static var speakerKey: String { LocalPaths.scoped("xhan_voice_speaker") }
}
```

### API（`Shared/Services/APIClient.swift`）

- `VoiceConfigInfo` 加 `let speakers: [VoiceSpeaker]?`（可选，旧后端没有该字段时整份配置仍能解码）；
  `VoiceSpeaker { let id: String; let name: String }`。
- `synthesizeSpeech(text: String)` → `synthesizeSpeech(text: String, speaker: String = "")`，
  只有非空时才把 `speaker` 放进 JSON body。

### 合成时读偏好（`Shared/Services/VoiceChatService.swift`）

`freezeSentence` 里调用处改为：

```swift
let speaker = UserDefaults.standard.string(forKey: VoicePreference.speakerKey) ?? ""
try await APIClient(...).synthesizeSpeech(text: speech, speaker: speaker)
```

在**每次合成时**读，而不是启动时缓存：设置窗与聊天窗可能同时开着，切换后下一句立即生效。

### 设置页（`client/xHelper/xHelperMac/App.swift` 的 `MacSettingsView`）

新增 `Section("语音")`（放在「外观」之后、「提醒」之前）：

```
音色        [ Vivi 2.0   ⌄ ]  (▶)
只影响语音回复的朗读；切换后下一句生效。
```

- `.task` 里 `apiClient.getVoiceConfig()`；失败或 `ready == false` 时整段不渲染（`if voiceReady`）。
- `@AppStorage(VoicePreference.speakerKey) private var storedSpeaker = ""`；
  `@State private var selectedSpeaker = ""` 与列表一起初始化：`storedSpeaker` 有效则用它，否则用 `cfg.speaker`。
- Picker 为 `.menu` 风格；选中后写 `storedSpeaker`。
- 试听按钮：`AVAudioPlayer(data:)` 播放，`@State private var previewPlayer` 持有；
  `play.fill` ↔ `stop.fill`；加载中禁用并显示 `ProgressView`。
  切换音色 / 关闭设置窗（`onDisappear`）停止并释放。
- 设置窗与聊天窗同时活着，所以试听前要先停回复朗读：在 `VoiceChatService.swift` 顶部定义
  `Notification.Name.voicePreviewWillStart`，`ChatView` 加 `.onReceive` 调 `voice.stopSpeaking()`，
  设置页在发试听请求前 post 一次。`App.swift` 顶部补 `import AVFoundation`。
- `storedSpeaker` 不在列表里 → 回落 `cfg.speaker`、清空 `storedSpeaker`、显示黄字提示。
- 设置窗可能在后端锁定时打开：请求失败就静默隐藏分组，`LogBuffer.shared.error` 记一条（可降级）。

## UI 规格

详见 `mockup.html`（PWA 手机 / PWA 宽屏 / macOS 示意；浅深色；正常 / 合成中 / 试听中 / 音色失效 / 未启用）。

| 元素 | 规格 |
|---|---|
| 分组位置 | 外观之后、会话之前；标题「语音」 |
| 音色行 | 左侧 label，右侧下拉（PWA 原生 `<select>` / macOS 原生 Picker） |
| 试听行 | 整行可点；右侧文字 + 图标：`播放` / `合成中…`（转圈）/ `停止`（跳动条） |
| 说明 | `只影响语音回复的朗读；切换后下一句生效。` 10.5–11pt，`--color-text-muted` |
| 失效提示 | 组下一行黄字：`「X」已不可用，已切回 Y` |
| 未启用 | 整组不渲染（macOS 为 Section 不渲染） |
| 颜色 | 只用现有 token；不新增色相 |

PWA 主验收形态：**手机 + 浅色 + 行内下拉**。

## 边界与失败处理

| 情况 | 行为 |
|---|---|
| 从没选过 | 请求不带 `speaker`，服务端用 `voice.tts.speaker` |
| 本地存的 id 已不在列表 | 回落默认 + 清本地 + 黄字提示（本次） |
| 请求里的 id 不在白名单（如列表更新前打开的旧页面） | 服务端记日志并回落默认，仍返回音频 |
| 合成到一半切换音色 | 当前句不重合成；下一句用新音色 |
| PWA 试听时正在朗读 | 进设置页已 unmount 聊天页，语音会话自动收掉，不会叠音 |
| macOS 试听时正在朗读 | 试听前 post `voicePreviewWillStart`，聊天窗 `stopSpeaking()` 后再播 |
| 试听失败（未开通 / 网络） | 就地提示，保持原选择，不改音色 |
| 后端未启用语音 / 配置拉取失败 | 整组不渲染，不报错弹窗 |
| 旧客户端不带 `speaker` | 继续用默认，行为不变 |
| 配置默认是复刻音色 | 列表首位显示「默认（配置）」，可被选中 |

## 测试与验收

- 后端：`go build ./... && go test ./...`，含上述新增/修改用例。
- PWA：`npm run build`、`npm test`（`voicePref` 的读写与空值回落加一个小单测）。
- 真机（PWA 手机浅色为主）：
  - 选音色 → 回聊天开语音 → 回复用新音色，且只影响语音轮的朗读；
  - 试听：播放、再点停止、切音色自动停、离开设置页停止；
  - 未启用时设置页不出现「语音」组；
  - 320px 窄窗下拉与试听行不换行、不溢出。
- 真机（macOS）：
  - ⌘, 里切音色 → 下一句生效；
  - 播放/停止按钮状态正确，关闭设置窗后声音停止；
  - 多开设置窗不叠加播放。
- 回归：不选音色时，行为与现在完全一致（请求不带 `speaker` → 配置默认）。

## 风险与已知取舍

- **跨设备不同步**：Mac 和手机各选各的。这是本地存储的代价，且与现有偏好一致；要同步另立需求。
- **音色开通情况不可预知**：12 个音色是否在账号下可用，只有真发一次 TTS 才知道；试听按钮同时也是可用性检查。某个音色未开通时，试听会报错，但不会静默切走。
- **试听计费**：每次约 20 字，可忽略；没有做缓存或频率限制。
- **PWA 原生 `<select>` 的样式受限**：iOS 上用系统选择器，桌面端外观随浏览器；这是为了少写一个弹层组件。真机若觉得丑，再换 `NSelect`。
- **两路音频**：进入设置页强制停朗读是客户端约定；若语音服务状态机在后台重新拉起朗读，需要真机确认（见 `docs/bugs/` 相关记录）。

## 影响文件

- 新增：`server/internal/voice/speakers.go`、`server/internal/voice/speakers_test.go`、`front-end/src/utils/voicePref.js`
- 修改：`server/internal/voice/tts.go`、`server/internal/controller/voice.go`、`server/internal/controller/voice_test.go`、
  `server/conf/config.yaml`（仅注释）、`client/xHelper/xHelper/Shared/Services/LocalPaths.swift`、
  `client/xHelper/xHelper/Shared/Services/APIClient.swift`、`client/xHelper/xHelper/Shared/Services/VoiceChatService.swift`、
  `client/xHelper/xHelper/Shared/Views/ChatView.swift`（新增通知监听）、
  `client/xHelper/xHelperMac/App.swift`（import AVFoundation + 语音 Section）、
  `front-end/src/views/SettingsView.vue`、`front-end/src/composables/useVoiceChat.js`
- 文档：本文件、`docs/design/voice-chat/design.md`（尾部追加变更记录）

## 后续变更

- 2026-11-09（落地）：
  - 后端新增 `internal/voice/speakers.go`（12 个精选 2.0 音色 + `SpeakerList` / `ResolveSpeaker`），
    `Synthesize` 增加 `speaker` 参数，`/api/voice/tts` 收可选 `speaker`（非法 id 记日志回落默认），
    `/api/voice/config` 增发 `speakers`。
  - PWA 新增 `utils/voicePref.js`（localStorage `voice.speaker`），设置页加「语音」分组与试听，
    句级合成请求带 `speaker`。
  - macOS 新增 `VoicePreference.speakerKey`，`VoiceConfigInfo` 增可选 `speakers`，
    `synthesizeSpeech(text:speaker:)`，设置页加「语音」Section + `AVAudioPlayer` 试听；
    试听前 post `voicePreviewWillStart` 让聊天窗先停朗读。
  - 与方案的一处差异：失效提示不显示已下架的 id（客户端只存 id、不存名字），文案为
    「已选音色不可用，已切回 X」。
