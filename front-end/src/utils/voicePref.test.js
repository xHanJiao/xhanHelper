import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  readVoiceSpeaker, setVoiceSpeaker,
  readVoiceNearField, setVoiceNearField
} from './voicePref.js'

function useStorage(initial = {}) {
  const store = new Map(Object.entries(initial))
  globalThis.localStorage = {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => { store.set(k, String(v)) },
    removeItem: (k) => { store.delete(k) }
  }
  return store
}

test('voice speaker: empty when unset', () => {
  useStorage()
  assert.equal(readVoiceSpeaker(), '')
})

test('voice speaker: round trip and clear', () => {
  useStorage()
  setVoiceSpeaker('zh_male_m191_uranus_bigtts')
  assert.equal(readVoiceSpeaker(), 'zh_male_m191_uranus_bigtts')
  setVoiceSpeaker('')
  assert.equal(readVoiceSpeaker(), '')
})

test('voice speaker: no localStorage fallback', () => {
  delete globalThis.localStorage
  assert.equal(readVoiceSpeaker(), '')
  assert.doesNotThrow(() => setVoiceSpeaker('x'))
})

test('voice near-field: 默认 standard（没设过就走标准档）', () => {
  useStorage()
  assert.equal(readVoiceNearField(), 'standard')
})

test('voice near-field: round trip', () => {
  useStorage()
  setVoiceNearField('loose')
  assert.equal(readVoiceNearField(), 'loose')
  setVoiceNearField('near')
  assert.equal(readVoiceNearField(), 'near')
})

test('voice near-field: 显式关闭要落盘（不能被读回默认档）', () => {
  const store = useStorage()
  setVoiceNearField('off')
  assert.equal(store.get('voice.nearField'), 'off')
  assert.equal(readVoiceNearField(), 'off')
})

test('voice near-field: no localStorage fallback', () => {
  delete globalThis.localStorage
  assert.equal(readVoiceNearField(), 'standard')
  assert.doesNotThrow(() => setVoiceNearField('near'))
})
