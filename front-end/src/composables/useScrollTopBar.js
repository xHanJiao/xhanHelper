import { ref, onMounted, onUnmounted } from 'vue'

export function useScrollTopBar(scrollContainerRef) {
  const isBarVisible = ref(true)
  const lastScrollTop = ref(0)
  const lastScrollDirection = ref('')
  const consecutiveSameDirection = ref(0)
  let ticking = false

  function handleScroll() {
    if (!scrollContainerRef.value) return

    const scrollTop = scrollContainerRef.value.scrollTop
    const scrollHeight = scrollContainerRef.value.scrollHeight
    const clientHeight = scrollContainerRef.value.clientHeight

    if (!ticking) {
      window.requestAnimationFrame(() => {
        const scrollDiff = scrollTop - lastScrollTop.value

        let direction = ''
        if (scrollDiff > 10) {
          direction = 'down'
        } else if (scrollDiff < -10) {
          direction = 'up'
        }

        if (direction) {
          if (direction === lastScrollDirection.value) {
            consecutiveSameDirection.value++
          } else {
            consecutiveSameDirection.value = 1
            lastScrollDirection.value = direction
          }

          if (consecutiveSameDirection.value >= 2) {
            if (direction === 'up') {
              isBarVisible.value = true
            } else if (direction === 'down' && scrollTop > 50) {
              isBarVisible.value = false
            }
          }
        }

        lastScrollTop.value = scrollTop
        ticking = false
      })
      ticking = true
    }
  }

  function showBar() {
    isBarVisible.value = true
    consecutiveSameDirection.value = 0
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
