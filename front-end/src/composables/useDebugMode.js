import { ref, watch } from 'vue'

const debugMode = ref(localStorage.getItem('debugMode') === 'true')

watch(debugMode, (val) => {
  localStorage.setItem('debugMode', val)
})

export function useDebugMode() {
  function toggle() {
    debugMode.value = !debugMode.value
  }
  return { debugMode, toggle }
}
