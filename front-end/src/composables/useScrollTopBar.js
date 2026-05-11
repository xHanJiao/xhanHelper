import { ref, onMounted, onUnmounted } from 'vue'

export function useScrollTopBar(scrollContainerRef) {
  const isBarVisible = ref(true)
  const lastScrollTop = ref(0)
  let ticking = false

  function handleScroll() {
    if (!scrollContainerRef.value) return

    const scrollTop = scrollContainerRef.value.scrollTop

    if (!ticking) {
      window.requestAnimationFrame(() => {
        // 向上滚动时显示，向下滚动时隐藏
        // 增加一个小阈值避免轻微抖动
        if (scrollTop < lastScrollTop.value - 5) {
          isBarVisible.value = true
        } else if (scrollTop > lastScrollTop.value + 5 && scrollTop > 20) {
          // 只有滚动超过20px才开始隐藏
          isBarVisible.value = false
        }

        lastScrollTop.value = scrollTop
        ticking = false
      })
      ticking = true
    }
  }

  function showBar() {
    isBarVisible.value = true
  }

  onMounted(() => {
    if (scrollContainerRef.value) {
      scrollContainerRef.value.addEventListener('scroll', handleScroll, { passive: true })
    }
  })

  onUnmounted(() => {
    if (scrollContainerRef.value) {
      scrollContainerRef.value.removeEventListener('scroll', handleScroll)
    }
  })

  return {
    isBarVisible,
    showBar
  }
}
