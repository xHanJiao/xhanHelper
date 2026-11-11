/**
 * 语音提交前等草稿图上传结束（见 docs/bugs/061）。
 * 成功和失败都算「结束」，所以这里只等 uploading 消失，不关心结果。
 * `getDrafts` 传函数，等图期间用户删图/清空（数组被整体替换）也能立刻看到。
 * 返回 true 表示等到超时仍有图在上传。
 */
export async function waitForUploads(getDrafts, {
  timeoutMs = 15000,
  intervalMs = 200,
  now = Date.now,
  sleep = (ms) => new Promise((resolve) => setTimeout(resolve, ms))
} = {}) {
  const drafts = typeof getDrafts === 'function' ? getDrafts : () => getDrafts
  const uploading = () => drafts().some((d) => d.status === 'uploading')
  if (!uploading()) return false
  const deadline = now() + timeoutMs
  while (uploading() && now() < deadline) await sleep(intervalMs)
  return uploading()
}
