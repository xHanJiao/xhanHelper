<script setup>
import { ref } from 'vue'

const animals = ['cat', 'crab', 'dolphin']

const props = defineProps({
  kind: { type: String, default: '' },
})

const animal = ref(props.kind || animals[Math.floor(Math.random() * animals.length)])
</script>

<template>
  <div :class="['waiting-animal', `animal-${animal}`]">

    <svg v-if="animal === 'cat'" width="84" height="60" viewBox="0 -1 14 10" xmlns="http://www.w3.org/2000/svg">
      <rect x="2" y="3" width="5" height="1" fill="#d4a574"/>
      <rect x="2" y="4" width="5" height="1" fill="#d4a574"/>
      <rect x="2" y="5" width="5" height="1" fill="#d4a574"/>
      <g class="legs-cat-a">
        <rect x="3" y="6" width="1" height="1" fill="#c4946a"/>
        <rect x="4" y="6" width="1" height="1" fill="#c4946a"/>
      </g>
      <g class="legs-cat-b">
        <rect x="6" y="6" width="1" height="1" fill="#c4946a"/>
        <rect x="7" y="6" width="1" height="1" fill="#c4946a"/>
      </g>
      <rect x="5" y="0" width="2" height="1" fill="#d4a574"/>
      <rect x="4" y="1" width="4" height="1" fill="#d4a574"/>
      <rect x="3" y="2" width="6" height="1" fill="#d4a574"/>
      <rect x="4" y="3" width="5" height="1" fill="#d4a574"/>
      <rect x="5" y="-1" width="1" height="1" fill="#d4875e"/>
      <rect x="7" y="-1" width="1" height="1" fill="#d4875e"/>
      <rect x="4" y="0"  width="1" height="1" fill="#d4875e"/>
      <rect x="8" y="0"  width="1" height="1" fill="#d4875e"/>
      <rect x="5" y="0"  width="1" height="1" fill="#e8b090"/>
      <rect x="7" y="0"  width="1" height="1" fill="#e8b090"/>
      <g class="blink">
        <rect x="5" y="2" width="1" height="1" fill="#333"/>
        <rect x="7" y="2" width="1" height="1" fill="#333"/>
      </g>
      <rect x="6" y="3" width="1" height="1" fill="#e8917e"/>
    </svg>

    <svg v-else-if="animal === 'crab'" width="72" height="54" viewBox="0 -2 12 9" xmlns="http://www.w3.org/2000/svg">
      <g class="legs-crab">
        <rect x="1" y="6" width="1" height="1" fill="#d06040"/>
        <rect x="1" y="7" width="1" height="1" fill="#d06040"/>
        <rect x="3" y="6" width="1" height="1" fill="#d06040"/>
        <rect x="3" y="7" width="1" height="1" fill="#d06040"/>
        <rect x="5" y="6" width="1" height="1" fill="#d06040"/>
        <rect x="5" y="7" width="1" height="1" fill="#d06040"/>
      </g>
      <rect x="1" y="2" width="10" height="1" fill="#e88060"/>
      <rect x="1" y="3" width="10" height="1" fill="#e88060"/>
      <rect x="1" y="4" width="10" height="1" fill="#e88060"/>
      <rect x="2" y="5" width="8"  height="1" fill="#e88060"/>
      <rect x="5" y="3" width="2" height="1" fill="#f0a080"/>
      <rect x="3" y="1" width="1" height="1" fill="#d06040"/>
      <rect x="7" y="1" width="1" height="1" fill="#d06040"/>
      <rect x="3" y="0" width="1" height="1" fill="#d06040"/>
      <rect x="7" y="0" width="1" height="1" fill="#d06040"/>
      <g class="blink">
        <rect x="3" y="-1" width="1" height="1" fill="#222"/>
        <rect x="7" y="-1" width="1" height="1" fill="#222"/>
      </g>
      <g class="legs-crab">
        <rect x="6"  y="6" width="1" height="1" fill="#d06040"/>
        <rect x="6"  y="7" width="1" height="1" fill="#d06040"/>
        <rect x="8"  y="6" width="1" height="1" fill="#d06040"/>
        <rect x="8"  y="7" width="1" height="1" fill="#d06040"/>
        <rect x="10" y="6" width="1" height="1" fill="#d06040"/>
        <rect x="10" y="7" width="1" height="1" fill="#d06040"/>
      </g>
      <g class="claw-l">
        <rect x="0" y="1" width="1" height="1" fill="#e07050"/>
        <rect x="0" y="2" width="2" height="1" fill="#e07050"/>
        <rect x="0" y="3" width="3" height="1" fill="#e07050"/>
        <rect x="0" y="4" width="1" height="1" fill="#e07050"/>
      </g>
      <g class="claw-r">
        <rect x="11" y="1" width="1" height="1" fill="#e07050"/>
        <rect x="10" y="2" width="2" height="1" fill="#e07050"/>
        <rect x="9"  y="3" width="3" height="1" fill="#e07050"/>
        <rect x="11" y="4" width="1" height="1" fill="#e07050"/>
      </g>
    </svg>

    <svg v-else width="84" height="72" viewBox="0 5 14 9" xmlns="http://www.w3.org/2000/svg">
      <rect x="0" y="8" width="14" height="1" fill="#5b9bd5"/>
      <rect x="0" y="9" width="14" height="5" fill="#3a7dbf"/>
      <g class="dolphin">
        <g class="body-b">
          <rect x="6" y="6" width="1" height="1" fill="#5b8cb8"/>
          <rect x="7" y="5" width="1" height="1" fill="#5b8cb8"/>
          <rect x="10" y="7" width="1" height="1" fill="#80c0d8"/>
          <rect x="11" y="7" width="1" height="1" fill="#80c0d8"/>
          <rect x="11" y="8" width="2" height="1" fill="#80c0d8"/>
          <rect x="13" y="8" width="1" height="1" fill="#90d0e8"/>
          <rect x="11" y="7" width="1" height="1" fill="#222"/>
          <rect x="7" y="7"  width="3" height="1" fill="#80c0d8"/>
          <rect x="7" y="8"  width="4" height="1" fill="#80c0d8"/>
          <rect x="8" y="9"  width="3" height="1" fill="#d8eef5"/>
          <rect x="9" y="10" width="2" height="1" fill="#d8eef5"/>
          <rect x="3" y="8"  width="4" height="1" fill="#5b8cb8"/>
          <rect x="4" y="9"  width="3" height="1" fill="#5b8cb8"/>
        </g>
        <g class="body-a">
          <rect x="10" y="6" width="2" height="1" fill="#80c0d8"/>
          <rect x="10" y="7" width="3" height="1" fill="#80c0d8"/>
          <rect x="13" y="7" width="1" height="1" fill="#90d0e8"/>
          <rect x="10" y="6" width="1" height="1" fill="#222"/>
          <rect x="6" y="5" width="1" height="1" fill="#5b8cb8"/>
          <rect x="7" y="4" width="1" height="1" fill="#5b8cb8"/>
          <rect x="7" y="7"  width="3" height="1" fill="#80c0d8"/>
          <rect x="7" y="8"  width="4" height="1" fill="#80c0d8"/>
          <rect x="8" y="9"  width="3" height="1" fill="#d8eef5"/>
          <rect x="9" y="10" width="2" height="1" fill="#d8eef5"/>
          <rect x="3" y="7"  width="4" height="1" fill="#5b8cb8"/>
          <rect x="4" y="8"  width="3" height="1" fill="#5b8cb8"/>
        </g>
        <g class="body-am">
          <rect x="10" y="5" width="2" height="1" fill="#80c0d8"/>
          <rect x="10" y="6" width="3" height="1" fill="#80c0d8"/>
          <rect x="13" y="6" width="1" height="1" fill="#90d0e8"/>
          <rect x="10" y="5" width="1" height="1" fill="#222"/>
          <rect x="6" y="4" width="1" height="1" fill="#5b8cb8"/>
          <rect x="7" y="3" width="1" height="1" fill="#5b8cb8"/>
          <rect x="6" y="8"  width="1" height="1" fill="#80c0d8"/>
          <rect x="7" y="7"  width="3" height="1" fill="#80c0d8"/>
          <rect x="7" y="8"  width="4" height="1" fill="#80c0d8"/>
          <rect x="8" y="9"  width="3" height="1" fill="#d8eef5"/>
          <rect x="9" y="10" width="2" height="1" fill="#d8eef5"/>
          <rect x="3" y="6"  width="4" height="1" fill="#5b8cb8"/>
          <rect x="4" y="7"  width="3" height="1" fill="#5b8cb8"/>
        </g>
        <g class="tail-b">
          <rect x="2" y="8" width="1" height="1" fill="#5b8cb8"/>
          <rect x="1" y="9" width="3" height="1" fill="#5b8cb8"/>
        </g>
        <g class="tail-a">
          <rect x="0" y="6" width="1" height="1" fill="#5b8cb8"/>
          <rect x="0" y="7" width="2" height="1" fill="#5b8cb8"/>
          <rect x="2" y="8" width="1" height="1" fill="#5b8cb8"/>
          <rect x="2" y="9" width="1" height="1" fill="#5b8cb8"/>
        </g>
        <g class="tail-am">
          <rect x="2" y="6" width="1" height="1" fill="#5b8cb8"/>
          <rect x="2" y="7" width="1" height="1" fill="#5b8cb8"/>
          <rect x="0" y="8" width="2" height="1" fill="#5b8cb8"/>
          <rect x="0" y="9" width="1" height="1" fill="#5b8cb8"/>
        </g>
      </g>
      <rect x="0" y="9" width="14" height="5" fill="#3a7dbf" opacity="0.85"/>
      <g class="splash-d">
        <rect x="10" y="8" width="1" height="1" fill="#a8ddf0"/>
        <rect x="13" y="8" width="1" height="1" fill="#a8ddf0"/>
        <rect x="11" y="7" width="1" height="1" fill="#a8ddf0"/>
        <rect x="12" y="7" width="1" height="1" fill="#a8ddf0"/>
      </g>
    </svg>

  </div>
