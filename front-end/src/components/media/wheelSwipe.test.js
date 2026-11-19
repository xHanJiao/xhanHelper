import test from 'node:test'
import assert from 'node:assert/strict'
import { createWheelSwipe } from './wheelSwipe.js'

const FRAME = 16

test('一次手势只切一张，惯性尾巴不重复触发', () => {
  const feed = createWheelSwipe()
  let t = 0
  const out = []
  // 手指划动（先大后小），接着是系统惯性（继续衰减）
  for (const dx of [20, 30, 40, 30, 20, 12, 8, 5, 3, 2]) out.push(feed(dx, 0, (t += FRAME)))
  assert.deepEqual(out.filter(Boolean), [1])
})

test('惯性还没停就又划一次：幅度反弹要能重新触发', () => {
  const feed = createWheelSwipe()
  let t = 0
  assert.equal(feed(40, 0, (t += FRAME)), 0)
  assert.equal(feed(30, 0, (t += FRAME)), 1) // 第一次切
  assert.equal(feed(6, 0, (t += FRAME)), 0) // 惯性衰减
  assert.equal(feed(3, 0, (t += FRAME)), 0)
  assert.equal(feed(40, 0, (t += FRAME)), 0) // 反弹 = 新手势，但从 40 重新累计
  assert.equal(feed(30, 0, (t += FRAME)), 1) // 第二次切
})

test('停手超过 120ms 后重新计', () => {
  const feed = createWheelSwipe()
  let t = 0
  assert.equal(feed(50, 0, (t += FRAME)), 0)
  assert.equal(feed(20, 0, (t += FRAME)), 1)
  t += 200
  assert.equal(feed(40, 0, (t += FRAME)), 0)
  assert.equal(feed(30, 0, (t += FRAME)), 1)
})

test('竖着滚（或斜得不够横）不切图', () => {
  const feed = createWheelSwipe()
  let t = 0
  for (let i = 0; i < 10; i++) assert.equal(feed(20, 40, (t += FRAME)), 0)
})

test('方向：dx 正 = 下一张，dx 负 = 上一张', () => {
  const next = createWheelSwipe()
  assert.equal(next(80, 0, FRAME), 1)
  const prev = createWheelSwipe()
  assert.equal(prev(-80, 0, FRAME), -1)
})
