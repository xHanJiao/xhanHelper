import { ref } from 'vue'

export function useScrollTopBar(_scrollContainerRef) {
  const isBarVisible = ref(true)

  function showBar() {
    isBarVisible.value = true
  }

  return {
    isBarVisible,
    showBar
  }
}