</template>

<style scoped>
.waiting-animal {
  display: flex;
  align-items: center;
  justify-content: flex-start;
  padding: 4px 0;
}

/* 猫咪动画 */
.animal-cat svg { animation: cat-bounce 0.4s ease-in-out infinite; }
.legs-cat-a, .legs-cat-b { animation: leg-run 0.4s ease-in-out infinite; }
.legs-cat-b { animation-delay: 0.2s; }

/* 螃蟹动画 */
.animal-crab svg { animation: crab-scuttle 0.4s ease-in-out infinite; }
.legs-crab { animation: leg-scurry 0.3s steps(2) infinite; }
.claw-l { transform-origin: 12px 24px; animation: claw-wave 0.5s ease-in-out infinite; }
.claw-r { transform-origin: 60px 24px; animation: claw-wave 0.5s ease-in-out 0.25s infinite; }

/* 共用眨眼 */
.blink { animation: animal-blink 3s steps(1) infinite; }

@keyframes cat-bounce {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-2px); }
}
@keyframes leg-run {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-1px); }
}
@keyframes crab-scuttle {
  0%, 100% { transform: translateX(0); }
  40% { transform: translateX(2px); }
  80% { transform: translateX(-1px); }
}
@keyframes leg-scurry {
  0% { transform: translateY(0); }
  50% { transform: translateY(1px); }
}
@keyframes claw-wave {
  0%, 100% { transform: translateY(0); }
  50% { transform: translateY(-2px); }
}
@keyframes animal-blink {
  0%, 82%, 100% { visibility: visible; }
  86%, 95% { visibility: hidden; }
}

