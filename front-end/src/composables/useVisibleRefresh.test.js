import { test } from 'node:test'
import assert from 'node:assert/strict'
import { canAutoRefresh } from './useVisibleRefresh.js'

test('回前台刷新：可见且过了间隔才刷', () => {
  assert.equal(canAutoRefresh(true, true, 10_000, 1_000, 5_000), true)
})

test('回前台刷新：不可见不刷', () => {
  assert.equal(canAutoRefresh(false, true, 10_000, 1_000, 5_000), false)
})

test('回前台刷新：被禁用（如多选态）不刷', () => {
  assert.equal(canAutoRefresh(true, false, 10_000, 1_000, 5_000), false)
})

test('回前台刷新：开屏 pageshow 与切 app 来回都落在间隔内，不刷', () => {
  assert.equal(canAutoRefresh(true, true, 1_200, 1_000, 5_000), false)
  assert.equal(canAutoRefresh(true, true, 6_000, 1_000, 5_000), true)
})
