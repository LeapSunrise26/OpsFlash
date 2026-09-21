<script setup lang="ts">
import { ref, onMounted, onUnmounted } from 'vue'
import { DashboardService } from '../../../bindings/opsflash/server'

const props = defineProps<{ token: string; username?: string }>()

const emit = defineEmits<{
  (e: 'navigate', key: string): void
}>()

// --- 类型定义 ---
interface ExecutionLog {
  id: number
  operation_type: string
  target_id: number
  target_name: string
  action: string
  environment_name: string
  status: string
  duration_ms: number
  started_at: string
  username: string
}

interface TopExecutedItem {
  operation_type: string
  target_name: string
  execute_count: number
}

// --- 数据 ---
const todaySuccess = ref(0)
const todayFailed = ref(0)
const successRate = ref(0)
const runningTunnels = ref(0)
const topExecuted = ref<TopExecutedItem[]>([])
const recentExecutions = ref<ExecutionLog[]>([])
const recentFailed = ref<ExecutionLog[]>([])
const loading = ref(true)
const refreshing = ref(false)

// --- 加载数据 ---
async function loadDashboard() {
  if (!loading.value) refreshing.value = true
  try {
    const res: any = await DashboardService.GetDashboardStats(props.token)
    if (res) {
      todaySuccess.value = res.today_success || 0
      todayFailed.value = res.today_failed || 0
      successRate.value = Math.round(res.success_rate || 0)
      runningTunnels.value = res.running_tunnels || 0
      topExecuted.value = res.top_executed || []
      recentExecutions.value = res.recent_executions || []
      recentFailed.value = res.recent_failed || []
    }
  } catch (e) {
    console.error('加载仪表盘数据失败', e)
  } finally {
    loading.value = false
    refreshing.value = false
  }
}

// --- 格式化函数 ---
function formatOperationType(type: string): string {
  const map: Record<string, string> = {
    command: '命令',
    script: '脚本',
    batch_task: '批量任务',
    tunnel: '隧道'
  }
  return map[type] || type
}

function formatStatus(status: string): string {
  const map: Record<string, string> = {
    success: '成功',
    failed: '失败',
    running: '执行中',
    timeout: '超时',
    stopped: '已停止'
  }
  return map[status] || status
}

function getStatusClass(status: string): string {
  const map: Record<string, string> = {
    success: 'status-success',
    failed: 'status-failed',
    running: 'status-running',
    timeout: 'status-timeout',
    stopped: 'status-stopped'
  }
  return map[status] || ''
}

function formatDuration(ms: number): string {
  if (ms < 1000) return ms + 'ms'
  if (ms < 60000) return (ms / 1000).toFixed(1) + 's'
  const minutes = Math.floor(ms / 60000)
  const seconds = Math.floor((ms % 60000) / 1000)
  return `${minutes}m ${seconds}s`
}

function goToPage(key: string) {
  emit('navigate', key as any)
}

function goToTargetByType(type: string) {
  const map: Record<string, string> = {
    command: 'ops',
    script: 'scripts',
    batch_task: 'batch',
    tunnel: 'tunnels'
  }
  emit('navigate', (map[type] || 'logs') as any)
}

function formatTime(time: string): string {
  if (!time) return '-'
  const d = new Date(time)
  const now = new Date()
  const diffMs = now.getTime() - d.getTime()
  const diffMin = Math.floor(diffMs / 60000)
  if (diffMin < 1) return '刚刚'
  if (diffMin < 60) return diffMin + '分钟前'
  const diffHour = Math.floor(diffMin / 60)
  if (diffHour < 24) return diffHour + '小时前'
  const diffDay = Math.floor(diffHour / 24)
  return diffDay + '天前'
}

const typeIconClass = (type: string) => 'type-' + type

// --- 自动刷新 ---
let refreshTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  loadDashboard()
  refreshTimer = setInterval(loadDashboard, 30000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})
</script>

