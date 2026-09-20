<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { AuthService, TunnelService, OpsService } from '../../bindings/opsflash/server'
import HomePage from './pages/HomePage.vue'
import OpsPage from './pages/OpsPage.vue'
import ConnectionsPage from './pages/ConnectionsPage.vue'
import BatchPage from './pages/BatchPage.vue'
import TunnelPage from './pages/TunnelPage.vue'
import ExecutionLogsPage from './pages/ExecutionLogsPage.vue'
import SettingsPage from './pages/SettingsPage.vue'

const props = defineProps<{
  username: string
  token: string
}>()

const emit = defineEmits<{
  (e: 'logout'): void
}>()

// --- 导航 ---
type NavKey = 'home' | 'ops' | 'connections' | 'tunnels' | 'batch' | 'logs' | 'settings'
const activeNav = ref<NavKey>('home')

function switchNav(key: NavKey) { activeNav.value = key }

// 从连接管理跳转到隧道页时携带连接筛选
const tunnelFilterConnId = ref(0)
function handleNavigate(key: NavKey, connectionId?: number) {
  if (key === 'tunnels') {
    tunnelFilterConnId.value = connectionId || 0
  }
  activeNav.value = key
}

const pageTitle = computed(() => ({ 
  home: '概览', 
  ops: '脚本库', 
  connections: '连接', 
  tunnels: '隧道', 
  batch: '编排', 
  logs: '记录',
  settings: '设置'
}[activeNav.value]))

const currentPage = computed(() => {
  switch (activeNav.value) {
    case 'home': return HomePage
    case 'ops': return OpsPage
    case 'connections': return ConnectionsPage
    case 'tunnels': return TunnelPage
    case 'batch': return BatchPage
    case 'logs': return ExecutionLogsPage
    case 'settings': return SettingsPage
    default: return null
  }
})

// --- 退出 ---
const logoutLoading = ref(false)
const handleLogout = async () => {
  logoutLoading.value = true
  try { await AuthService.Logout({ token: props.token }) } catch (e) { /* ignore */ }
  emit('logout')
}

// --- 顶部栏隧道状态指示器 ---
const tunnelSummary = ref<{ total: number; running: number; reconnecting: number }>({ total: 0, running: 0, reconnecting: 0 })
let tunnelPollTimer: ReturnType<typeof setInterval> | null = null

async function refreshTunnelSummary() {
  try {
    const res = await TunnelService.GetTunnelSummary({ token: props.token })
    if (res.success) {
      tunnelSummary.value = {
        total: res.total || 0,
        running: res.running || 0,
        reconnecting: res.reconnecting || 0,
      }
    }
  } catch (e) {
    // 静默失败，保持上次状态
  }
}

function startTunnelPolling() {
  if (tunnelPollTimer) return
  refreshTunnelSummary()
  tunnelPollTimer = setInterval(refreshTunnelSummary, 5000)
}

function stopTunnelPolling() {
  if (tunnelPollTimer) {
    clearInterval(tunnelPollTimer)
    tunnelPollTimer = null
  }
}

onMounted(startTunnelPolling)
onUnmounted(stopTunnelPolling)

// 有运行中的隧道（用于徽标样式与跳转）
const tunnelActive = computed(() => tunnelSummary.value.running > 0)
</script>

