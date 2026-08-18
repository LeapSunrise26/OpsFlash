<script setup lang="ts">
import { ref, computed, shallowRef, onMounted, onUnmounted } from 'vue'
import { AuthService, TunnelService } from '../../bindings/opsflash/server'
import HomePage from './pages/HomePage.vue'
import UserPage from './pages/UserPage.vue'
import ConnectionsPage from './pages/ConnectionsPage.vue'
import TunnelPage from './pages/TunnelPage.vue'
import ScriptsPage from './pages/ScriptsPage.vue'
import EnvironmentPage from './pages/EnvironmentPage.vue'

const props = defineProps<{
  username: string
  token: string
}>()

const emit = defineEmits<{
  (e: 'logout'): void
}>()

// --- 导航 ---
type NavKey = 'home' | 'users' | 'connections' | 'tunnels' | 'scripts' | 'env'
const activeNav = ref<NavKey>('home')

// 侧边栏收起/展开（默认收起；用户手动展开后记住状态）
const sidebarCollapsed = ref(localStorage.getItem('ops_sidebar_collapsed') !== '0')

function toggleSidebar() {
  sidebarCollapsed.value = !sidebarCollapsed.value
  localStorage.setItem('ops_sidebar_collapsed', sidebarCollapsed.value ? '1' : '0')
}

// 侧边栏进入隧道页：清除连接筛选
function openTunnelsPage() {
  tunnelFilterConnId.value = 0
  switchNav('tunnels')
}

function switchNav(key: NavKey) { activeNav.value = key }

// 从连接管理跳转到隧道页时携带连接筛选
const tunnelFilterConnId = ref(0)
function handleNavigate(key: NavKey, connectionId?: number) {
  if (key === 'tunnels') {
    tunnelFilterConnId.value = connectionId || 0
  }
  activeNav.value = key
}

const pageTitle = computed(() => ({ home: '首页', users: '用户管理', connections: '连接管理', tunnels: '隧道管理', scripts: '脚本库', env: '环境管理' }[activeNav.value]))

