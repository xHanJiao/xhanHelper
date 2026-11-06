import test from 'node:test'
import assert from 'node:assert/strict'
import { createIdleWatchdog, createSSEParser } from './sse.js'

function collect() {
  const events = []
  const parser = createSSEParser((event, data) => events.push({ event, data }))
  return { events, parser }
}

test('单事件：event + 单行 data', () => {
  const { events, parser } = collect()
  parser.feed('event: message\ndata: {"delta":"hi"}\n\n')
  parser.end()
  assert.deepEqual(events, [{ event: 'message', data: '{"delta":"hi"}' }])
})

test('多行 data 用换行拼回（thought_delta 的裸文本）', () => {
  const { events, parser } = collect()
  parser.feed('event: thought_delta\ndata: 第一行\ndata: 第二行\ndata: 第三行\n\n')
  parser.end()
  assert.deepEqual(events, [{ event: 'thought_delta', data: '第一行\n第二行\n第三行' }])
})

test('事件之间重置 event 名，缺省为 message', () => {
  const { events, parser } = collect()
  parser.feed('event: status\ndata: a\n\ndata: b\n\n')
  parser.end()
  assert.deepEqual(events, [
    { event: 'status', data: 'a' },
    { event: 'message', data: 'b' }
  ])
})

test('CRLF、注释行与 data: 后无空格', () => {
  const { events, parser } = collect()
  parser.feed(': ping\r\nevent: verify\r\ndata:{"ok":true}\r\n\r\n')
  parser.end()
  assert.deepEqual(events, [{ event: 'verify', data: '{"ok":true}' }])
})

test('chunk 边界切在行中间也能解析', () => {
  const { events, parser } = collect()
  parser.feed('event: message\ndata: {"delta":"你')
  parser.feed('好"}\n\nevent: done\ndata: {}\n\n')
  parser.end()
  assert.deepEqual(events, [
    { event: 'message', data: '{"delta":"你好"}' },
    { event: 'done', data: '{}' }
  ])
})

test('流结束没有空行收尾时，补派发未完成事件', () => {
  const { events, parser } = collect()
  parser.feed('event: status\ndata: 正在分析')
  parser.end()
  assert.deepEqual(events, [{ event: 'status', data: '正在分析' }])
})

test('只有 event 没有 data 不派发', () => {
  const { events, parser } = collect()
  parser.feed('event: status\n\n')
  parser.end()
  assert.deepEqual(events, [])
})

test('看门狗：一直没数据时按 ms 触发一次', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let fired = 0
  const dog = createIdleWatchdog(100, () => fired++)
  dog.touch()
  t.mock.timers.tick(99)
  assert.equal(fired, 0)
  t.mock.timers.tick(1)
  assert.equal(fired, 1)
  // 触发后不再重复
  t.mock.timers.tick(1000)
  assert.equal(fired, 1)
})

test('看门狗：touch 会重置计时，stop 之后永不触发', (t) => {
  t.mock.timers.enable({ apis: ['setTimeout'] })
  let fired = 0
  const dog = createIdleWatchdog(100, () => fired++)
  dog.touch()
  t.mock.timers.tick(90)
  dog.touch() // 收到新数据
  t.mock.timers.tick(90)
  assert.equal(fired, 0)
  dog.stop()
  t.mock.timers.tick(1000)
  assert.equal(fired, 0)
})
