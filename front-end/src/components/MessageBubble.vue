<script setup>
import { computed } from 'vue'
import MarkdownIt from 'markdown-it'
import { NCollapse, NCollapseItem, NSpin } from 'naive-ui'

const props = defineProps({
  message: {
    type: Object,
    required: true
  }
})

const md = new MarkdownIt()

const isUser = computed(() => props.message.role === 'user')
const isAssistant = computed(() => props.message.role === 'assistant')

const contentHtml = computed(() => md.render(props.message.content || ''))
</script>

<template>
  <div style="display: flex; margin-bottom: 16px;">
    <!-- 用户消息 -->
    <div v-if="isUser" style="width: 100%; display: flex; justify-content: flex-end;">
      <div style="max-width: 70%; background: #18a058; color: #fff; border-radius: 12px; padding: 4px 10px; font-size: 15px; line-height: 1.4;">
        <div v-html="contentHtml" style="margin: 0;"></div>
      </div>
      <div style="width: 36px; height: 36px; border-radius: 50%; background: linear-gradient(135deg, #f093fb 0%, #f5576c 100%); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: bold; margin-left: 10px; flex-shrink: 0;">我</div>
    </div>

    <!-- AI 消息 -->
    <div v-else style="width: 100%; display: flex;">
      <div style="width: 36px; height: 36px; border-radius: 50%; background: linear-gradient(135deg, #667eea 0%, #764ba2 100%); display: flex; align-items: center; justify-content: center; color: #fff; font-weight: bold; margin-right: 10px; flex-shrink: 0;">AI</div>
      <div style="flex: 1; min-width: 0;">
        <div v-if="message.reasoning" style="margin-bottom: 10px;">
          <NCollapse>
            <NCollapseItem title="思考过程" name="reasoning">
              <div style="white-space: pre-wrap; font-size: 14px; color: #666; background: #f5f5f5; padding: 10px; border-radius: 8px;">{{ message.reasoning }}</div>
            </NCollapseItem>
          </NCollapse>
        </div>

        <div v-if="contentHtml" style="font-size: 15px; line-height: 1.7; color: #333;">
          <div v-html="contentHtml" style="margin: 0;"></div>
        </div>

        <div v-if="message.toolCalls && message.toolCalls.length > 0" style="margin-top: 12px;">
          <div v-for="(tc, idx) in message.toolCalls" :key="idx" style="border: 1px solid #e8e8e8; border-radius: 12px; padding: 10px 14px; background: #fafafa; margin-bottom: 8px;">
            <div style="display: flex; align-items: center; gap: 8px; margin-bottom: 8px;">
              <NSpin v-if="tc.status === 'running'" size="small" />
              <span style="font-weight: 600; font-size: 14px;">{{ tc.tool }}.{{ tc.function }}</span>
              <span
                :style="{
                  fontSize: '12px',
                  padding: '2px 8px',
                  borderRadius: '10px',
                  background: tc.status === 'done' ? '#e8f5e9' : tc.status === 'failed' ? '#fff3e0' : '#f5f5f5',
                  color: tc.status === 'done' ? '#2e7d32' : tc.status === 'failed' ? '#e65100' : '#666'
                }"
              >
                {{ tc.status === 'running' ? '执行中...' : tc.status === 'done' ? '成功' : '失败' }}
              </span>
            </div>
            <div v-if="tc.result" style="font-size: 13px; color: #666; max-height: 120px; overflow: auto;">
              <pre style="margin: 0; white-space: pre-wrap; font-family: inherit;">{{ JSON.stringify(tc.result, null, 2) }}</pre>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>