const currentPage = computed(() => {
  switch (activeNav.value) {
    case 'home': return HomePage
    case 'users': return UserPage
    case 'connections': return ConnectionsPage
    case 'tunnels': return TunnelPage
    case 'scripts': return ScriptsPage
    case 'env': return EnvironmentPage
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
    <!-- ====== 侧边栏 ====== -->
    <aside class="sidebar" :class="{ 'sidebar-collapsed': sidebarCollapsed }">
      <div class="sidebar-logo">
        <div class="logo-icon">
          <img src="/logo.png" class="logo-icon-img" alt="OpsFlash" />
        </div>
        <span v-show="!sidebarCollapsed" class="logo-text">OpsFlash</span>
      </div>
      <div class="sidebar-divider"></div>
      <nav class="sidebar-nav">
        <a class="nav-item" :class="{ active: activeNav === 'home' }" href="#" title="首页" @click.prevent="switchNav('home')">
          <svg viewBox="0 0 24 24" fill="none"><path d="M3 9l9-7 9 7v11a2 2 0 01-2 2H5a2 2 0 01-2-2V9z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span v-show="!sidebarCollapsed">首页</span>
        </a>
        <a class="nav-item" :class="{ active: activeNav === 'connections' }" href="#" title="连接管理" @click.prevent="switchNav('connections')">
          <svg viewBox="0 0 24 24" fill="none"><path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span v-show="!sidebarCollapsed">连接管理</span>
        </a>
        <a class="nav-item" :class="{ active: activeNav === 'tunnels' }" href="#" title="隧道管理" @click.prevent="openTunnelsPage()">
          <svg viewBox="0 0 24 24" fill="none"><path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span v-show="!sidebarCollapsed">隧道管理</span>
        </a>
        <a class="nav-item" :class="{ active: activeNav === 'scripts' }" href="#" title="脚本库" @click.prevent="switchNav('scripts')">
          <svg viewBox="0 0 24 24" fill="none"><polyline points="4 17 10 11 4 5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="12" y1="19" x2="20" y2="19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
          <span v-show="!sidebarCollapsed">脚本库</span>
        </a>
        <a class="nav-item" :class="{ active: activeNav === 'env' }" href="#" title="环境管理" @click.prevent="switchNav('env')">
          <svg viewBox="0 0 24 24" fill="none"><path d="M3 7a2 2 0 012-2h14a2 2 0 012 2v3a2 2 0 010 4v3a2 2 0 01-2 2H5a2 2 0 01-2-2v-3a2 2 0 010-4V7z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><circle cx="8" cy="12" r="1.5" fill="currentColor"/><circle cx="16" cy="12" r="1.5" fill="currentColor"/></svg>
          <span v-show="!sidebarCollapsed">环境管理</span>
        </a>
        <a class="nav-item" :class="{ active: activeNav === 'users' }" href="#" title="用户管理" @click.prevent="switchNav('users')">
          <svg viewBox="0 0 24 24" fill="none"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="9" cy="7" r="4" stroke="currentColor" stroke-width="2"/><path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span v-show="!sidebarCollapsed">用户管理</span>
        </a>
      </nav>

      <!-- 侧边栏收起/展开按钮 -->
      <div class="sidebar-footer">
        <button
          class="sidebar-toggle"
          :title="sidebarCollapsed ? '展开菜单' : '收起菜单'"
          @click="toggleSidebar"
        >
          <svg
            viewBox="0 0 24 24" fill="none"
            :class="{ 'chevron-collapsed': sidebarCollapsed }"
          >
            <polyline points="15 18 9 12 15 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          <span v-show="!sidebarCollapsed">收起菜单</span>
        </button>
      </div>
    </aside>

    <!-- ====== 右侧内容 ====== -->
    <div class="main-area">
      <!-- 顶部栏 -->
      <header class="topbar">
        <div class="topbar-left">
          <h1 class="topbar-title">{{ pageTitle }}</h1>
          <!-- 隧道状态徽标：点击跳转到隧道管理 -->
          <button
            v-if="tunnelSummary.total > 0"
            class="tunnel-indicator"
            :class="{
              'tunnel-indicator-active': tunnelActive,
              'tunnel-indicator-warn': tunnelSummary.reconnecting > 0
            }"
            title="隧道状态（点击查看隧道管理）"
            @click="switchNav('tunnels')"
          >
            <span class="tunnel-indicator-dot" :class="{ 'tunnel-indicator-dot-on': tunnelActive }"></span>
            <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
              <path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span class="tunnel-indicator-text">{{ tunnelSummary.running }}/{{ tunnelSummary.total }}</span>
          </button>
        </div>
        <div class="topbar-user">
          <div class="user-avatar">{{ props.username.charAt(0).toUpperCase() }}</div>
          <span class="user-name">{{ props.username }}</span>
          <button class="logout-btn" :disabled="logoutLoading" @click="handleLogout">
            <svg viewBox="0 0 24 24" fill="none"><path d="M9 21H5a2 2 0 01-2-2V5a2 2 0 012-2h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><polyline points="16 17 21 12 16 7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="21" y1="12" x2="9" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            {{ logoutLoading ? '退出中...' : '退出登录' }}
          </button>
        </div>
      </header>

      <!-- 内容区：动态组件切换 -->
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
  </div>
</template>

<style scoped>
/* ========== 布局 ========== */
.layout { display: flex; height: 100vh; height: 100dvh; overflow: hidden; color: var(--text); --wails-draggable: drag; }

/* ========== 侧边栏 ========== */
.sidebar { width: 220px; flex-shrink: 0; display: flex; flex-direction: column; padding: 20px; gap: 8px; background: rgba(255,255,255,0.04); backdrop-filter: blur(16px); -webkit-backdrop-filter: blur(16px); border-right: 1px solid rgba(255,255,255,0.08); --wails-draggable: no-drag; transition: width 0.25s ease; overflow: hidden; }
.sidebar-logo { display: flex; align-items: center; gap: 10px; padding: 4px; }
.logo-icon { width: 30px; height: 30px; flex-shrink: 0; }
.logo-icon img { width: 100%; height: 100%; object-fit: contain; }
.logo-text { font-size: 18px; font-weight: 700; color: #fff; letter-spacing: 0.02em; white-space: nowrap; }
.sidebar-divider { height: 1px; background: rgba(255,255,255,0.12); margin: 4px 0; }
.sidebar-nav { display: flex; flex-direction: column; gap: 4px; margin-top: 4px; }
.nav-item { display: flex; align-items: center; gap: 10px; padding: 10px; border-radius: 10px; color: rgba(255,255,255,0.6); font-size: 14px; font-weight: 500; text-decoration: none; transition: all 0.2s ease; cursor: pointer; white-space: nowrap; }
.nav-item svg { width: 20px; height: 20px; flex-shrink: 0; }
.nav-item:hover { background: rgba(255,255,255,0.06); color: rgba(255,255,255,0.85); }
.nav-item.active { background: rgba(255,0,110,0.15); color: #FF77B0; font-weight: 700; }

/* 侧边栏收起态 */
.sidebar-collapsed { width: 64px; padding-left: 12px; padding-right: 12px; }
.sidebar-collapsed .sidebar-logo { justify-content: center; padding: 4px 0; }
.sidebar-collapsed .nav-item { justify-content: center; padding: 10px 0; }
.sidebar-collapsed .sidebar-toggle span { display: none; }
.sidebar-collapsed .sidebar-toggle { padding: 10px 0; }

/* 收起/展开按钮（底部） */
.sidebar-footer { margin-top: auto; padding-top: 8px; }
.sidebar-toggle { display: flex; align-items: center; justify-content: center; gap: 8px; width: 100%; padding: 10px; border: none; border-radius: 10px; background: transparent; color: rgba(255,255,255,0.5); font-size: 13px; cursor: pointer; transition: all 0.2s ease; white-space: nowrap; }
.sidebar-toggle:hover { background: rgba(255,255,255,0.06); color: rgba(255,255,255,0.85); }
.sidebar-toggle svg { width: 18px; height: 18px; flex-shrink: 0; transition: transform 0.25s ease; }
.sidebar-toggle .chevron-collapsed { transform: rotate(180deg); }

/* ========== 主区域 ========== */
.main-area { flex: 1; display: flex; flex-direction: column; min-width: 0; background: var(--ink); }

/* ========== 顶部栏 ========== */
.topbar { display: flex; align-items: center; justify-content: space-between; height: 56px; padding: 0 24px; background: rgba(255,255,255,0.03); border-bottom: 1px solid rgba(255,255,255,0.08); flex-shrink: 0; --wails-draggable: no-drag; }
.topbar-left { display: flex; align-items: center; gap: 12px; min-width: 0; }
.topbar-title { font-size: 18px; font-weight: 700; color: #fff; margin: 0; white-space: nowrap; }

/* 隧道状态徽标 */
.tunnel-indicator {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  padding: 4px 10px;
  border: 1px solid rgba(255,255,255,0.12);
  border-radius: 999px;
  background: rgba(255,255,255,0.04);
  color: rgba(255,255,255,0.55);
  font-size: 12px;
  font-weight: 500;
  cursor: pointer;
  transition: all 0.2s ease;
  white-space: nowrap;
}
.tunnel-indicator:hover { background: rgba(255,255,255,0.08); color: rgba(255,255,255,0.9); }
.tunnel-indicator-active {
  border-color: rgba(52,211,153,0.4);
  background: rgba(52,211,153,0.1);
  color: #34D399;
}
.tunnel-indicator-warn {
  border-color: rgba(251,191,36,0.4);
  background: rgba(251,191,36,0.1);
  color: #FBBF24;
}
.tunnel-indicator-dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: rgba(255,255,255,0.25);
  flex-shrink: 0;
}
.tunnel-indicator-dot-on {
  background: #34D399;
  box-shadow: 0 0 6px rgba(52,211,153,0.6);
  animation: tunnel-dot-pulse 2s ease-in-out infinite;
}
@keyframes tunnel-dot-pulse {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.45; }
}
.tunnel-indicator-text { font-variant-numeric: tabular-nums; }
.topbar-user { display: flex; align-items: center; gap: 12px; }
.user-avatar { width: 32px; height: 32px; border-radius: 50%; background: linear-gradient(135deg, #FF006E, #FF77B0); display: flex; align-items: center; justify-content: center; font-size: 14px; font-weight: 700; color: #fff; flex-shrink: 0; }
.user-name { font-size: 14px; font-weight: 500; color: rgba(255,255,255,0.75); }
.logout-btn { display: inline-flex; align-items: center; gap: 4px; padding: 6px 12px; background: rgba(255,0,110,0.15); border: 1px solid rgba(255,0,110,0.3); border-radius: 8px; color: #FF77B0; font-size: 12px; font-weight: 500; cursor: pointer; transition: all 0.2s ease; }
.logout-btn:hover:not(:disabled) { background: rgba(255,0,110,0.25); border-color: rgba(255,0,110,0.5); }
.logout-btn:disabled { opacity: 0.5; cursor: not-allowed; }
.logout-btn svg { width: 16px; height: 16px; }

/* ========== 内容区 ========== */
.content { flex: 1; padding: 40px; overflow-y: auto; --wails-draggable: no-drag; }
</style>
