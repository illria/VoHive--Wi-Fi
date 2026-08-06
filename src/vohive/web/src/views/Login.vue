<script setup lang="ts">
import { ref } from 'vue'
import { useAuthStore } from '../stores/auth'
import { useRoute, useRouter } from 'vue-router'
import { Person24Regular, LockClosed24Regular, ArrowRight24Regular } from '@vicons/fluent'

const auth = useAuthStore()
const router = useRouter()
const route = useRoute()

const form = ref({
  username: '',
  password: ''
})

const loading = ref(false)

async function handleLogin() {
  const { ElMessage } = await import('element-plus')
  if (!form.value.username || !form.value.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  
  loading.value = true
  // Mock delay for feel
  await new Promise<void>(r => setTimeout(r, 600))
  const success = await auth.login(form.value.username, form.value.password)
  loading.value = false

  if (success) {
    ElMessage.success('欢迎回来')
    const q = typeof route.query.redirect === 'string' ? route.query.redirect : ''
    let redirect = q ? decodeURIComponent(q) : ''
    if (!redirect) {
      try {
        redirect = sessionStorage.getItem('post_login_redirect') || ''
      } catch {
        // Ignore sessionStorage read failures.
      }
    }
    if (redirect) {
      try {
        sessionStorage.removeItem('post_login_redirect')
      } catch {
        // Ignore sessionStorage delete failures.
      }
      router.push(redirect)
    } else {
      router.push('/')
    }
  } else {
    ElMessage.error('登录失败，请检查凭证')
  }
}
</script>

<template>
  <div class="login-shell">
    <div class="login-layout">
      <section class="login-intro" aria-label="VoHive 产品介绍">
        <div class="login-brand-row">
          <div class="login-mark" aria-hidden="true">V</div>
          <span>VoHive</span>
        </div>
        <div class="login-kicker">Device connectivity control plane</div>
        <h1>把设备状态，<br />放在一个清晰的控制台里。</h1>
        <p>统一查看 4G 模组、网络连接、短信和系统日志，让每一次设备操作都有明确反馈。</p>
        <div class="login-feature-list" aria-label="主要功能">
          <span class="login-feature">实时状态</span>
          <span class="login-feature">网络管理</span>
          <span class="login-feature">日志追踪</span>
        </div>
      </section>

      <section class="login-card" aria-labelledby="login-title">
        <div class="login-heading">
          <h2 id="login-title">登录控制台</h2>
          <p>使用管理员账号继续</p>
        </div>

        <form @submit.prevent="handleLogin" class="login-form">
          <label class="login-field">
            <span>用户名</span>
            <div class="login-input-wrap">
              <Person24Regular class="login-input-icon" aria-hidden="true" />
              <input v-model="form.username" class="login-input" placeholder="输入用户名" type="text" autocomplete="username" />
            </div>
          </label>

          <label class="login-field">
            <span>密码</span>
            <div class="login-input-wrap">
              <LockClosed24Regular class="login-input-icon" aria-hidden="true" />
              <input v-model="form.password" class="login-input" placeholder="输入密码" type="password" autocomplete="current-password" />
            </div>
          </label>

          <button type="submit" :disabled="loading" class="login-submit">
            <span v-if="loading" class="login-spinner" aria-hidden="true"></span>
            <span>{{ loading ? '登录中…' : '登录' }}</span>
            <ArrowRight24Regular v-if="!loading" class="w-5 h-5" aria-hidden="true" />
          </button>
        </form>

        <p class="login-footer">VoHive · 设备连接与通信管理</p>
      </section>
    </div>
  </div>
</template>
