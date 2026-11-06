// 增量 SSE 解析器（按规范）：data 可以有多行，空行才派发一个事件。
// 后端 WriteSSE 会把裸文本里的换行拆成多条 data: 行，所以这里必须用 \n 拼回去，
// 否则 thought_delta / error 里换行之后的内容会被当成未知字段丢掉。
export function createSSEParser(onEvent) {
  let buffer = ''
  let eventName = ''
  let dataLines = []

  function flush() {
    // 没有 data 的事件（如只有 event: 行）按规范不派发
    if (dataLines.length) onEvent(eventName || 'message', dataLines.join('\n'))
    eventName = ''
    dataLines = []
  }

  function handleLine(line) {
    if (line.endsWith('\r')) line = line.slice(0, -1)
    if (line === '') {
      flush()
      return
    }
    if (line.startsWith(':')) return // 注释行（心跳）
    const idx = line.indexOf(':')
    const field = idx === -1 ? line : line.slice(0, idx)
    let value = idx === -1 ? '' : line.slice(idx + 1)
    if (value.startsWith(' ')) value = value.slice(1)
    if (field === 'event') eventName = value
    else if (field === 'data') dataLines.push(value)
  }

  return {
    feed(chunk) {
      buffer += chunk
      const lines = buffer.split('\n')
      buffer = lines.pop() // 最后一段可能是不完整的行，留到下次
      for (const line of lines) handleLine(line)
    },
    end() {
      if (buffer) handleLine(buffer)
      buffer = ''
      flush()
    }
  }
}

// 空闲看门狗：每 touch() 一次重置计时，超过 ms 没有 touch 就回调一次（只回调一次）。
// 流式响应用它兜住「半开连接」——reader 既不返回也不报错时，UI 会永远停在流式中。
// shortcut: 服务端没有心跳帧，只能按「多久没收到任何字节」判死；要更灵敏需服务端定时发 `: ping`。
export function createIdleWatchdog(ms, onIdle) {
  let timer = null
  let fired = false

  function clear() {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  return {
    touch() {
      if (fired) return
      clear()
      timer = setTimeout(() => {
        timer = null
        fired = true
        onIdle()
      }, ms)
    },
    stop() {
      clear()
      fired = true
    }
  }
}
