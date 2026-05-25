<script setup>
// 像素小猫动画 — 流式消息等待时展示
</script>

<template>
  <div class="waiting-cat">
    <svg width="84" height="60" viewBox="0 -1 14 10" xmlns="http://www.w3.org/2000/svg">
      <!-- 身体 -->
      <rect x="2" y="3" width="5" height="1" fill="#d4a574"/>
      <rect x="2" y="4" width="5" height="1" fill="#d4a574"/>
      <rect x="2" y="5" width="5" height="1" fill="#d4a574"/>

      <!-- 后腿 -->
      <g class="legs-back">
        <rect x="3" y="6" width="1" height="1" fill="#c4946a"/>
        <rect x="4" y="6" width="1" height="1" fill="#c4946a"/>
      </g>

      <!-- 前腿 -->
      <g class="legs-front">
        <rect x="6" y="6" width="1" height="1" fill="#c4946a"/>
        <rect x="7" y="6" width="1" height="1" fill="#c4946a"/>
      </g>

      <!-- 头 -->
      <rect x="5"  y="0" width="2" height="1" fill="#d4a574"/>
      <rect x="4"  y="1" width="4" height="1" fill="#d4a574"/>
      <rect x="3"  y="2" width="6" height="1" fill="#d4a574"/>
      <rect x="4"  y="3" width="5" height="1" fill="#d4a574"/>

      <!-- 双耳 -->
      <rect x="5" y="-1" width="1" height="1" fill="#d4875e"/>
      <rect x="7" y="-1" width="1" height="1" fill="#d4875e"/>
      <rect x="4" y="0"  width="1" height="1" fill="#d4875e"/>
      <rect x="8" y="0"  width="1" height="1" fill="#d4875e"/>

      <!-- 内耳 -->
      <rect x="5" y="0" width="1" height="1" fill="#e8b090"/>
      <rect x="7" y="0" width="1" height="1" fill="#e8b090"/>

      <!-- 双眼 -->
      <g class="blink">
        <rect x="5" y="2" width="1" height="1" fill="#333"/>
        <rect x="7" y="2" width="1" height="1" fill="#333"/>
      </g>

      <!-- 鼻子 -->
      <rect x="6" y="3" width="1" height="1" fill="#e8917e"/>
    </svg>
  </div>
</template>

<style scoped>
.waiting-cat {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 4px 0;
}

.waiting-cat svg {
  animation: cat-bounce 0.4s ease-in-out infinite;
}

@keyframes cat-bounce {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-2px); }
}

.legs-front, .legs-back {
  animation: leg-run 0.4s ease-in-out infinite;
}
.legs-back {
  animation-delay: 0.2s;
}

@keyframes leg-run {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-1px); }
}

.blink {
  animation: cat-blink 3s ease-in-out infinite;
}

@keyframes cat-blink {
  0%, 80%, 100% { opacity: 1; }
  85%, 95% { opacity: 0; }
}
</style>
