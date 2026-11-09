# 声纹探针：豆包流式 ASR `ssd_mode=2` 实测

> 日期：2026-11-09
> 分支：`feat/voiceprint-probe`
> 工具：`server/cmd/voice-smoke`（新增 `-speaker-info` / `-ssd-mode` / `-voiceprint-id` / `-voiceprint-group`）
> 样本：`say` 合成的两个不同中文音色（Tingting / Rocko），16k/16bit/mono wav

## 结论

1. **`ssd_mode=2` 可用**。同一段两人对话里，注册过的声音返回注册时的 `SpeakerName`，另一个人返回分离 ID（`"1"` / `"2"`）。
2. **匹配结果不是 SpeakID，是注册时的 `SpeakerName`**。过滤条件必须按名字比，不能按声纹 ID 比。
3. **必须同时开 `enable_speaker_info=true`**。只传 `ssd_mode=2` 时，即使声音匹配，`speaker_id` 也恒为空。
4. **未匹配不报错**：会退回普通说话人分离，返回 `"1"` / `"2"` 这类 ID；实现里除注册名以外的值一律当“不是本人”丢弃。
5. **声纹组可用**：`voiceprints:[{"id":"","group_id":"..."}]`，组内成员的 `SpeakerName` 同样会返回。多设备（Mac / iPhone）可以各注册一份进同一个组。
6. 注册 API 返回 **HTTP 307**，必须跟 `-L`；响应字段是 `Result.SpeakID`（大写 D），成功码 `1000`。
7. **注册只接受 `AudioUrl`**：传 `AudioBase64` 实测返回 `InvalidParameter`（`AudioURL and SpeakerName are required`），公网 URL 绕不开。

## 实测记录

| 请求 | 结果 |
|---|---|
| `-speaker-info`，两人拼接 wav | 第一人 `speaker="1"`，第二人 `speaker="2"` |
| `-speaker-info -ssd-mode 2 -voiceprint-id <真实ID>`，本人 wav | `speaker="probe-owner"` |
| 同上，另一人 wav | `speaker="1"` |
| 同上，两人拼接 wav | 本人段 `speaker="probe-owner"`，另一人段 `speaker="1"` |
| `-ssd-mode 2 -voiceprint-id <真实ID>`（**不开** speaker-info），本人 wav | `speaker=""` |
| `-speaker-info -ssd-mode 2 -voiceprint-id <假ID>`，本人 wav | `speaker="1"`（退回分离，不报错） |
| `-ssd-mode 2 -voiceprint-group <组ID>`，本人 / 另一人 wav | `speaker="probe-owner"` / `speaker="1"` |

## 复现命令

```bash
# 注册（AudioUrl 必须是公网可下载的 wav/pcm/mp3，5–30 秒）
curl -sS -L -X POST 'https://openspeech.bytedance.com/api/proxy/invoke?Action=UpdateVoiceprint' \
  -H "X-Api-Key: $DOUBAO_VOICE_API_KEY" -H 'Content-Type: application/json' \
  -d '{"Action":0,"AudioUrl":"https://<公网>/owner.wav","SpeakerName":"owner"}'

# 验证（本人 / 另一人各跑一次）
cd server
go run ./cmd/voice-smoke -wav owner.wav -result-type single -speaker-info \
  -ssd-mode 2 -voiceprint-id <SpeakID>
go run ./cmd/voice-smoke -wav other.wav -result-type single -speaker-info \
  -ssd-mode 2 -voiceprint-id <SpeakID>
```

## 对实现的影响

- 过滤条件：`utterances[].additions.speaker_id == 注册时的 SpeakerName`；空串和数字 ID 都丢。
- `DialASR` 在 `SSDMode=2` 时强制 `EnableSpeakerInfo=true`（已落代码）。
- 注册音频需要公网 URL：prod 可加临时公开路由，本地 Mac 走云端 relay（见 `docs/design/voiceprint/design.md`）或 TOS。
- 多设备用声纹组，不要指望一份声纹在 Mac / iPhone 上都准（官方建议注册与使用同设备）。

## 还没验证

- 真人声、远场（0.5–1m）、不同麦克风/耳机、感冒等音色变化下的误拒率。
- 误接受率（另一个人被认成本人）。
- 是否额外计费（ASR 时长费之外）。
- 注册音频在豆包侧的处理/保留策略（`AudioUrl` 会转 PCM 存 TOS）。

## 后续

- 2026-11-09：评估结论为“开发成本大、收益不明确，暂不实现”。完整方案与理由见
  `docs/design/voiceprint/design.md`；探针代码保留在 `feat/voiceprint-probe` 分支。
