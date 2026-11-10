import { test } from 'node:test'
import assert from 'node:assert/strict'
import {
  readVoiceSpeaker, setVoiceSpeaker,
  readVoiceNearField, setVoiceNearField,
  readVoiceMode, setVoiceMode
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

test('voice mode: 默认连续对话（老用户升级后行为不变）', () => {
  useStorage()
  assert.equal(readVoiceMode(), 'continuous')
})

test('voice mode: round trip', () => {
  const store = useStorage()
  setVoiceMode('hold')
  assert.equal(store.get('voice.mode'), 'hold')
  assert.equal(readVoiceMode(), 'hold')
  setVoiceMode('continuous')
  assert.equal(readVoiceMode(), 'continuous')
})

test('voice mode: 未知值读回 continuous', () => {
  useStorage({ 'voice.mode': 'garbage' })
  assert.equal(readVoiceMode(), 'continuous')
})

test('voice mode: no localStorage fallback', () => {
  delete globalThis.localStorage
  assert.equal(readVoiceMode(), 'continuous')
  assert.doesNotThrow(() => setVoiceMode('hold'))
})
