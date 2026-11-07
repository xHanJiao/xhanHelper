// 增量 SSE 解析器（按规范）：data 可以有多行，空行才派发一个事件。
// 后端 WriteSSE 会把裸文本里的换行拆成多条 data: 行，所以这里必须用 \n 拼回去，
// 否则 thought_delta / error 里换行之后的内容会被当成未知字段丢掉。
// `id:` 是续传锚点（每轮从 1 开始），通过 onEvent 的第三个参数交给调用方。
export function createSSEParser(onEvent) {
  let buffer = ''
  let eventName = ''
  let dataLines = []
  // 按规范「最后一个事件 ID」要跨事件保留；空 id 表示重置。
  let lastId = null

  function flush() {
    // 没有 data 的事件（如只有 event: 行）按规范不派发
    if (dataLines.length) onEvent(eventName || 'message', dataLines.join('\n'), lastId)
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
    else if (field === 'id') lastId = value
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
    },
    get lastId() {
      return lastId
    }
  }
}

// 空闲看门狗：每 touch() 一次重置计时，超过 ms 没有 touch 就回调一次（只回调一次）。
// 流式响应用它兜住「半开连接」——reader 既不返回也不报错时，UI 会永远停在流式中。
// 服务端每 15s 发 `: ping`，所以阈值可以压到 60s；pause/resume 给「页面被冻结」用：
// 冻结期间浏览器会攒着定时器、恢复时立刻补触发，不 pause 就会误报「连接超时」。
export function createIdleWatchdog(ms, onIdle) {
  let timer = null
  let fired = false

  function clear() {
    if (timer) {
      clearTimeout(timer)
      timer = null
    }
  }

  function arm(delay) {
    clear()
    timer = setTimeout(() => {
      timer = null
      fired = true
      onIdle()
    }, delay)
  }

  return {
    touch() {
      if (fired) return
      arm(ms)
    },
    // 暂停计时（不清 fired）：挂起期间不判死
    pause() {
      clear()
    },
    // 重新武装（含从 fired 状态恢复）：回前台给重连留 graceMs
    resume(graceMs) {
      fired = false
      arm(graceMs || ms)
    },
    stop() {
      clear()
      fired = true
    }
  }
}
