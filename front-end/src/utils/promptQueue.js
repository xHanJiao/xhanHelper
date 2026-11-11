// 队列是否该自动出队。抽成纯函数，ChatView 的 watcher 与单测共用同一套判定。
// 三道闸：流式中不发（后端同会话只允许一个循环）、暂停不发（上一轮失败）、空队列不发。
export function shouldDrainQueue({ streaming, paused, held = false, count }) {
  return !streaming && !paused && !held && count > 0
}
