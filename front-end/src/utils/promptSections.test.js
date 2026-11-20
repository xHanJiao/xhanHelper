import test from 'node:test'
import assert from 'node:assert/strict'
import { splitPromptSections, isXmlSection } from './promptSections.js'

test('splitPromptSections 按 ## 切段并保留 markdown 正文', () => {
  const prompt = [
    '## 行为准则',
    '',
    '- 第一条',
    '### 子标题不切',
    '',
    '## 角色定义',
    '> 你是助手',
    '',
    '## 稳定上下文',
    '',
    '<profile description="画像">',
    '  <field key="A">甲</field>',
    '</profile>',
    '',
  ].join('\n')

  const secs = splitPromptSections(prompt)
  assert.deepEqual(secs.map(s => s.title), ['行为准则', '角色定义', '稳定上下文'])
  assert.match(secs[0].body, /### 子标题不切/)
  assert.equal(secs[1].body, '> 你是助手')
  assert.ok(isXmlSection(secs[2].body))
  assert.ok(!isXmlSection(secs[0].body))
})

test('splitPromptSections 无标题时整段返回', () => {
  const secs = splitPromptSections('<memories><memory>x</memory></memories>')
  assert.equal(secs.length, 1)
  assert.equal(secs[0].title, '')
  assert.ok(isXmlSection(secs[0].body))
})

test('splitPromptSections XML 段内部的 `## ` 不切段', () => {
  const prompt = [
    '## 稳定上下文',
    '<profile description="画像">',
    '  <field key="A">第一行',
    '## 这不是标题',
    '第三行</field>',
    '</profile>',
  ].join('\n')

  const secs = splitPromptSections(prompt)
  assert.equal(secs.length, 1)
  assert.ok(secs[0].body.includes('## 这不是标题'))
  assert.ok(isXmlSection(secs[0].body))
})

test('splitPromptSections XML 闭合后的 `## 标题` 仍要切段', () => {
  const prompt = [
    '## 稳定上下文',
    '<profile description="画像">',
    '  <field key="A">甲</field>',
    '</profile>',
    '',
    '## 语音回答',
    '这次是语音轮，请简短。',
  ].join('\n')

  const secs = splitPromptSections(prompt)
  assert.deepEqual(secs.map(s => s.title), ['稳定上下文', '语音回答'])
  assert.ok(isXmlSection(secs[0].body))
  assert.equal(secs[1].body, '这次是语音轮，请简短。')
})

test('splitPromptSections 空文本返回空数组', () => {
  assert.deepEqual(splitPromptSections(''), [])
  assert.deepEqual(splitPromptSections(undefined), [])
})
