import { ref, onMounted, onUnmounted } from 'vue'
import { useDevice } from './useDevice'

export function useScrollTopBar(scrollContainerRef) {
  const { isIOS } = useDevice()

  const isBarVisible = ref(!isIOS)
  const lastScrollTop = ref(0)
  const lastScrollDirection = ref('')
  const consecutiveSameDirection = ref(0)
  let ticking = false

  function handleScroll() {
    if (!scrollContainerRef.value || isIOS) return

    const scrollTop = scrollContainerRef.value.scrollTop

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

  function hideBar() {
    if (isIOS) {
      isBarVisible.value = false
    }
  }

  onMounted(() => {
    if (!isIOS && scrollContainerRef.value) {
      scrollContainerRef.value.addEventListener('scroll', handleScroll, { passive: true })
    }
  })

  onUnmounted(() => {
    if (!isIOS && scrollContainerRef.value) {
      scrollContainerRef.value.removeEventListener('scroll', handleScroll)
    }
  })

  return {
    isBarVisible,
    showBar,
    hideBar
  }
}
