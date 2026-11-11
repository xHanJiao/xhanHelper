// 设备标识：同一浏览器（含重新登录）共用一个 device_id，服务端据此替换旧 token。
const KEY = 'xhanhelper_device_id'

export function getDeviceId() {
  try {
    let id = localStorage.getItem(KEY)
    if (!id) {
      id = typeof crypto !== 'undefined' && crypto.randomUUID
        ? crypto.randomUUID()
        : `dev-${Date.now()}-${Math.random().toString(36).slice(2, 10)}`
      localStorage.setItem(KEY, id)
    }
    return id
  } catch (e) {
    console.error('Failed to get device id', e)
    return ''
  }
}
