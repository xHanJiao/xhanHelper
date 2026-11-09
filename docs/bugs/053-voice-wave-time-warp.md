# 053 音波线「说」态在 macOS 上明显更快（绝对时间做噪声相位）

## 问题表现

- macOS 连续对话里，助手朗读时音波线的高频纹理变化明显比 PWA 快，像有人把播放速度调快了。
- PWA 也有同样问题，只是刚打开页面时 `t` 小、不明显；页面开得越久越快。

## 根因

两端都把绝对时钟直接乘上速度系数当作噪声坐标：

```js
noise(x, t * sp(t) * RESPONSE * DRIFT, seed)
```

它的瞬时频率不是 `sp`，而是

```text
d/dt [t · sp(t)] = sp(t) + t · sp'(t)
```

`sp` 在「说」态是 `2 + 3.5·cadence(t)`，每秒都在变（`sp' ≈ 0.1`）。于是 `t · sp'` 这一项会随时间线性放大：

- macOS 的 `t = Date.timeIntervalSinceReferenceDate`（约 `8×10⁸` 秒），从第一帧起就失控；
- PWA 的 `t` 是页面打开后的秒数，刚加载时正常，开几小时后同样会快。

同参数数值复算（240 帧、120Hz、x=0.5）：`t=30` 每帧平均变化 0.028，`t≈2.77×10⁹` 为 0.17，约 6 倍差。

## 最终方案

把 `t · sp(t)` 改成**相位累加器**，让纹理时间只由速度决定、与时钟原点无关：

```text
flow += speed(phase, t) * dt
noise(x, flow * RESPONSE * DRIFT, seed)
```

- `speed`：thinking 3.6、listening `1.4 + 3.2a`、speaking `2 + 3.5·cadence(t)`；`a` 仍是麦克风电平。
- 振幅 `amp` 的 EMA、`cadence` 的幅度调制不变，只修纹理相位。
- macOS / PWA / 调参页三处同改，避免再次漂移。

修复后同参数复算：`t=30` 与 `t≈2.77×10⁹` 的每帧变化量分别为 0.000020 / 0.000016（一致）。

影响文件：`client/xHelper/xHelper/Shared/Views/VoiceOrbView.swift`、
`front-end/src/components/VoiceWaveLine.vue`、`docs/design/voice-chat/mockup.html`（调参页）。
