<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { AuthService } from '../../bindings/opsflash/server'

const emit = defineEmits<{
  (e: 'login', username: string, token: string): void
}>()

const username = ref('')
const password = ref('')
const error = ref('')
const loading = ref(false)

// 记住账号和密码
const remember = ref(false)
const REMEMBER_KEY = 'ops_login_remember'

// 页面加载时恢复已保存的账号密码
onMounted(() => {
  try {
    const raw = localStorage.getItem(REMEMBER_KEY)
    if (!raw) return
    const data = JSON.parse(raw)
    if (data && data.username) {
      username.value = data.username
      if (data.password) {
        password.value = decodeURIComponent(atob(data.password))
        remember.value = true
      }
    }
  } catch {
    // 数据损坏时忽略，不阻塞登录
  }
})

// 登录成功时按勾选状态保存/清除
function saveRemember() {
  try {
    if (remember.value && username.value && password.value) {
      localStorage.setItem(
        REMEMBER_KEY,
        JSON.stringify({
          username: username.value,
          password: btoa(encodeURIComponent(password.value)), // base64 编码，避免明文直存
        }),
      )
    } else {
      localStorage.removeItem(REMEMBER_KEY)
    }
  } catch (e) {
    console.error('保存记住登录失败', e)
  }
}

async function handleLogin() {
  error.value = ''
  
  if (!username.value.trim()) {
    error.value = '请输入用户名'
    return
  }
  
  if (!password.value) {
    error.value = '请输入密码'
    return
  }
  
  loading.value = true
  
  try {
    const res = await AuthService.Login({
      username: username.value,
      password: password.value,
    })
    
    if (res.success) {
      saveRemember()
      emit('login', res.username, res.token)
    } else {
      error.value = res.message
    }
  } catch (e) {
    error.value = '登录失败，请稍后重试'
    console.error(e)
  }
  
  loading.value = false
}
</script>

<template>
  <main class="login-container">
    <div class="login-card">
      <header class="login-header">
        <img src="/logo.png" class="login-logo" alt="Logo" />
        <h1 class="login-title">欢迎登录</h1>
        <p class="login-subtitle">请输入您的账号信息</p>
      </header>

      <form class="login-form" @submit.prevent="handleLogin">
        <div class="form-group">
          <label class="form-label" for="username">用户名</label>
          <div class="input-wrapper">
            <svg class="input-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <path d="M20 21v-2a4 4 0 0 0-4-4H8a4 4 0 0 0-4 4v2"/>
              <circle cx="12" cy="7" r="4"/>
            </svg>
            <input
              id="username"
              v-model="username"
              type="text"
              class="form-input"
              placeholder="请输入用户名"
              autocomplete="username"
              :disabled="loading"
            />
          </div>
        </div>

        <div class="form-group">
          <label class="form-label" for="password">密码</label>
          <div class="input-wrapper">
            <svg class="input-icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">
              <rect x="3" y="11" width="18" height="11" rx="2" ry="2"/>
              <path d="M7 11V7a5 5 0 0 1 10 0v4"/>
            </svg>
            <input
              id="password"
              v-model="password"
              type="password"
              class="form-input"
              placeholder="请输入密码"
              autocomplete="current-password"
              :disabled="loading"
            />
          </div>
        </div>

        <label class="remember-row" :class="{ 'remember-row-disabled': loading }">
          <input
            v-model="remember"
            type="checkbox"
            class="remember-checkbox"
            :disabled="loading"
          />
          <span>记住账号和密码</span>
        </label>

        <p v-if="error" class="error-message">{{ error }}</p>

        <button
          type="submit"
          class="login-btn"
          :disabled="loading"
        >
          <span v-if="loading" class="btn-loading">
            <svg class="spinner" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
              <circle cx="12" cy="12" r="10" stroke-dasharray="30 60" stroke-linecap="round"/>
            </svg>
            登录中...
          </span>
          <span v-else>登 录</span>
        </button>
      </form>

    </div>
  </main>
</template>

<style scoped>
.login-container {
  flex: 1 0 auto;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 2rem 0;
}

