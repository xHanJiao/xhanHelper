<script setup>
// 待发图片托盘：缩略图 + 单张删除 + 失败重试 + 清空。
// 上传在选图时就跑，所以这里的状态只有 上传中 / 成功 / 失败。
defineProps({
  items: { type: Array, default: () => [] },
  limit: { type: Number, default: 20 }
})
const emit = defineEmits(['add', 'remove', 'retry', 'clear'])
</script>

<template>
  <div class="tray no-scrollbar">
    <div v-for="item in items" :key="item.id" class="thumb" :class="item.status">
      <img :src="item.preview" :alt="item.name" />
      <button class="x" type="button" aria-label="移除" @click="emit('remove', item)">×</button>
      <div v-if="item.status === 'uploading'" class="mask">…</div>
      <button
        v-else-if="item.status === 'error'"
        class="retry"
        type="button"
        :title="item.error || '上传失败，点击重试'"
        @click="emit('retry', item)"
      >↻</button>
    </div>

    <button v-if="items.length < limit" class="add" type="button" title="再加一张" @click="emit('add')">+</button>
    <button v-if="items.length >= 2" class="clear" type="button" @click="emit('clear')">清空</button>
  </div>
</template>

<style scoped>
.tray {
  display: flex;
  align-items: center;
  gap: 6px;
  overflow-x: auto;
  padding: 0 2px 8px;
}
.thumb {
  position: relative;
  flex: 0 0 auto;
  width: 56px;
  height: 56px;
  border-radius: 10px;
  overflow: hidden;
  border: 0.5px solid var(--color-border);
  background: var(--color-tool);
}
.thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
.thumb.error { border-color: var(--color-danger); }
.thumb.uploading img { opacity: 0.5; }
.mask {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: var(--color-text-secondary);
  font-size: 14px;
  background: rgba(255, 255, 255, 0.35);
}
[data-theme="dark"] .mask { background: rgba(0, 0, 0, 0.35); }
.x {
  position: absolute;
  top: 2px;
  right: 2px;
  width: 18px;
  height: 18px;
  padding: 0;
  border: 0;
  border-radius: 50%;
  background: rgba(0, 0, 0, 0.55);
  color: #fff;
  font-size: 12px;
  line-height: 1;
  cursor: pointer;
}
.retry {
  position: absolute;
  inset: 0;
  border: 0;
  background: rgba(0, 0, 0, 0.42);
  color: #fff;
  font-size: 18px;
  cursor: pointer;
}
.add {
  flex: 0 0 auto;
  width: 56px;
  height: 56px;
  border-radius: 10px;
  border: 0.5px dashed var(--color-border-strong);
  background: transparent;
  color: var(--color-text-muted);
  font-size: 20px;
  line-height: 1;
  cursor: pointer;
}
.add:hover { background: var(--color-primary-soft); color: var(--color-primary); }
.clear {
  flex: 0 0 auto;
  border: 0;
  background: transparent;
  color: var(--color-text-muted);
  font-size: 11px;
  padding: 6px 4px;
  cursor: pointer;
  white-space: nowrap;
}
.clear:hover { color: var(--color-danger); }
</style>
