<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { DashboardService } from '../../../bindings/opsflash/server'

const props = defineProps<{ token: string }>()

// --- 类型定义 ---
interface ExecutionLog {
  id: number
  operation_type: string
  target_id: number
  target_name: string
  action: string
  environment_id: number
  environment_name: string
  mode: string
  connection_id: number
  connection_name: string
  status: string
  exit_code: number
  output: string
  error_message: string
  started_at: string
  finished_at: string | null
  duration_ms: number
  username: string
  created_at: string
}

// --- 数据 ---
const logs = ref<ExecutionLog[]>([])
const loading = ref(true)
const total = ref(0)
const currentPage = ref(1)
const pageSize = ref(20)

// --- 筛选 ---
const filterType = ref('')
const filterStatus = ref('')
const searchQuery = ref('')

// --- 清空确认弹窗 ---
const showClearConfirm = ref(false)
const clearing = ref(false)

function openClearConfirm() {
  showClearConfirm.value = true
}

function closeClearConfirm() {
  showClearConfirm.value = false
}

async function confirmClearLogs() {
  clearing.value = true
  try {
    const res = await DashboardService.ClearExecutionLogs({
      token: props.token,
      operationType: filterType.value,
      status: filterStatus.value,
      search: searchQuery.value
    })
    if (res.success) {
      showClearConfirm.value = false
      currentPage.value = 1
      await loadLogs()
    }
  } catch (e) {
    console.error('清空执行记录失败', e)
  } finally {
    clearing.value = false
  }
}

// --- 操作类型选项 ---
const operationTypes = [
  { value: '', label: '全部类型' },
  { value: 'command', label: '命令执行' },
  { value: 'script', label: '脚本执行' },
  { value: 'batch_task', label: '批量任务' },
  { value: 'tunnel', label: '隧道操作' }
]

// --- 状态选项 ---
const statusOptions = [
  { value: '', label: '全部状态' },
  { value: 'success', label: '成功' },
  { value: 'failed', label: '失败' },
  { value: 'running', label: '执行中' },
  { value: 'timeout', label: '超时' },
  { value: 'stopped', label: '已停止' }
]

// --- 加载数据 ---
async function loadLogs() {
  loading.value = true
  try {
    const res = await DashboardService.GetExecutionLogs({
      token: props.token,
      page: currentPage.value,
      pageSize: pageSize.value,
      operationType: filterType.value,
      status: filterStatus.value,
      environmentId: 0,
      search: searchQuery.value
    })
    if (res && res.success) {
      logs.value = res.items || []
      total.value = res.total || 0
    }
  } catch (e) {
    console.error('加载执行记录失败', e)
  } finally {
    loading.value = false
  }
}

// --- 分页 ---
const totalPages = computed(() => Math.ceil(total.value / pageSize.value) || 1)

// 计算可见页码（最多显示7个）
const visiblePages = computed(() => {
  const total = totalPages.value
  const current = currentPage.value
  const pages: (number | string)[] = []
  
  if (total <= 7) {
    // 总页数<=7，显示所有页码
    for (let i = 1; i <= total; i++) pages.push(i)
  } else {
    // 始终显示第1页和最后1页
    if (current <= 4) {
      // 当前页在前4页
      for (let i = 1; i <= 5; i++) pages.push(i)
      pages.push('...')
      pages.push(total)
    } else if (current >= total - 3) {
      // 当前页在后4页
      pages.push(1)
      pages.push('...')
      for (let i = total - 4; i <= total; i++) pages.push(i)
    } else {
      // 当前页在中间
      pages.push(1)
      pages.push('...')
      for (let i = current - 1; i <= current + 1; i++) pages.push(i)
      pages.push('...')
      pages.push(total)
    }
  }
  return pages
})

function goToPage(page: number) {
  if (page >= 1 && page <= totalPages.value) {
    currentPage.value = page
    loadLogs()
  }
}

// --- 筛选变化 ---
function onFilterChange() {
  currentPage.value = 1
  loadLogs()
}

// --- 判断是否有筛选条件 ---
const hasActiveFilters = computed(() => {
  return filterType.value !== '' || filterStatus.value !== '' || searchQuery.value !== ''
})