<template>
  <div class="layout">
    <!-- ====== 顶部导航栏 ====== -->
    <header class="topbar">
      <div class="topbar-left">
        <div class="logo">
          <img src="/logo.png" class="logo-icon" alt="OpsFlash" />
          <span class="logo-text">OpsFlash</span>
        </div>
        <nav class="topnav">
          <a class="nav-item" :class="{ active: activeNav === 'home' }" href="#" @click.prevent="switchNav('home')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M3 9l9-7 9 7v11a2 2 0 01-2 2H5a2 2 0 01-2-2V9z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            概览
          </a>
          <a class="nav-item" :class="{ active: activeNav === 'batch' }" href="#" @click.prevent="switchNav('batch')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M9 11l3 3L22 4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M21 12v7a2 2 0 01-2 2H5a2 2 0 01-2-2V5a2 2 0 012-2h11" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            编排
          </a>
          <a class="nav-item" :class="{ active: activeNav === 'ops' }" href="#" @click.prevent="switchNav('ops')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><polyline points="14 2 14 8 20 8" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><line x1="16" y1="13" x2="8" y2="13" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="16" y1="17" x2="8" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            脚本库
          </a>
          <a class="nav-item" :class="{ active: activeNav === 'connections' }" href="#" @click.prevent="switchNav('connections')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            连接
          </a>
          <a class="nav-item" :class="{ active: activeNav === 'tunnels' }" href="#" @click.prevent="switchNav('tunnels')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
            隧道
            <span v-if="tunnelSummary.total > 0" class="tunnel-badge" :class="{ 'tunnel-badge-active': tunnelActive }">
              {{ tunnelSummary.running }}/{{ tunnelSummary.total }}
            </span>
          </a>
          <a class="nav-item" :class="{ active: activeNav === 'logs' }" href="#" @click.prevent="switchNav('logs')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><polyline points="14 2 14 8 20 8" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><line x1="16" y1="13" x2="8" y2="13" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="16" y1="17" x2="8" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="10" y1="9" x2="8" y2="9" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            记录
          </a>
          <a class="nav-item" :class="{ active: activeNav === 'settings' }" href="#" @click.prevent="switchNav('settings')">
            <svg viewBox="0 0 24 24" fill="none" width="16" height="16">
              <circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="2"/>
              <path d="M19.4 15a1.65 1.65 0 00.33 1.82l.06.06a2 2 0 010 2.83 2 2 0 01-2.83 0l-.06-.06a1.65 1.65 0 00-1.82-.33 1.65 1.65 0 00-1 1.51V21a2 2 0 01-2 2 2 2 0 01-2-2v-.09A1.65 1.65 0 009 19.4a1.65 1.65 0 00-1.82.33l-.06.06a2 2 0 01-2.83 0 2 2 0 010-2.83l.06-.06a1.65 1.65 0 00.33-1.82 1.65 1.65 0 00-1.51-1H3a2 2 0 01-2-2 2 2 0 012-2h.09A1.65 1.65 0 004.6 9a1.65 1.65 0 00-.33-1.82l-.06-.06a2 2 0 010-2.83 2 2 0 012.83 0l.06.06a1.65 1.65 0 001.82.33H9a1.65 1.65 0 001-1.51V3a2 2 0 012-2 2 2 0 012 2v.09a1.65 1.65 0 001 1.51 1.65 1.65 0 001.82-.33l.06-.06a2 2 0 012.83 0 2 2 0 010 2.83l-.06.06a1.65 1.65 0 00-.33 1.82V9a1.65 1.65 0 001.51 1H21a2 2 0 012 2 2 2 0 01-2 2h-.09a1.65 1.65 0 00-1.51 1z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            设置
          </a>
        </nav>
      </div>
      <div class="topbar-right">
        <div class="user-info">
          <div class="user-avatar">{{ props.username.charAt(0).toUpperCase() }}</div>
          <span class="user-name">{{ props.username }}</span>
        </div>
        <button class="logout-btn" :disabled="logoutLoading" @click="handleLogout" title="退出登录">
          <svg viewBox="0 0 24 24" fill="none" width="16" height="16"><path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><polyline points="16 17 21 12 16 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="21" y1="12" x2="9" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        </button>
      </div>
    </header>

    <!-- ====== 内容区 ====== -->
    <div class="content">
      <component
        :is="currentPage"
        :token="props.token"
        :username="props.username"
        :initial-connection-id="activeNav === 'tunnels' ? tunnelFilterConnId : 0"
        @navigate="(key: NavKey, connectionId?: number) => handleNavigate(key, connectionId)"
      />
    </div>
  </div>
</template>

<style scoped>
/* ========== 布局 ========== */
.layout { display: flex; flex-direction: column; height: 100vh; height: 100dvh; overflow: hidden; color: var(--text); --wails-draggable: drag; }

/* ========== 顶部导航栏 ========== */
.topbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 56px;
  padding: 0 24px;
  background: rgba(255,255,255,0.03);
  border-bottom: 1px solid rgba(255,255,255,0.08);
  flex-shrink: 0;
  --wails-draggable: no-drag;
}

.topbar-left {
  display: flex;
  align-items: center;
  gap: 32px;
}

.logo {
  display: flex;
  align-items: center;
  gap: 10px;
}

.logo-icon {
  width: 28px;
  height: 28px;
}

.logo-text {
  font-size: 18px;
  font-weight: 700;
  color: #fff;
  letter-spacing: 0.02em;
}

.topnav {
  display: flex;
  align-items: center;
  gap: 4px;
}

.nav-item {
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 8px 14px;
  border-radius: 8px;
  color: rgba(255,255,255,0.6);
  font-size: 14px;
  font-weight: 500;
  text-decoration: none;
  transition: all 0.2s ease;
  cursor: pointer;
  white-space: nowrap;
}

.nav-item svg {
  flex-shrink: 0;
  opacity: 0.7;
}

.nav-item:hover {
  background: rgba(255,255,255,0.06);
  color: rgba(255,255,255,0.85);
}

.nav-item:hover svg {
  opacity: 1;
}

.nav-item.active {
  background: rgba(255,0,110,0.15);
  color: #FF77B0;
  font-weight: 600;
}

.nav-item.active svg {
  opacity: 1;
}

.tunnel-badge {
  font-size: 11px;
  padding: 2px 6px;
  border-radius: 10px;
  background: rgba(255,255,255,0.1);
  color: rgba(255,255,255,0.5);
  font-variant-numeric: tabular-nums;
}

.tunnel-badge-active {
  background: rgba(52,211,153,0.2);
  color: #34D399;
}

.topbar-right {
  display: flex;
  align-items: center;
  gap: 16px;
}

.settings-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 36px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: rgba(255,255,255,0.6);
  cursor: pointer;
  transition: all 0.2s ease;
}

.settings-btn:hover {
  background: rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.9);
}

.user-info {
  display: flex;
  align-items: center;
  gap: 8px;
}

.user-avatar {
  width: 28px;
  height: 28px;
  border-radius: 50%;
  background: rgba(255,0,110,0.2);
  color: #FF77B0;
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: 12px;
  font-weight: 600;
}

.user-name {
  font-size: 13px;
  color: rgba(255,255,255,0.7);
}

.logout-btn {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: rgba(255,255,255,0.5);
  cursor: pointer;
  transition: all 0.2s ease;
}

.logout-btn:hover {
  background: rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.9);
}

/* ========== 内容区 ========== */
.content {
  flex: 1;
  overflow: auto;
  background: var(--ink);
  padding: 16px 24px 24px;
}
</style>
