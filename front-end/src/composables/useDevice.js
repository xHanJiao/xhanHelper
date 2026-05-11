export function useDevice() {
  const isIOS = /iPad|iPhone|iPod/.test(navigator.userAgent) ||
    (navigator.platform === 'MacIntel' && navigator.maxTouchPoints > 1)

  const isSafari = /^((?!chrome|android).)*safari/i.test(navigator.userAgent)

  return {
    isIOS,
    isSafari
  }
}
