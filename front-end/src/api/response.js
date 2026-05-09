/**
 * 统一响应处理
 *
 * 当前后端返回裸 data，unwrap() 直接透传。
 * 待后端切换为 {"status":"ok","errorCode":0,"errMsg":"","data":{}} 格式后，
 * 只需修改此函数即可全局生效。
 */

export function unwrap(res) {
  if (res && typeof res === 'object' && 'errorCode' in res) {
    // 新统一格式: {"status":"ok","errorCode":0,"errMsg":"","data":{}}
    if (res.status === 'ok' || res.errorCode === 0) {
      return res.data
    }
    throw new ApiError(res.errorCode || 500, res.errMsg || '请求失败')
  }
  // 旧格式：裸 data，直接返回
  return res
}

export class ApiError extends Error {
  constructor(code, message) {
    super(message)
    this.name = 'ApiError'
    this.code = code
  }
}
