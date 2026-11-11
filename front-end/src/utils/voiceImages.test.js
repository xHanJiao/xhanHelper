import { test } from 'node:test'
import assert from 'node:assert/strict'
import { waitForUploads } from './voiceImages.js'

function clock(start = 0) {
  let t = start
  return {
    now: () => t,
    sleep: async (ms) => { t += ms }
  }
}

test('voice images: no uploads returns immediately', async () => {
  const drafts = [{ status: 'done' }, { status: 'error' }]
  const { now, sleep } = clock()
  assert.equal(await waitForUploads(() => drafts, { now, sleep }), false)
})

test('voice images: waits until uploading clears', async () => {
  const drafts = [{ status: 'uploading' }, { status: 'done' }]
  const { now, sleep } = clock()
  let ticks = 0
  const timedOut = await waitForUploads(() => drafts, {
    intervalMs: 200,
    now,
    sleep: async (ms) => { drafts[0].status = 'done'; await sleep(ms); ticks++ }
  })
  assert.equal(timedOut, false)
  assert.equal(ticks, 1)
})

test('voice images: plain array still works', async () => {
  const { now, sleep } = clock()
  assert.equal(await waitForUploads([{ status: 'done' }], { now, sleep }), false)
})

test('voice images: draft removed mid-wait ends the wait', async () => {
  let drafts = [{ status: 'uploading' }]
  const { now, sleep } = clock()
  let ticks = 0
  const timedOut = await waitForUploads(() => drafts, {
    intervalMs: 200,
    now,
    sleep: async (ms) => { drafts = []; await sleep(ms); ticks++ }
  })
  assert.equal(timedOut, false)
  assert.equal(ticks, 1)
})

test('voice images: stuck upload gives up at deadline', async () => {
  const drafts = [{ status: 'uploading' }]
  const { now, sleep } = clock()
  const timedOut = await waitForUploads(() => drafts, { timeoutMs: 1000, intervalMs: 200, now, sleep })
  assert.equal(timedOut, true)
  assert.equal(now(), 1000)
})
