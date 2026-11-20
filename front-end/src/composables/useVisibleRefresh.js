import { onBeforeUnmount, onMounted } from 'vue'

// 纯判定（可单测）：不可见 / 被禁用 / 离上次刷新太近，都不该重拉
export function canAutoRefresh(visible, enabled, now, last, minGapMs) {
  return visible && enabled && now - last >= minGapMs
}

/**
 * 回到前台时重拉数据。
 *
 * 主屏 PWA 被 iOS 挂起时不销毁进程，回到前台也不会重新 mount，
 * 只在 onMounted 拉一次的列表会一直停在挂起那天的快照上（bug 046 / 072）。
 *
 * @param {() => void} refresh 重拉函数（自己负责错误处理，别再抛回来）
 * @param {{ enabled?: () => boolean, minGapMs?: number }} options
 *   enabled：不该刷新的状态（如多选态）返回 false
 *   minGapMs：两次刷新最小间隔，避免切 app 来回切时连环重拉
 */
export function useVisibleRefresh(refresh, { enabled = () => true, minGapMs = 5000 } = {}) {
  // pageshow 会跟着首次加载一起触发，用这个时间戳把开屏那一次跳过
  let last = Date.now()

  function run() {
    const now = Date.now()
    if (!canAutoRefresh(document.visibilityState === 'visible', enabled(), now, last, minGapMs)) return
    last = now
    refresh()
  }

  onMounted(() => {
    document.addEventListener('visibilitychange', run)
    // Safari 从 bfcache 恢复不触发 visibilitychange
    window.addEventListener('pageshow', run)
  })

  onBeforeUnmount(() => {
    document.removeEventListener('visibilitychange', run)
    window.removeEventListener('pageshow', run)
  })
}
