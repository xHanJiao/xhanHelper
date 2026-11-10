import { test } from 'node:test'
import assert from 'node:assert/strict'
import { nearFieldThreshold, shouldAccept, shouldKeepAlive, judgeHoldSpan, containsNearSpeech, median, percentile } from './nearFieldGate.js'

const T0 = 1_700_000_000_000

// 每 50ms 一帧，从 t0 起共 n 帧
function frames(level, n, { t0 = T0, gated = false } = {}) {
  return Array.from({ length: n }, (_, i) => ({ t: t0 + i * 50, level, gated }))
}

test('threshold: off 返回 null，三档各有阈值', () => {
  assert.equal(nearFieldThreshold('off'), null)
  assert.equal(nearFieldThreshold('loose'), 0.02)
  assert.equal(nearFieldThreshold('standard'), 0.05)
  assert.equal(nearFieldThreshold('near'), 0.1)
  assert.equal(nearFieldThreshold('unknown'), null)
})

test('关闭时恒放行', () => {
  assert.equal(shouldAccept(frames(0.001, 40), null), true)
})

test('窗口不足 0.8 秒放行（fail-open）', () => {
  const short = frames(0.001, 10) // 0.45s
  assert.equal(shouldAccept(short, 0.05, T0 + 500), true)
  assert.equal(shouldAccept([], 0.05, T0), true)
})

test('远场（0.006）被拒，近场（0.21）通过', () => {
  const now = T0 + 1500
  assert.equal(shouldAccept(frames(0.006, 40), 0.05, now), false)
  assert.equal(shouldAccept(frames(0.21, 40), 0.05, now), true)
})

test('门控帧被排除：播报期 TTS 残留不能算用户说话', () => {
  const now = T0 + 1000
  // 40 帧门控残留 0.3（时间跨度 1.95s）+ 20 帧未门控静音 0.004 → 未门控部分是静音，拒
  // 若不排除门控帧，中位数会落在 0.3 上而误放行
  const mixed = [...frames(0.3, 40, { gated: true }), ...frames(0.004, 20, { t0: T0 + 100 })]
  assert.equal(shouldAccept(mixed, 0.05, now), false)
})

test('短句 + 句末判停静音仍然通过（真机最容易踩的误拒）', () => {
  // 0.4s 说话 + 0.6s 静音：直接取中位数会落在静音上，必须靠噪声本底剔除救回来
  const mixed = [...frames(0.21, 8), ...frames(0.003, 12, { t0: T0 + 400 })]
  assert.equal(shouldAccept(mixed, 0.05, T0 + 1000), true)
})

test('远场 + 句末静音被拒', () => {
  const mixed = [...frames(0.006, 8), ...frames(0.003, 12, { t0: T0 + 400 })]
  assert.equal(shouldAccept(mixed, 0.05, T0 + 1000), false)
})

test('数据够但整窗都是静音 → 拒（这一段没人靠近说话）', () => {
  assert.equal(shouldAccept(frames(0.003, 40), 0.05, T0 + 2000), false)
})

test('超出 2 秒窗口的旧帧不算', () => {
  const now = T0 + 5000
  assert.equal(shouldAccept(frames(0.21, 40), 0.05, now), true) // 全过期 → 数据不足 → 放行
})

test('混合说话人：窗内有近场人声就放行（距离闸不区分是谁）', () => {
  const now = T0 + 2000
  const mixed = [...frames(0.004, 30), ...frames(0.21, 10, { t0: T0 + 1500 })]
  assert.equal(shouldAccept(mixed, 0.05, now), true)
})

test('混合说话人：只有远场帧时被拒', () => {
  const now = T0 + 2000
  const mixed = [...frames(0.006, 30), ...frames(0.003, 10, { t0: T0 + 1500 })]
  assert.equal(shouldAccept(mixed, 0.05, now), false)
})

test('远场 + 单帧碰麦尖峰 → 仍然拒（尖峰不构成「有人说话」）', () => {
  const mixed = [...frames(0.006, 39), { t: T0 + 1950, level: 0.55, gated: false }]
  assert.equal(shouldAccept(mixed, 0.05, T0 + 2000), false)
})

test('宽松档：连续 0.03 的人声能通过（cap 跟着阈值走）', () => {
  assert.equal(shouldAccept(frames(0.03, 40), 0.02, T0 + 2000), true)
})

