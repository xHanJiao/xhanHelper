// 选图 → 压缩 → 上传前的准备。
//
// 参数对齐 macOS 端 ImageCompressor：长边 2048、JPEG 0.85、GIF 不碰（重编码只剩第一帧）。
// 服务端只认 png/jpeg/webp/gif，所以 HEIC（iPhone 原图）必须在这里换成 JPEG —— Safari 能解，
// 桌面 Chrome 解不了，那种情况按原字节传，由服务端按魔数拒绝并给出中文原因。
const MAX_PIXEL = 2048
const SKIP_BYTES = 4 << 20
const KEEP_TYPES = ['image/jpeg', 'image/png', 'image/webp']

async function decode(file) {
  try {
    return await createImageBitmap(file, { imageOrientation: 'from-image' })
  } catch {
    // 老 Safari / Chrome 不认的格式走 <img> 兜底（EXIF 方向由浏览器负责）
    const url = URL.createObjectURL(file)
    try {
      const img = new Image()
      img.src = url
      await img.decode()
      return img
    } catch {
      return null
    } finally {
      URL.revokeObjectURL(url)
    }
  }
}

function toBlob(canvas, type) {
  return new Promise(resolve => canvas.toBlob(resolve, type, 0.85))
}

// 同内容只留一份（服务端按内容去重返回同一个 image_id，重复发会让模型看两遍）。
// LAN http 下 crypto.subtle 不可用，退回 name+size+mtime。
async function contentKey(blob, file) {
  if (globalThis.crypto?.subtle) {
    const digest = await crypto.subtle.digest('SHA-256', await blob.arrayBuffer())
    return [...new Uint8Array(digest).slice(0, 16)]
      .map(b => b.toString(16).padStart(2, '0'))
      .join('')
  }
  return `${file.name}|${blob.size}|${file.lastModified}`
}

/**
 * 压缩并算出去重 key。
 * @returns {Promise<{blob: Blob, name: string, key: string}>}
 */
export async function prepareImage(file) {
  if (file.type === 'image/gif') {
    return { blob: file, name: file.name, key: await contentKey(file, file) }
  }

  const source = await decode(file)
  if (!source) {
    return { blob: file, name: file.name, key: await contentKey(file, file) }
  }

  const width = source.width
  const height = source.height
  const scale = Math.min(1, MAX_PIXEL / Math.max(width, height))
  const keep = scale === 1 && KEEP_TYPES.includes(file.type) && file.size <= SKIP_BYTES
  if (keep) {
    source.close?.()
    return { blob: file, name: file.name, key: await contentKey(file, file) }
  }

  const canvas = document.createElement('canvas')
  canvas.width = Math.round(width * scale)
  canvas.height = Math.round(height * scale)
  const ctx = canvas.getContext('2d')
  if (!ctx) {
    source.close?.()
    return { blob: file, name: file.name, key: await contentKey(file, file) }
  }
  ctx.drawImage(source, 0, 0, canvas.width, canvas.height)
  source.close?.()

  const type = file.type === 'image/png' ? 'image/png' : 'image/jpeg'
  const blob = await toBlob(canvas, type)
  // 压完反而更大（小图转格式偶尔如此）就保留原图，除非原格式服务端不收
  if (!blob || (blob.size >= file.size && KEEP_TYPES.includes(file.type))) {
    return { blob: file, name: file.name, key: await contentKey(file, file) }
  }
  const name = file.name.replace(/\.[^.]+$/, '') + (type === 'image/png' ? '.png' : '.jpg')
  return { blob, name, key: await contentKey(blob, file) }
}