/* 海豚 */
.animal-dolphin svg { transform: none !important; }
.dolphin { animation: dolphin-jump 2.5s ease-in-out infinite; }
@keyframes dolphin-jump {
  0%,100%{transform:translateY(0)rotate(0deg)}
  8%{transform:translateY(0)rotate(0deg)}
  22%{transform:translateY(-2px)rotate(-3deg)}
  38%{transform:translateY(-6px)rotate(-6deg)}
  55%{transform:translateY(-6px)rotate(-6deg)}
  68%{transform:translateY(-2px)rotate(2deg)}
  82%{transform:translateY(0)rotate(0deg)}
}
.body-b{animation:body-b-vis 2.5s linear infinite}
@keyframes body-b-vis{0%,64%,100%{visibility:visible}17%{visibility:hidden}}
.body-a{animation:body-a-vis 2.5s linear infinite}
@keyframes body-a-vis{0%,33%,100%{visibility:hidden}18%{visibility:visible}}
.body-am{animation:body-am-vis 2.5s linear infinite}
@keyframes body-am-vis{0%,57%,100%{visibility:hidden}36%{visibility:visible}}
.tail-b{animation:tail-b-vis 2.5s linear infinite}
@keyframes tail-b-vis{0%,65%,100%{visibility:visible}16%{visibility:hidden}}
.tail-a{animation:tail-a-vis 2.5s linear infinite}
@keyframes tail-a-vis{0%,33%,100%{visibility:hidden}17%{visibility:visible}}
.tail-am{animation:tail-am-vis 2.5s linear infinite}
@keyframes tail-am-vis{0%,59%,100%{visibility:hidden}35%{visibility:visible}}
.splash-d{animation:splash-show 2.5s ease-in-out infinite}
@keyframes splash-show{0%,100%{opacity:0}72%,82%{opacity:0}77%{opacity:1}}
</style>