.login-card {
  width: 100%;
  max-width: 400px;
  background: var(--glass);
  border: 1px solid var(--glass-border);
  border-radius: var(--radius);
  padding: 2.5rem 2rem;
  -webkit-backdrop-filter: blur(12px);
  backdrop-filter: blur(12px);
  --wails-draggable: no-drag;
}

.login-header {
  text-align: center;
  margin-bottom: 2rem;
}

.login-logo {
  width: 64px;
  height: 64px;
  margin-bottom: 1rem;
}

.login-title {
  font-size: 1.5rem;
  font-weight: 700;
  color: var(--text);
  margin: 0 0 0.5rem 0;
}

.login-subtitle {
  font-size: 0.875rem;
  color: var(--muted);
  margin: 0;
}

.login-form {
  display: flex;
  flex-direction: column;
  gap: 1.25rem;
}

.form-group {
  display: flex;
  flex-direction: column;
  gap: 0.5rem;
}

.form-label {
  font-size: 0.8125rem;
  font-weight: 500;
  color: var(--muted);
}

.input-wrapper {
  position: relative;
  display: flex;
  align-items: center;
}

.input-icon {
  position: absolute;
  left: 0.875rem;
  width: 1.125rem;
  height: 1.125rem;
  color: var(--muted);
  pointer-events: none;
}

.form-input {
  width: 100%;
  height: 2.75rem;
  padding: 0 1rem 0 2.5rem;
  border: 1px solid var(--glass-border);
  border-radius: 8px;
  font-size: 0.9375rem;
  color: var(--text);
  background-color: var(--glass);
  transition: all 0.2s ease;
  outline: none;
  box-sizing: border-box;
  -webkit-user-select: text;
  user-select: text;
}

.form-input::placeholder {
  color: rgba(154, 166, 192, 0.5);
}

.form-input:focus {
  border-color: var(--accent);
  background-color: var(--glass-strong);
  box-shadow: 0 0 0 3px rgba(255, 77, 77, 0.15);
}

.form-input:disabled {
  opacity: 0.6;
  cursor: not-allowed;
}

.error-message {
  font-size: 0.8125rem;
  color: var(--accent);
  margin: 0;
  padding: 0;
}

/* 记住账号和密码 */
.remember-row {
  display: flex;
  align-items: center;
  gap: 0.5rem;
  font-size: 0.8125rem;
  color: var(--muted);
  cursor: pointer;
  user-select: none;
  -webkit-user-select: none;
  margin-top: -0.25rem;
  --wails-draggable: no-drag;
}
.remember-row-disabled {
  cursor: not-allowed;
  opacity: 0.7;
}
.remember-checkbox {
  width: 14px;
  height: 14px;
  accent-color: #FF2D72;
  cursor: pointer;
  flex-shrink: 0;
}
.remember-checkbox:disabled {
  cursor: not-allowed;
}

.login-btn {
  width: 100%;
  height: 2.75rem;
  margin-top: 0.5rem;
  border: none;
  border-radius: 8px;
  background: var(--grad);
  color: #ffffff;
  font-size: 0.9375rem;
  font-weight: 600;
  cursor: pointer;
  transition: all 0.2s ease;
  display: flex;
  align-items: center;
  justify-content: center;
  gap: 0.5rem;
  box-shadow: 0 6px 18px rgba(255, 45, 114, 0.35);
}

.login-btn:hover:not(:disabled) {
  box-shadow: 0 8px 24px rgba(255, 45, 114, 0.5);
}

.login-btn:active:not(:disabled) {
  transform: scale(0.98);
}

.login-btn:disabled {
  opacity: 0.7;
  cursor: not-allowed;
}

.btn-loading {
  display: flex;
  align-items: center;
  gap: 0.5rem;
}

.spinner {
  width: 1rem;
  height: 1rem;
  animation: spin 1s linear infinite;
}

@keyframes spin {
  from { transform: rotate(0deg); }
  to { transform: rotate(360deg); }
}

.login-footer {
  text-align: center;
  margin-top: 1.5rem;
  padding-top: 1rem;
  border-top: 1px solid var(--glass-border);
  font-size: 0.8125rem;
  color: var(--muted);
}
</style>
