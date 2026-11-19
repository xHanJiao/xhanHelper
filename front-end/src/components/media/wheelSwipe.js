// 触控板双指横滑的判定（纯逻辑，便于单测）：喂每个 wheel 的 delta，吐 -1 / 0 / 1。
//
// 要点是「一次物理手势只切一张」：触控板抬指后系统还在继续送惯性事件，
// 光靠「停手 N 毫秒」判新手势会让惯性流永远不结束（滑一次要等一两秒才能再滑）。
// 所以新手势看两个信号：事件流断了 GAP_MS，或者惯性衰减中幅度突然反弹（用户又划了一次）。
const GAP_MS = 120
const THRESHOLD = 60
// 横向要压过纵向这么多倍才算横滑（双指斜着划也会带 deltaY）
const RATIO = 1.5
// 惯性是逐帧衰减的，幅度涨回这么多倍就是新手势
const BUMP_RATIO = 2.5
// 太小的抖动不算反弹
const BUMP_MIN = 8

export function createWheelSwipe() {
  let accX = 0
  let accY = 0
  let fired = false
  let lastAbsX = 0
  let lastAt = 0

  /// 返回 -1 上一张 / 0 没切 / 1 下一张；dx 正 = 内容往右滚 = 手指往左划
  return function feed(dx, dy, now) {
    const absX = Math.abs(dx)
    const prevAbs = lastAbsX
    lastAbsX = absX
    if (now - lastAt > GAP_MS || (fired && absX > prevAbs * BUMP_RATIO && absX > BUMP_MIN)) {
      accX = 0
      accY = 0
      fired = false
    }
    lastAt = now

    accX += dx
    accY += dy
    if (fired) return 0
    if (Math.abs(accX) < THRESHOLD || Math.abs(accX) < Math.abs(accY) * RATIO) return 0
    fired = true
    return accX > 0 ? 1 : -1
  }
}
