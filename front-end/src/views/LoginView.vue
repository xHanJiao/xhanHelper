<script setup>
import { ref, computed } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import {
  NCard,
  NForm,
  NFormItem,
  NInput,
  NButton,
  useMessage
} from 'naive-ui'

const router = useRouter()
const auth = useAuthStore()
const message = useMessage()

const isLoginMode = ref(true)
const username = ref('')
const password = ref('')
const loading = computed(() => auth.loading)

async function handleSubmit() {
  if (!username.value || !password.value) {
    message.warning('请输入用户名和密码')
    return
  }

  let res
  if (isLoginMode.value) {
    res = await auth.login(username.value, password.value)
  } else {
    res = await auth.register(username.value, password.value)
  }

  if (res.success) {
    message.success(isLoginMode.value ? '登录成功' : '注册成功')
    router.push('/chat')
  } else {
    message.error(res.message)
  }
}

function toggleMode() {
  isLoginMode.value = !isLoginMode.value
}
</script>

<template>
  <div style="height: 100vh; display: flex; align-items: center; justify-content: center; background: var(--color-bg-page);">
    <NCard title="xhanHelper" style="width: 400px; text-align: center;">
      <NForm>
        <NFormItem label="用户名">
          <NInput v-model:value="username" placeholder="请输入用户名" :disabled="loading" />
        </NFormItem>
        <NFormItem label="密码">
          <NInput v-model:value="password" type="password" placeholder="请输入密码" :disabled="loading" @keyup.enter="handleSubmit" />
        </NFormItem>
        <NFormItem>
          <NButton type="primary" block :loading="loading" @click="handleSubmit">
            {{ isLoginMode ? '登录' : '注册' }}
          </NButton>
        </NFormItem>
        <NFormItem style="text-align: center;">
          <a href="javascript:;" @click="toggleMode" style="color: var(--color-primary); text-decoration: none;">
            {{ isLoginMode ? '没有账号？去注册' : '已有账号？去登录' }}
          </a>
        </NFormItem>
      </NForm>
    </NCard>
  </div>
</template>
