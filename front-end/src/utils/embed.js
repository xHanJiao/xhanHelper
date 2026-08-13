// 判断是否运行在 macOS 客户端内嵌 WebView 中（Swift 通过 XH_BRIDGE 注入 platform）。
// 浏览器访问时 XH_BRIDGE 不存在 → false。
export function isMacEmbedded() {
  return typeof window !== 'undefined' && window.XH_BRIDGE?.platform === 'macOS'
}
