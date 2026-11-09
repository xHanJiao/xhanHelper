// 近场电平闸：判断「这句话是不是离麦克风够近的人说的」。
// 刻度与 useVoiceChat 的 level 一致（min(1, RMS × 4)）；阈值见 docs/design/voice-nearfield/design.md
// 实测：隔壁房间会议声 2s 窗中位数 0.006，本人 50cm 0.21，差 35 倍。

export const NEAR_FIELD_WINDOW_MS = 2000
// 窗口里至少要有 0.8 秒未门控数据才判：不够就放行（误拒比误收难受）
export const NEAR_FIELD_MIN_SPAN_MS = 800
// 剔除静音后至少要有 0.15 秒有声帧：单帧尖峰（碰一下麦克风 0.3~0.55）不算「有人说话」
export const NEAR_FIELD_MIN_VOICED_MS = 150
// 噪声本底剔除：floor = min(cap, max(0.004, 本窗 p20 × 2))，cap = min(0.04, 阈值/2)
const NOISE_BASE = 0.004
const NOISE_MULT = 2
const NOISE_CAP = 0.04

export const NEAR_FIELD_LEVELS = [
  { value: 'off', label: '关闭', threshold: null },
  { value: 'loose', label: '宽松', threshold: 0.02 },
  { value: 'standard', label: '标准', threshold: 0.05 },
  { value: 'near', label: '近场', threshold: 0.1 }
]

/** 阈值；null = 闸门关闭（与加闸之前的行为完全一致）。 */
export function nearFieldThreshold(value) {
  return NEAR_FIELD_LEVELS.find(o => o.value === value)?.threshold ?? null
}

export function percentile(values, q) {
  if (!values.length) return 0
  const sorted = [...values].sort((a, b) => a - b)
  return sorted[Math.min(sorted.length - 1, Math.floor((sorted.length - 1) * q))]
}

export function median(values) {
  if (!values.length) return 0
  const sorted = [...values].sort((a, b) => a - b)
  return sorted[(sorted.length - 1) >> 1]
}

/**
 * frames: [{ t, level, gated }]（按时间递增），true = 放行。
 *
 * 三处刻意设计：
 * - 只用未门控帧：播报期间读到的是 TTS 漏音残留，不是用户说话。
 * - 先剔掉静音帧再取中位数：sentence 到达时窗口尾部有 0.8 秒以上的判停静音，
 *   直接取中位数会把 1 秒出头的短句拉到阈值以下（真机数据模拟过）。静音帧不携带
 *   距离信息，按本窗 p20 估个噪声本底剔掉；剔完一个不剩说明这段本来就没人靠近说。
 * - 有声帧要覆盖 0.15 秒以上：否则一次碰麦尖峰（单帧 0.3~0.55）就能把判定翻成放行。
 */
export function shouldAccept(frames, threshold, now = Date.now()) {
  if (threshold == null) return true
  const recent = frames.filter(f => !f.gated && now - f.t <= NEAR_FIELD_WINDOW_MS)
  if (recent.length < 4) return true
  if (recent[recent.length - 1].t - recent[0].t < NEAR_FIELD_MIN_SPAN_MS) return true
  const levels = recent.map(f => f.level)
  const cap = Math.min(NOISE_CAP, threshold / 2)
  const floor = Math.min(cap, Math.max(NOISE_BASE, percentile(levels, 0.2) * NOISE_MULT))
  const voiced = recent.filter(f => f.level >= floor)
  if (voiced.length < 2) return false
  if (voiced[voiced.length - 1].t - voiced[0].t < NEAR_FIELD_MIN_VOICED_MS) return false
  return median(voiced.map(f => f.level)) >= threshold
}