// --- 清空确认弹窗文案 ---
const clearConfirmMessage = computed(() => {
  if (hasActiveFilters.value) {
    return '确定要清空当前筛选条件下的执行记录吗？此操作不可撤销。'
  }
  return '确定要清空所有执行记录吗？此操作不可撤销。'
})

// --- 搜索 ---
let searchTimeout: ReturnType<typeof setTimeout> | null = null
function onSearchInput() {
  if (searchTimeout) clearTimeout(searchTimeout)
  searchTimeout = setTimeout(() => {
    currentPage.value = 1
    loadLogs()
  }, 300)
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

function formatAction(action: string): string {
  const map: Record<string, string> = {
    execute: '执行',
    start: '启动',
    stop: '停止'
  }
  return map[action] || action
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

function formatTime(time: string): string {
  if (!time) return '-'
  return time.replace('T', ' ').slice(0, 19)
}

// --- 详情弹窗 ---
const showDetailModal = ref(false)
const detailLog = ref<ExecutionLog | null>(null)

function openDetail(log: ExecutionLog) {
  detailLog.value = log
  showDetailModal.value = true
}

function closeDetail() {
  showDetailModal.value = false
  detailLog.value = null
}

// --- 自动刷新 ---
let refreshTimer: ReturnType<typeof setInterval> | null = null

onMounted(() => {
  loadLogs()
  // 每30秒自动刷新
  refreshTimer = setInterval(loadLogs, 30000)
})

onUnmounted(() => {
  if (refreshTimer) {
    clearInterval(refreshTimer)
    refreshTimer = null
  }
})
</script>

<template>
  <div class="logs-page">
    <!-- 工具栏 -->
    <div class="toolbar">
      <div class="toolbar-left">
        <div class="filter-group">
          <select v-model="filterType" class="filter-select" @change="onFilterChange">
            <option v-for="opt in operationTypes" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
          <select v-model="filterStatus" class="filter-select" @change="onFilterChange">
            <option v-for="opt in statusOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</option>
          </select>
          <div class="search-box">
            <svg class="search-icon" viewBox="0 0 24 24" fill="none"><circle cx="11" cy="11" r="8" stroke="currentColor" stroke-width="2"/><path d="M21 21l-4.35-4.35" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            <input v-model="searchQuery" type="text" class="search-input" placeholder="搜索目标名称..." @input="onSearchInput"/>
          </div>
          <button class="btn-clear-logs" @click="openClearConfirm">
            <svg viewBox="0 0 24 24" fill="none" width="14" height="14"><path d="M3 6h18M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="10" y1="11" x2="10" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="14" y1="11" x2="14" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            清空记录
          </button>
        </div>
      </div>
      <div class="toolbar-right">
        <span class="total-count">共 {{ total }} 条记录</span>
        <button class="btn-refresh" @click="loadLogs" :disabled="loading">
          <svg viewBox="0 0 24 24" fill="none" :class="{ 'spinning': loading }"><path d="M23 4v6h-6M1 20v-6h6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M3.51 9a9 9 0 0114.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0020.49 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          刷新
        </button>
      </div>
    </div>

    <!-- 表格 -->
    <div class="table-card">
      <div class="table-header-row">
        <div class="th-type">类型</div>
        <div class="th-action">操作</div>
        <div class="th-target">目标名称</div>
        <div class="th-status">状态</div>
        <div class="th-duration">耗时</div>
        <div class="th-time">开始时间</div>
        <div class="th-user">操作者</div>
        <div class="th-detail">详情</div>
      </div>
      <div class="table-divider"></div>
      <div v-if="loading" class="table-empty">加载中...</div>
      <template v-else>
        <div v-for="(log, idx) in logs" :key="log.id" class="table-row" :class="{ 'row-alt': idx % 2 === 1 }">
          <div class="td-type">
            <span class="type-badge" :class="'type-' + log.operation_type">{{ formatOperationType(log.operation_type) }}</span>
          </div>
          <div class="td-action">{{ formatAction(log.action) }}</div>
          <div class="td-target" :title="log.target_name">{{ log.target_name }}</div>
          <div class="td-status">
            <span class="status-badge" :class="getStatusClass(log.status)">{{ formatStatus(log.status) }}</span>
          </div>
          <div class="td-duration">{{ formatDuration(log.duration_ms) }}</div>
          <div class="td-time">{{ formatTime(log.started_at) }}</div>
          <div class="td-user">{{ log.username || '-' }}</div>
          <div class="td-detail">
            <button class="action-btn action-view" title="查看详情" @click="openDetail(log)">
              <svg viewBox="0 0 24 24" fill="none"><path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="2"/></svg>
            </button>
          </div>
        </div>
        <div v-if="!loading && logs.length === 0" class="table-empty">暂无执行记录</div>
      </template>
    </div>

    <!-- 分页 -->
    <div class="pagination" v-if="totalPages > 1">
      <span class="page-info">共 {{ total }} 条，每页 {{ pageSize }} 条</span>
      <div class="page-nav">
        <button class="page-btn" :disabled="currentPage <= 1" @click="goToPage(currentPage - 1)">
          <svg viewBox="0 0 24 24" fill="none"><polyline points="15 18 9 12 15 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
        <template v-for="(page, idx) in visiblePages" :key="idx">
          <span v-if="page === '...'" class="page-ellipsis">...</span>
          <button v-else class="page-btn" :class="{ 'page-active': page === currentPage }" @click="goToPage(page as number)">{{ page }}</button>
        </template>
        <button class="page-btn" :disabled="currentPage >= totalPages" @click="goToPage(currentPage + 1)">
          <svg viewBox="0 0 24 24" fill="none"><polyline points="9 18 15 12 9 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </button>
      </div>
    </div>

    <!-- 详情弹窗 -->
    <Teleport to="body">
      <div v-if="showDetailModal && detailLog" class="modal-overlay" @click.self="closeDetail">
        <div class="modal-card modal-card-lg">
          <div class="modal-header">
            <div class="modal-header-left">
              <div class="modal-icon icon-detail">
                <svg viewBox="0 0 24 24" fill="none"><path d="M14 2H6a2 2 0 00-2 2v16a2 2 0 002 2h12a2 2 0 002-2V8z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><polyline points="14 2 14 8 20 8" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><line x1="16" y1="13" x2="8" y2="13" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="16" y1="17" x2="8" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
              </div>
              <h2 class="modal-title">执行详情</h2>
            </div>
            <button class="modal-close" @click="closeDetail">
              <svg viewBox="0 0 24 24" fill="none"><line x1="18" y1="6" x2="6" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="6" y1="6" x2="18" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            </button>
          </div>
          <div class="modal-body">
            <div class="detail-grid">
              <div class="detail-item">
                <span class="detail-label">操作类型</span>
                <span class="detail-value"><span class="type-badge" :class="'type-' + detailLog.operation_type">{{ formatOperationType(detailLog.operation_type) }}</span></span>
              </div>
              <div class="detail-item">
                <span class="detail-label">操作动作</span>
                <span class="detail-value">{{ formatAction(detailLog.action) }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">目标名称</span>
                <span class="detail-value">{{ detailLog.target_name }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">状态</span>
                <span class="detail-value"><span class="status-badge" :class="getStatusClass(detailLog.status)">{{ formatStatus(detailLog.status) }}</span></span>
              </div>
              <div class="detail-item">
                <span class="detail-label">执行耗时</span>
                <span class="detail-value">{{ formatDuration(detailLog.duration_ms) }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">操作者</span>
                <span class="detail-value">{{ detailLog.username || '-' }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">环境</span>
                <span class="detail-value">{{ detailLog.environment_name || '-' }}</span>
              </div>
              <div class="detail-item">
                <span class="detail-label">连接</span>
                <span class="detail-value">{{ detailLog.connection_name || '-' }}</span>
              </div>
              <div class="detail-item detail-full">
                <span class="detail-label">开始时间</span>
                <span class="detail-value">{{ formatTime(detailLog.started_at) }}</span>
              </div>
              <div class="detail-item detail-full">
                <span class="detail-label">结束时间</span>
                <span class="detail-value">{{ formatTime(detailLog.finished_at || '') }}</span>
              </div>
            </div>
            <div class="detail-section" v-if="detailLog.error_message">
              <span class="detail-label">错误信息</span>
              <pre class="detail-output error-output">{{ detailLog.error_message }}</pre>
            </div>
            <div class="detail-section" v-if="detailLog.output">
              <span class="detail-label">执行输出</span>
              <pre class="detail-output">{{ detailLog.output }}</pre>
            </div>
          </div>
        </div>
      </div>
    </Teleport>

    <!-- 清空确认弹窗 -->
    <Teleport to="body">
      <div v-if="showClearConfirm" class="modal-overlay" @click.self="closeClearConfirm">
        <div class="modal-card modal-card-sm">
          <div class="modal-header">
            <div class="modal-header-left">
              <div class="modal-icon icon-delete">
                <svg viewBox="0 0 24 24" fill="none"><path d="M3 6h18M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </div>
              <h2 class="modal-title">确认清空</h2>
            </div>
            <button class="modal-close" @click="closeClearConfirm">
              <svg viewBox="0 0 24 24" fill="none"><line x1="18" y1="6" x2="6" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="6" y1="6" x2="18" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
            </button>
          </div>
          <div class="modal-body">
            <p class="delete-warning">{{ clearConfirmMessage }}</p>
            <div class="modal-actions">
              <button type="button" class="btn-cancel" :disabled="clearing" @click="closeClearConfirm">取消</button>
              <button type="button" class="btn-danger" :disabled="clearing" @click="confirmClearLogs">
                <template v-if="clearing"><span class="spinner"></span>清空中...</template>
                <template v-else>确认清空</template>
              </button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.logs-page { display: flex; flex-direction: column; gap: 20px; }

/* 工具栏 */
.toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; flex-wrap: wrap; }
.toolbar-left { display: flex; align-items: center; gap: 12px; flex: 1; min-width: 0; }
.toolbar-right { display: flex; align-items: center; gap: 12px; flex-shrink: 0; }
.filter-group { display: flex; align-items: center; gap: 8px; }
.filter-select { height: 36px; padding: 0 12px; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.12); border-radius: 8px; color: rgba(255,255,255,0.85); font-size: 13px; cursor: pointer; outline: none; min-width: 110px; appearance: none; background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='12' height='12' viewBox='0 0 24 24' fill='none' stroke='rgba(255,255,255,0.5)' stroke-width='2' stroke-linecap='round' stroke-linejoin='round'%3E%3Cpolyline points='6 9 12 15 18 9'%3E%3C/polyline%3E%3C/svg%3E"); background-repeat: no-repeat; background-position: right 10px center; padding-right: 28px; }
.filter-select:hover { border-color: rgba(255,255,255,0.2); }
.filter-select:focus { border-color: rgba(255,0,110,0.5); }
.filter-select option { background: #1a1b23; color: rgba(255,255,255,0.85); }
.btn-clear-logs { display: inline-flex; align-items: center; gap: 4px; height: 36px; padding: 0 12px; border: 1px solid rgba(255,0,110,0.3); border-radius: 8px; background: rgba(255,0,110,0.1); color: #FF77B0; font-size: 12px; font-weight: 500; cursor: pointer; transition: all 0.2s ease; white-space: nowrap; }
.btn-clear-logs:hover { background: rgba(255,0,110,0.2); border-color: rgba(255,0,110,0.5); }
.btn-clear-logs svg { width: 14px; height: 14px; }
.search-box { display: flex; align-items: center; gap: 8px; flex: 1; max-width: 300px; height: 36px; padding: 0 12px; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.12); border-radius: 8px; transition: border-color 0.2s ease; }
.search-box:focus-within { border-color: rgba(255,0,110,0.5); }
.search-icon { width: 16px; height: 16px; color: rgba(255,255,255,0.35); pointer-events: none; flex-shrink: 0; }
.search-input { flex: 1; background: transparent; border: none; outline: none; color: rgba(255,255,255,0.85); font-size: 13px; }
.search-input::placeholder { color: rgba(255,255,255,0.35); }
.total-count { font-size: 13px; color: rgba(255,255,255,0.5); }
.btn-refresh { display: inline-flex; align-items: center; gap: 6px; padding: 8px 14px; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.12); border-radius: 8px; color: rgba(255,255,255,0.75); font-size: 13px; font-weight: 500; cursor: pointer; transition: all 0.2s ease; }
.btn-refresh:hover:not(:disabled) { background: rgba(255,255,255,0.1); color: #fff; }
.btn-refresh:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-refresh svg { width: 14px; height: 14px; }
.spinning { animation: spin 1s linear infinite; }
@keyframes spin { from { transform: rotate(0deg); } to { transform: rotate(360deg); } }

/* 表格 */
.table-card { background: rgba(255,255,255,0.03); border: 1px solid rgba(255,255,255,0.08); border-radius: 12px; overflow: hidden; }
.table-header-row { display: grid; grid-template-columns: 80px 70px 1fr 80px 80px 160px 80px 50px; gap: 12px; padding: 14px 20px; font-size: 12px; font-weight: 600; color: rgba(255,255,255,0.45); text-transform: uppercase; letter-spacing: 0.05em; }
.table-divider { height: 1px; background: rgba(255,255,255,0.06); }
.table-row { display: grid; grid-template-columns: 80px 70px 1fr 80px 80px 160px 80px 50px; gap: 12px; padding: 14px 20px; align-items: center; transition: background 0.15s ease; }
.table-row:hover { background: rgba(255,255,255,0.04); }
.row-alt { background: rgba(255,255,255,0.015); }
.table-empty { padding: 48px 20px; text-align: center; color: rgba(255,255,255,0.35); font-size: 14px; }

/* 类型徽标 */
.type-badge { display: inline-flex; align-items: center; padding: 4px 8px; border-radius: 6px; font-size: 12px; font-weight: 500; }
.type-command { background: rgba(96,165,250,0.15); color: #60A5FA; }
.type-script { background: rgba(52,211,153,0.15); color: #34D399; }
.type-batch_task { background: rgba(167,139,250,0.15); color: #A78BFA; }
.type-tunnel { background: rgba(251,191,36,0.15); color: #FBBF24; }

/* 状态徽标 */
.status-badge { display: inline-flex; align-items: center; padding: 4px 8px; border-radius: 6px; font-size: 12px; font-weight: 500; }
.status-success { background: rgba(52,211,153,0.15); color: #34D399; }
.status-failed { background: rgba(248,113,113,0.15); color: #F87171; }
.status-running { background: rgba(96,165,250,0.15); color: #60A5FA; }
.status-timeout { background: rgba(251,191,36,0.15); color: #FBBF24; }
.status-stopped { background: rgba(255,255,255,0.1); color: rgba(255,255,255,0.6); }

/* 操作按钮 */
.action-btn { display: inline-flex; align-items: center; justify-content: center; width: 30px; height: 30px; border: none; border-radius: 6px; background: transparent; color: rgba(255,255,255,0.45); cursor: pointer; transition: all 0.15s ease; }
.action-btn:hover { background: rgba(255,255,255,0.08); color: rgba(255,255,255,0.85); }
.action-btn svg { width: 16px; height: 16px; }

/* 分页 */
.pagination { display: flex; align-items: center; justify-content: space-between; height: 40px; }
.page-info { font-size: 13px; color: rgba(255,255,255,0.45); }
.page-nav { display: flex; align-items: center; gap: 4px; }
.page-btn { width: 32px; height: 32px; border-radius: 8px; border: none; background: rgba(255,255,255,0.06); color: rgba(255,255,255,0.6); font-size: 13px; font-weight: 500; display: flex; align-items: center; justify-content: center; cursor: pointer; transition: all 0.2s ease; }
.page-btn:hover:not(:disabled) { background: rgba(255,255,255,0.12); }
.page-btn:disabled { opacity: 0.3; cursor: not-allowed; }
.page-btn svg { width: 16px; height: 16px; }
.page-active { background: linear-gradient(135deg, #FF006E, #FF77B0); color: #fff; font-weight: 700; }
.page-ellipsis { width: 32px; height: 32px; display: flex; align-items: center; justify-content: center; color: rgba(255,255,255,0.35); font-size: 13px; }

/* 弹窗 */
.modal-overlay { position: fixed; inset: 0; background: rgba(0,0,0,0.6); backdrop-filter: blur(4px); display: flex; align-items: center; justify-content: center; z-index: 1000; padding: 20px; }
.modal-card { background: #1a1b23; border: 1px solid rgba(255,255,255,0.12); border-radius: 16px; width: 100%; max-width: 480px; max-height: 90vh; display: flex; flex-direction: column; overflow: hidden; }
.modal-card-sm { max-width: 420px; }
.modal-card-lg { max-width: 640px; }
.modal-header { display: flex; align-items: center; justify-content: space-between; padding: 20px 24px; border-bottom: 1px solid rgba(255,255,255,0.08); }
.modal-header-left { display: flex; align-items: center; gap: 12px; }
.modal-icon { width: 36px; height: 36px; border-radius: 10px; display: flex; align-items: center; justify-content: center; }
.modal-icon svg { width: 18px; height: 18px; }
.icon-detail { background: rgba(96,165,250,0.15); color: #60A5FA; }
.icon-delete { background: rgba(255,0,110,0.12); color: #FF006E; }
.modal-title { font-size: 16px; font-weight: 600; color: #fff; margin: 0; }
.modal-close { display: flex; align-items: center; justify-content: center; width: 32px; height: 32px; border: none; border-radius: 8px; background: transparent; color: rgba(255,255,255,0.45); cursor: pointer; transition: all 0.15s ease; }
.modal-close:hover { background: rgba(255,255,255,0.08); color: #fff; }
.modal-close svg { width: 18px; height: 18px; }
.modal-body { padding: 20px 24px; overflow-y: auto; }
.modal-actions { display: flex; justify-content: flex-end; gap: 10px; }
.delete-warning { margin: 0; font-size: 15px; color: rgba(255,255,255,0.75); line-height: 1.6; }
.delete-warning strong { color: #fff; }
.btn-cancel { padding: 10px 20px; background: rgba(255,255,255,0.06); border: 1px solid rgba(255,255,255,0.14); border-radius: 10px; color: rgba(255,255,255,0.65); font-size: 14px; font-weight: 500; cursor: pointer; transition: all 0.15s ease; }
.btn-cancel:hover:not(:disabled) { background: rgba(255,255,255,0.1); color: #fff; }
.btn-cancel:disabled { opacity: 0.5; cursor: not-allowed; }
.btn-danger { display: inline-flex; align-items: center; gap: 6px; padding: 10px 20px; background: rgba(255,0,110,0.9); border: none; border-radius: 10px; color: #fff; font-size: 14px; font-weight: 600; cursor: pointer; transition: all 0.15s ease; }
.btn-danger:hover:not(:disabled) { background: #FF006E; box-shadow: 0 4px 16px rgba(255,0,110,0.4); }
.btn-danger:disabled { opacity: 0.6; cursor: not-allowed; }

/* 详情网格 */
.detail-grid { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
.detail-item { display: flex; flex-direction: column; gap: 4px; }
.detail-full { grid-column: span 2; }
.detail-label { font-size: 12px; color: rgba(255,255,255,0.45); font-weight: 500; }
.detail-value { font-size: 14px; color: rgba(255,255,255,0.85); }
.detail-section { margin-top: 20px; display: flex; flex-direction: column; gap: 8px; }
.detail-output { padding: 12px; background: rgba(0,0,0,0.3); border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; font-family: 'JetBrains Mono', monospace; font-size: 12px; line-height: 1.6; color: rgba(255,255,255,0.75); white-space: pre-wrap; word-break: break-all; max-height: 200px; overflow-y: auto; margin: 0; }
.error-output { border-color: rgba(248,113,113,0.3); color: #F87171; }
.spinner { display: inline-block; width: 14px; height: 14px; border: 2px solid rgba(255,255,255,0.3); border-top-color: #fff; border-radius: 50%; animation: spin 0.6s linear infinite; flex-shrink: 0; }
@keyframes spin { to { transform: rotate(360deg); } }
</style>