test('续命闸：远场噪声不续命，近场人声续命', () => {
  const now = T0 + 500
  assert.equal(shouldKeepAlive(frames(0.006, 10), 0.05, now), false)
  assert.equal(shouldKeepAlive(frames(0.21, 10), 0.05, now), true)
})

test('续命闸：关闭档恒续命', () => {
  assert.equal(shouldKeepAlive(frames(0.001, 10), null, T0 + 500), true)
})

test('续命闸：门控帧不算（播报期残留不能续命）', () => {
  assert.equal(shouldKeepAlive(frames(0.3, 10, { gated: true }), 0.05, T0 + 500), false)
})

test('续命闸：窗口外的旧帧不算，帧不足不续命', () => {
  assert.equal(shouldKeepAlive(frames(0.21, 10), 0.05, T0 + 5000), false)
})

// 长按整段判定：帧不够不是 fail-open，而是「太短」
test('整段判定：关闭档恒放行', () => {
  assert.equal(judgeHoldSpan(frames(0.001, 40), null), 'accept')
})

test('整段判定：近场人声放行、远场拒', () => {
  assert.equal(judgeHoldSpan(frames(0.21, 40), 0.05), 'accept')
  assert.equal(judgeHoldSpan(frames(0.006, 40), 0.05), 'reject')
})

test('整段判定：整段都是静音 → 拒（不是放行）', () => {
  assert.equal(judgeHoldSpan(frames(0.003, 40), 0.05), 'reject')
})

test('整段判定：跨度不足 0.8 秒 → tooShort', () => {
  assert.equal(judgeHoldSpan(frames(0.21, 10), 0.05), 'tooShort') // 0.45s
  assert.equal(judgeHoldSpan([], 0.05), 'tooShort')
})

test('整段判定：远场 + 句末静音被拒，近场 + 句末静音放行', () => {
  const far = [...frames(0.006, 8), ...frames(0.003, 12, { t0: T0 + 400 })]
  const near = [...frames(0.21, 8), ...frames(0.003, 12, { t0: T0 + 400 })]
  assert.equal(judgeHoldSpan(far, 0.05), 'reject')
  assert.equal(judgeHoldSpan(near, 0.05), 'accept')
})

test('整段判定：门控帧不算（虽然长按模式不播报）', () => {
  const mixed = [...frames(0.3, 40, { gated: true }), ...frames(0.004, 20, { t0: T0 + 100 })]
  assert.equal(judgeHoldSpan(mixed, 0.05), 'reject')
})

test('median / percentile', () => {
  assert.equal(median([]), 0)
  assert.equal(median([3, 1, 2]), 2)
  assert.equal(median([4, 1, 3, 2]), 2)
  assert.equal(percentile([1, 2, 3, 4, 5], 0.2), 1)
  assert.equal(percentile([1, 2, 3, 4, 5], 0.9), 4)
})

// 提交闸的证据（任意 2 秒窗有近处人声即记，docs/bugs/058）

test('证据：近场记、远场不记', () => {
  const now = T0 + 2000
  assert.equal(containsNearSpeech(frames(0.21, 40), 0.05, now), true)
  assert.equal(containsNearSpeech(frames(0.006, 40), 0.05, now), false)
})

test('证据：数据不足不记（句子到达时仍走 fail-open）', () => {
  assert.equal(containsNearSpeech(frames(0.21, 10), 0.05, T0 + 450), false)
  assert.equal(containsNearSpeech([], 0.05, T0), false)
})

test('证据：闸门关闭恒记', () => {
  assert.equal(containsNearSpeech(frames(0.001, 10), null, T0), true)
})

test('证据：上一句的帧不能给下一句当证据', () => {
  // 上一句已提交，2 秒窗里还留着它的近场帧；since 之后的帧只有远场
  const mixed = [...frames(0.21, 30), ...frames(0.006, 10, { t0: T0 + 1500 })]
  const now = T0 + 2200
  assert.equal(containsNearSpeech(mixed, 0.05, now), true)
  assert.equal(containsNearSpeech(mixed, 0.05, now, T0 + 2000), false)
})

test('证据：门控帧不算（播报期 TTS 残留）', () => {
  const now = T0 + 1000
  const mixed = [...frames(0.3, 40, { gated: true }), ...frames(0.004, 20, { t0: T0 + 100 })]
  assert.equal(containsNearSpeech(mixed, 0.05, now), false)
})
