import test from 'node:test'
import assert from 'node:assert/strict'
import { shouldDrainQueue } from './promptQueue.js'

test('shouldDrainQueue：空闲 + 未暂停 + 未挂起 + 有队列才出队', () => {
  assert.equal(shouldDrainQueue({ streaming: false, paused: false, count: 2 }), true)
  assert.equal(shouldDrainQueue({ streaming: true, paused: false, count: 2 }), false)
  assert.equal(shouldDrainQueue({ streaming: false, paused: true, count: 2 }), false)
  assert.equal(shouldDrainQueue({ streaming: false, paused: false, held: true, count: 2 }), false)
  assert.equal(shouldDrainQueue({ streaming: false, paused: false, count: 0 }), false)
})
