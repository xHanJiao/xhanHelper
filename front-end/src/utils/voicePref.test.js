import { test } from 'node:test'
import assert from 'node:assert/strict'
import { readVoiceSpeaker, setVoiceSpeaker } from './voicePref.js'

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
