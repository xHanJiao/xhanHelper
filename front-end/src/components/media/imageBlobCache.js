import { api } from '@/api/client'

// 私有图床的 blob 缓存（上限 12，LRU 撤销）。
// 灯箱来回切图不重复拉，也让下一张进浮层时能立刻出现（否则会先闪占位再跳尺寸）。
const NEEDS_AUTH = /^\/api\/images\//
const CACHE_LIMIT = 12
const cache = new Map() // src -> objectURL

export function isAuthImageSrc(src) {
  return NEEDS_AUTH.test(src)
}

function touch(src, url) {
  const old = cache.get(src)
  if (old && old !== url) URL.revokeObjectURL(old)
  cache.delete(src)
  cache.set(src, url)
  while (cache.size > CACHE_LIMIT) {
    const [key, evicted] = cache.entries().next().value
    URL.revokeObjectURL(evicted)
    cache.delete(key)
  }
}

async function fetchInto(src) {
  const blob = await api.blob(src)
  if (!blob.type.startsWith('image/')) throw new Error(`unexpected content-type: ${blob.type}`)
  const url = URL.createObjectURL(blob)
  touch(src, url)
  return url
}

/// 命中即返回（并刷新 LRU 位置），没命中返回空串
export function cachedImage(src) {
  const hit = cache.get(src)
  if (!hit) return ''
  touch(src, hit)
  return hit
}

/// 拉取并缓存；调用方自己负责忽略过期结果
export function loadImage(src) {
  return fetchInto(src)
}

/// 预热相邻图：失败只是没提前拿到，真正显示时会再试一次
export async function prefetchImage(src) {
  if (!src || !NEEDS_AUTH.test(src) || cache.has(src)) return
  try {
    await fetchInto(src)
  } catch (e) {
    console.warn('Prefetch image failed', src, e)
  }
}