<template>
  <div class="dashboard">
    <!-- 统计卡片 -->
    <div class="stats-row">
      <div class="stat-card clickable" @click="goToPage('logs')">
        <div class="stat-info">
          <span class="stat-label">今日成功</span>
          <span class="stat-value stat-value-success">{{ todaySuccess }}</span>
        </div>
        <div class="stat-icon stat-icon-green">
          <svg viewBox="0 0 24 24" fill="none"><path d="M22 11.08V12a10 10 0 11-5.93-9.14" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><polyline points="22 4 12 14.01 9 11.01" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
      </div>
      <div class="stat-card clickable" :class="{ 'stat-card-warn': todayFailed > 0 }" @click="goToPage('logs')">
        <div class="stat-info">
          <span class="stat-label">今日失败</span>
          <span class="stat-value stat-value-failed">{{ todayFailed }}</span>
        </div>
        <div class="stat-icon stat-icon-red">
          <svg viewBox="0 0 24 24" fill="none"><path d="M10.29 3.86L1.82 18a2 2 0 001.71 3h16.94a2 2 0 001.71-3L13.71 3.86a2 2 0 00-3.42 0z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="12" y1="9" x2="12" y2="13" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="12" y1="17" x2="12.01" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        </div>
      </div>
      <div class="stat-card clickable" @click="goToPage('logs')">
        <div class="stat-info">
          <span class="stat-label">成功率</span>
          <span class="stat-value">{{ successRate }}%</span>
        </div>
        <div class="stat-icon stat-icon-purple">
          <svg viewBox="0 0 24 24" fill="none"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
      </div>
      <div class="stat-card clickable" @click="goToPage('tunnels')">
        <div class="stat-info">
          <span class="stat-label">已启用隧道</span>
          <span class="stat-value">{{ runningTunnels }}</span>
        </div>
        <div class="stat-icon stat-icon-cyan">
          <svg viewBox="0 0 24 24" fill="none"><path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
      </div>
    </div>

    <!-- 列表区域 -->
    <div class="lists-row">
      <!-- 最近7天最常执行 -->
      <div class="card">
        <div class="card-header">
          <h3 class="card-title">最近7天最常执行 TOP10</h3>
        </div>
        <div v-if="loading" class="card-empty">加载中...</div>
        <div v-else-if="topExecuted.length === 0" class="card-empty">暂无执行记录</div>
        <div v-else class="top-list">
          <div v-for="(item, idx) in topExecuted" :key="idx" class="top-item clickable" @click="goToTargetByType(item.operation_type)">
            <div class="top-rank" :class="{ 'top-rank-hot': idx < 3 }">{{ idx + 1 }}</div>
            <div class="top-info">
              <span class="type-badge" :class="typeIconClass(item.operation_type)">{{ formatOperationType(item.operation_type) }}</span>
              <span class="top-name">{{ item.target_name }}</span>
            </div>
            <div class="top-count">{{ item.execute_count }} 次</div>
          </div>
        </div>
      </div>

      <!-- 最近执行 + 最近失败 -->
      <div class="card-lists">
        <!-- 最近执行 -->
        <div class="card">
          <div class="card-header">
            <h3 class="card-title">最近执行 TOP10</h3>
          </div>
          <div v-if="loading" class="card-empty">加载中...</div>
          <div v-else-if="recentExecutions.length === 0" class="card-empty">暂无执行记录</div>
          <div v-else class="recent-list">
            <div v-for="log in recentExecutions" :key="log.id" class="recent-item clickable" @click="goToTargetByType(log.operation_type)">
              <div class="recent-left">
                <span class="type-badge" :class="typeIconClass(log.operation_type)">{{ formatOperationType(log.operation_type) }}</span>
                <span class="recent-name">{{ log.target_name }}</span>
              </div>
              <div class="recent-right">
                <span class="status-badge" :class="getStatusClass(log.status)">{{ formatStatus(log.status) }}</span>
                <span class="recent-duration">{{ formatDuration(log.duration_ms) }}</span>
                <span class="recent-time">{{ formatTime(log.started_at) }}</span>
              </div>
            </div>
          </div>
        </div>

        <!-- 最近失败 -->
        <div class="card">
          <div class="card-header">
            <h3 class="card-title">最近失败 TOP10</h3>
          </div>
          <div v-if="loading" class="card-empty">加载中...</div>
          <div v-else-if="recentFailed.length === 0" class="card-empty">暂无失败记录</div>
          <div v-else class="recent-list">
            <div v-for="log in recentFailed" :key="log.id" class="recent-item clickable" @click="goToTargetByType(log.operation_type)">
              <div class="recent-left">
                <span class="type-badge" :class="typeIconClass(log.operation_type)">{{ formatOperationType(log.operation_type) }}</span>
                <span class="recent-name">{{ log.target_name }}</span>
              </div>
              <div class="recent-right">
                <span class="recent-duration">{{ formatDuration(log.duration_ms) }}</span>
                <span class="recent-time">{{ formatTime(log.started_at) }}</span>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.dashboard { display: flex; flex-direction: column; gap: 24px; }

