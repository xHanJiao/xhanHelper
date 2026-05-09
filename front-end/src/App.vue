<script setup>
import { NConfigProvider, NMessageProvider } from 'naive-ui'
import ConversationList from '@/components/ConversationList.vue'
</script>

<template>
  <NConfigProvider>
    <NMessageProvider>
      <div style="display: flex; height: 100vh; width: 100vw; overflow: hidden;">
        <ConversationList />
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
