import test from 'node:test'
import assert from 'node:assert/strict'
import { cutSentences } from './speech.js'

test('句末标点处切句，未结束的尾巴留给下一次', () => {
  const { sentences, consumed } = cutSentences('今天天气很好。我们去公园吧', 0, true)
  assert.deepEqual(sentences, ['今天天气很好。'])
  assert.equal(consumed, '今天天气很好。'.length)
})

test('首句在 12 字后的逗号处先切一刀，后续不再切逗号', () => {
  const text = '我先看了一下你的账单记录，发现这个月的支出比上个月高了不少。'
  const { sentences } = cutSentences(text, 0, true)
  assert.deepEqual(sentences, ['我先看了一下你的账单记录，', '发现这个月的支出比上个月高了不少。'])
})

test('非首句不在逗号处切', () => {
  const { sentences } = cutSentences('这个月花得有点多，下个月注意一点。', 0, false)
  assert.deepEqual(sentences, ['这个月花得有点多，下个月注意一点。'])
})

test('换行也当句末，拼接后与原文一致', () => {
  const text = '第一行\n第二句。尾巴'
  const { sentences, consumed } = cutSentences(text, 0, false)
  assert.deepEqual(sentences, ['第一行\n', '第二句。'])
  assert.equal(sentences.join('') + text.slice(consumed), text)
})