/* 统计卡片 */
.stats-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 16px; }
.stat-card {
  display: flex; align-items: center; justify-content: space-between;
  padding: 20px; background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08);
  border-radius: 14px; cursor: pointer; transition: all 0.25s ease;
}
.stat-card:hover { background: rgba(255,255,255,0.07); transform: translateY(-2px); box-shadow: 0 8px 24px rgba(0,0,0,0.2); }
.stat-card-warn { border-color: rgba(248,113,113,0.3); }
.stat-info { display: flex; flex-direction: column; gap: 6px; }
.stat-label { font-size: 13px; color: rgba(255,255,255,0.45); font-weight: 500; }
.stat-value { font-size: 28px; font-weight: 700; color: #fff; }
.stat-value-success { color: #34D399; }
.stat-value-failed { color: #F87171; }
.stat-icon { width: 44px; height: 44px; border-radius: 12px; display: flex; align-items: center; justify-content: center; }
.stat-icon svg { width: 22px; height: 22px; }
.stat-icon-blue { background: rgba(96,165,250,0.15); color: #60A5FA; }
.stat-icon-green { background: rgba(52,211,153,0.15); color: #34D399; }
.stat-icon-red { background: rgba(248,113,113,0.15); color: #F87171; }
.stat-icon-purple { background: rgba(167,139,250,0.15); color: #A78BFA; }
.stat-icon-cyan { background: rgba(34,211,238,0.15); color: #22D3EE; }

/* 列表区域 */
.lists-row { display: grid; grid-template-columns: 320px 1fr; gap: 16px; }
.card-lists { display: flex; flex-direction: column; gap: 16px; }

/* 卡片 */
.card {
  background: rgba(255,255,255,0.04); border: 1px solid rgba(255,255,255,0.08);
  border-radius: 14px; overflow: hidden;
}
.card-header { display: flex; align-items: center; justify-content: space-between; padding: 14px 18px; border-bottom: 1px solid rgba(255,255,255,0.06); }
.card-title { font-size: 14px; font-weight: 600; color: rgba(255,255,255,0.85); margin: 0; }
.card-link { background: none; border: none; color: #FF77B0; font-size: 12px; cursor: pointer; padding: 0; }
.card-link:hover { text-decoration: underline; }
.card-empty { padding: 32px 20px; text-align: center; color: rgba(255,255,255,0.35); font-size: 13px; }

/* 最常执行列表 */
.top-list { display: flex; flex-direction: column; }
.top-item {
  display: flex; align-items: center; gap: 12px;
  padding: 10px 18px; border-bottom: 1px solid rgba(255,255,255,0.04);
  cursor: pointer; transition: background 0.15s ease;
}
.top-item:last-child { border-bottom: none; }
.top-item:hover { background: rgba(255,255,255,0.03); }
.top-rank {
  width: 22px; height: 22px; border-radius: 6px; display: flex; align-items: center; justify-content: center;
  font-size: 12px; font-weight: 600; color: rgba(255,255,255,0.4); background: rgba(255,255,255,0.06);
  flex-shrink: 0;
}
.top-rank-hot { background: linear-gradient(135deg, #FF006E, #FF77B0); color: #fff; }
.top-info { display: flex; align-items: center; gap: 8px; min-width: 0; flex: 1; }
.top-name { font-size: 13px; color: rgba(255,255,255,0.75); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.top-count { font-size: 12px; color: rgba(255,255,255,0.4); font-variant-numeric: tabular-nums; flex-shrink: 0; }

/* 最近执行/失败列表 */
.recent-list { display: flex; flex-direction: column; }
.recent-item {
  display: flex; align-items: center; justify-content: space-between;
  padding: 10px 18px; border-bottom: 1px solid rgba(255,255,255,0.04);
  transition: background 0.15s ease;
}
.recent-item:last-child { border-bottom: none; }
.recent-item:hover { background: rgba(255,255,255,0.03); }
.recent-left { display: flex; align-items: center; gap: 8px; min-width: 0; }
.recent-name { font-size: 13px; color: rgba(255,255,255,0.75); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.recent-right { display: flex; align-items: center; gap: 10px; flex-shrink: 0; }
.recent-duration { font-size: 12px; color: rgba(255,255,255,0.4); font-variant-numeric: tabular-nums; }
.recent-time { font-size: 12px; color: rgba(255,255,255,0.35); }

/* 类型徽标 */
.type-badge { display: inline-flex; padding: 2px 6px; border-radius: 4px; font-size: 11px; font-weight: 500; flex-shrink: 0; }
.type-command { background: rgba(96,165,250,0.15); color: #60A5FA; }
.type-script { background: rgba(52,211,153,0.15); color: #34D399; }
.type-batch_task { background: rgba(167,139,250,0.15); color: #A78BFA; }
.type-tunnel { background: rgba(251,191,36,0.15); color: #FBBF24; }

/* 状态徽标 */
.status-badge { display: inline-flex; padding: 2px 6px; border-radius: 4px; font-size: 11px; font-weight: 500; }
.status-success { background: rgba(52,211,153,0.15); color: #34D399; }
.status-failed { background: rgba(248,113,113,0.15); color: #F87171; }
.status-running { background: rgba(96,165,250,0.15); color: #60A5FA; }
.status-timeout { background: rgba(251,191,36,0.15); color: #FBBF24; }
.status-stopped { background: rgba(255,255,255,0.1); color: rgba(255,255,255,0.6); }
</style>
