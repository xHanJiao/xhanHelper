<script setup>
import PageTopBar from '@/components/PageTopBar.vue'
</script>

<template>
  <div class="page">
    <PageTopBar title="灵阅" />
    <div class="empty-wrap">
      <div class="empty-card">
        <div class="empty-ico">✦</div>
        <h2>灵阅</h2>
        <p>HTML 页面列表、详情和分享会在 M2 接入。现在可以先去对话里让模型生成一页。</p>
        <router-link to="/chat" class="btn">去对话</router-link>
      </div>
    </div>
  </div>
</template>

<style scoped>
.page { height: 100%; display: flex; flex-direction: column; min-height: 0; }
.empty-wrap { flex: 1; display: grid; place-items: center; padding: 24px; }
.empty-card {
  max-width: 360px;
  text-align: center;
  padding: 26px 24px;
  border-radius: 14px;
  background: var(--color-surface);
  border: 0.5px solid var(--color-border);
  box-shadow: var(--shadow-md);
}
.empty-ico {
  width: 44px; height: 44px; margin: 0 auto 12px;
  border-radius: 14px; display: grid; place-items: center;
  background: var(--color-primary-soft); color: var(--color-primary); font-size: 20px;
}
.empty-card h2 { font-size: 17px; margin: 0 0 8px; }
.empty-card p { font-size: 12.5px; line-height: 1.6; color: var(--color-text-secondary); margin: 0 0 16px; }
.btn {
  display: inline-block; border-radius: 999px; background: var(--color-primary);
  color: #fff; padding: 8px 16px; font-size: 12.5px; text-decoration: none;
}
[data-theme="dark"] .btn { color: #10221B; }
</style>
