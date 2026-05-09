<script setup>
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import NavSidebar from '@/components/NavSidebar.vue'
import ConversationList from '@/components/ConversationList.vue'

const route = useRoute()

const showSidebar = computed(() => route.path.startsWith('/chat'))
</script>

<template>
  <NConfigProvider>
    <NMessageProvider>
      <div style="display: flex; height: 100vh; width: 100vw; overflow: hidden;">
        <NavSidebar />
        <ConversationList v-if="showSidebar" />
        <div v-else style="width: 280px; background: #f7f8fa; border-right: 1px solid #e8e8e8; flex-shrink: 0;" />
        <div style="flex: 1; min-width: 0; display: flex; flex-direction: column;">
          <router-view />
        </div>
      </div>
    </NMessageProvider>
  </NConfigProvider>
</template>

<style>
html, body, #app {
  margin: 0;
  padding: 0;
  height: 100%;
  width: 100%;
}
</style>
