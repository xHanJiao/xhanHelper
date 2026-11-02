import { onBeforeUnmount, ref, watch } from 'vue'

// iOS Safari 的 dvh 不随键盘收缩：用 visualViewport 与布局视口的差值当键盘高度，
// 让 AppShell 把输入区顶到键盘上方，并在键盘弹起时收起底部 Tab。
// 差值阈值 120px：地址栏收起之类的抖动不算键盘。
export function useKeyboardInset(enabled) {
  const inset = ref(0)

  function update() {
    const vv = window.visualViewport
    if (!vv) return
    const next = Math.round(window.innerHeight - vv.height - vv.offsetTop)
    inset.value = next > 120 ? next : 0
  }

  function attach() {
    const vv = window.visualViewport
    if (!vv) return
    vv.addEventListener('resize', update)
    vv.addEventListener('scroll', update)
    update()
  }

  function detach() {
    const vv = window.visualViewport
    if (!vv) return
    vv.removeEventListener('resize', update)
    vv.removeEventListener('scroll', update)
    inset.value = 0
  }

  watch(enabled, (on) => (on ? attach() : detach()), { immediate: true })
  onBeforeUnmount(detach)

  return { inset }
}
