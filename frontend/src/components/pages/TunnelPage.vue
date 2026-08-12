<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { TunnelService, ConnService } from '../../../bindings/opsflash/server'
import { Tunnel, Connection } from '../../../bindings/opsflash/server/models'
import TunnelCard from './tunnels/TunnelCard.vue'
import TunnelFormModal from './tunnels/TunnelFormModal.vue'
import TunnelLogModal from './tunnels/TunnelLogModal.vue'

// ==================== 数据状态 ====================
const props = defineProps<{ token: string; initialConnectionId?: number }>()

const tunnels = ref<Tunnel[]>([])
const sshConnections = ref<Connection[]>([])
const loading = ref(false)
const error = ref('')

// 按连接筛选（从连接管理页跳转时携带）
const filterConnectionId = ref(props.initialConnectionId || 0)
const filterConnName = computed(() => {
  const c = sshConnections.value.find(c => c.id === filterConnectionId.value)
  return c ? c.name : ''
})
watch(() => props.initialConnectionId, (v) => {
  filterConnectionId.value = v || 0
  loadTunnels()
})
function clearFilter() {
  filterConnectionId.value = 0
  loadTunnels()
}

// ==================== 分组视图 ====================
interface TunnelGroup {
  key: string    // 真实 groupName（空字符串 = 未分组）
  label: string  // 显示名
  items: Tunnel[]
}

const tunnelGroups = computed<TunnelGroup[]>(() => {
  const map = new Map<string, TunnelGroup>()
  for (const t of tunnels.value) {
    const key = t.groupName || ''
    const label = t.groupName || '未分组'
    if (!map.has(key)) map.set(key, { key, label, items: [] })
    map.get(key)!.items.push(t)
  }
  // 未分组排最后，其余按名称排序
  return [...map.values()].sort((a, b) => {
    if (a.key === '') return 1
    if (b.key === '') return -1
    return a.label.localeCompare(b.label, 'zh')
  })
})

function groupStats(g: TunnelGroup): string {
  const running = g.items.filter(t => t.running).length
  return `${running}/${g.items.length} 运行中`
}

// 分组收起/展开（localStorage 持久化，刷新保持）
const collapsedGroups = ref<Record<string, boolean>>({})
{
  const saved = localStorage.getItem('tunnel-collapsed-groups')
  if (saved) {
    try { collapsedGroups.value = JSON.parse(saved) } catch { /* 忽略损坏数据 */ }
  }
}

function isGroupCollapsed(key: string): boolean {
  return !!collapsedGroups.value[key || 'ungrouped']
}

function toggleGroupCollapse(key: string) {
  const k = key || 'ungrouped'
  collapsedGroups.value = { ...collapsedGroups.value, [k]: !collapsedGroups.value[k] }
  localStorage.setItem('tunnel-collapsed-groups', JSON.stringify(collapsedGroups.value))
}

// 组级启停（groupLoading 用 null 表示"未在操作"——未分组组 key 是 ''，不能与初始值冲突）
const groupLoading = ref<string | null>(null)
async function startGroup(key: string) {
  if (groupLoading.value !== null) return
  groupLoading.value = key
  try {
    const res = await TunnelService.StartTunnelGroup({ token: props.token, groupName: key })
    toast.value = { success: res.success, message: res.message }
    await loadTunnels()
    setTimeout(() => { toast.value = null }, 4000)
  } catch (e) {
    toast.value = { success: false, message: '组启动失败' }
    console.error(e)
  } finally {
    groupLoading.value = null
  }
}

async function stopGroup(key: string) {
  if (groupLoading.value !== null) return
  groupLoading.value = key
  try {
    const res = await TunnelService.StopTunnelGroup({ token: props.token, groupName: key })
    toast.value = { success: res.success, message: res.message }
    await loadTunnels()
    setTimeout(() => { toast.value = null }, 4000)
  } catch (e) {
    toast.value = { success: false, message: '组停止失败' }
    console.error(e)
  } finally {
    groupLoading.value = null
  }
}

// ==================== 加载 ====================
async function loadTunnels() {
  try {
    const res = await TunnelService.GetTunnels({ token: props.token, connectionId: filterConnectionId.value })
    if (res.success) {
      tunnels.value = res.tunnels || []
    } else {
      error.value = res.message || '获取隧道失败'
    }
  } catch (e) {
    console.error('获取隧道失败', e)
  }
}

async function loadSSHConnections() {
  try {
    const res = await ConnService.GetConnections({ token: props.token, type: 'ssh' })
    if (res.success) {
      sshConnections.value = res.connections || []
    }
  } catch (e) {
    console.error('获取 SSH 连接失败', e)
  }
}

// ==================== 轮询 ====================
let pollTimer: ReturnType<typeof setInterval> | null = null

function startPolling() {
  if (pollTimer) return
  pollTimer = setInterval(async () => {
    const needPoll = tunnels.value.some(t => t.running || t.reconnecting)
    if (!needPoll) return
    await loadTunnels()
  }, 2000)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

// ==================== 单条/全部启停 ====================
const bulkLoading = ref(false)

async function toggleTunnel(t: Tunnel) {
  try {
    if (t.running) {
      const res = await TunnelService.StopTunnel({ token: props.token, id: t.id })
      toast.value = { success: res.success, message: res.message }
    } else {
      const res = await TunnelService.StartTunnel({ token: props.token, id: t.id })
      toast.value = { success: res.success, message: res.message }
    }
    await loadTunnels()
    setTimeout(() => { toast.value = null }, 3000)
  } catch (e) {
    toast.value = { success: false, message: '操作失败' }
    console.error(e)
  }
}

async function startAll() {
  if (bulkLoading.value) return
  bulkLoading.value = true
  try {
    const res = await TunnelService.StartAllTunnels({ token: props.token, id: 0 })
    toast.value = { success: res.success, message: res.message }
    await loadTunnels()
    setTimeout(() => { toast.value = null }, 4000)
  } catch (e) {
    toast.value = { success: false, message: '批量启动失败' }
    console.error(e)
  } finally {
    bulkLoading.value = false
  }
}

async function stopAll() {
  try {
    const res = await TunnelService.StopAllTunnels({ token: props.token, id: 0 })
    toast.value = { success: res.success, message: res.message }
    await loadTunnels()
    setTimeout(() => { toast.value = null }, 3000)
  } catch (e) {
    console.error(e)
  }
}

// ==================== 弹窗（表单/日志/删除） ====================
const toast = ref<{ success: boolean; message: string } | null>(null)

const formModal = ref<{
  show: boolean
  mode: 'single' | 'batch'
  isEdit: boolean
  tunnel: Tunnel | null
}>({ show: false, mode: 'single', isEdit: false, tunnel: null })

function openAddModal() {
  formModal.value = { show: true, mode: 'single', isEdit: false, tunnel: null }
}

function openBatchModal() {
  formModal.value = { show: true, mode: 'batch', isEdit: false, tunnel: null }
}

function openEditModal(t: Tunnel) {
  formModal.value = { show: true, mode: 'single', isEdit: true, tunnel: t }
}

function onFormSaved(message: string) {
  formModal.value = { show: false, mode: 'single', isEdit: false, tunnel: null }
  toast.value = { success: true, message }
  loadTunnels()
  setTimeout(() => { toast.value = null }, 4000)
}

// 日志弹窗
const logTunnel = ref<Tunnel | null>(null)
function openLogModal(t: Tunnel) {
  logTunnel.value = t
}
function closeLogModal() {
  logTunnel.value = null
}

// 删除确认
const showDeleteModal = ref(false)
const deleteTarget = ref<Tunnel | null>(null)
const deleteMsg = ref('')
const deleting = ref(false)

function openDeleteModal(t: Tunnel) {
  deleteTarget.value = t
  deleteMsg.value = ''
  showDeleteModal.value = true
}

async function deleteTunnel() {
  if (!deleteTarget.value || deleting.value) return
  deleting.value = true
  try {
    const res = await TunnelService.DeleteTunnel({ token: props.token, id: deleteTarget.value.id })
    if (res.success) {
      showDeleteModal.value = false
      deleteTarget.value = null
      toast.value = { success: true, message: res.message }
      await loadTunnels()
      setTimeout(() => { toast.value = null }, 3000)
    } else {
      deleteMsg.value = res.message || '删除失败'
    }
  } catch (e) {
    deleteMsg.value = '删除失败'
    console.error(e)
  } finally {
    deleting.value = false
  }
}

// ==================== 导入导出 ====================
async function exportTunnels() {
  try {
    const res = await TunnelService.ExportTunnels({ token: props.token })
    if (!res.success) {
      toast.value = { success: false, message: res.message || '导出失败' }
      setTimeout(() => { toast.value = null }, 3000)
      return
    }
    const blob = new Blob([JSON.stringify(res.tunnels || [], null, 2)], { type: 'application/json' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `tunnels-${new Date().toISOString().slice(0, 10)}.json`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
    toast.value = { success: true, message: res.message }
    setTimeout(() => { toast.value = null }, 3000)
  } catch (e) {
    toast.value = { success: false, message: '导出失败' }
    console.error(e)
  }
}

const importFileInput = ref<HTMLInputElement | null>(null)

async function onImportFile(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  if (!file) return
  try {
    const text = await file.text()
    const data = JSON.parse(text)
    const arr = Array.isArray(data) ? data : (data.tunnels || [])
    if (!Array.isArray(arr) || arr.length === 0) {
      toast.value = { success: false, message: '文件中没有有效的隧道配置' }
    } else {
      const res = await TunnelService.ImportTunnels({ token: props.token, tunnels: arr })
      toast.value = { success: res.success, message: res.message }
      await loadTunnels()
    }
  } catch (err) {
    toast.value = { success: false, message: '导入文件解析失败，请选择导出的 JSON 文件' }
    console.error(err)
  } finally {
    input.value = ''
    setTimeout(() => { toast.value = null }, 4000)
  }
}

// ==================== 统计 ====================
const stats = computed(() => {
  const total = tunnels.value.length
  const running = tunnels.value.filter(t => t.running).length
  return `${running}/${total} 运行中`
})

// ==================== 生命周期 ====================
onMounted(async () => {
  loading.value = true
  await Promise.all([loadTunnels(), loadSSHConnections()])
  loading.value = false
  startPolling()
})

onUnmounted(() => {
  stopPolling()
})
</script>

<template>
  <div class="tunnel-page">
    <!-- ==================== 顶部工具栏 ==================== -->
    <div class="tunnel-toolbar">
      <div class="tunnel-toolbar-left">
        <button class="tunnel-bulk-btn tunnel-bulk-start" :disabled="bulkLoading" @click="startAll">
          <span v-if="bulkLoading" class="tunnel-bulk-spinner"></span>
          <svg v-else viewBox="0 0 24 24" fill="none" width="14" height="14"><polygon points="5 3 19 12 5 21 5 3" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/></svg>
          <span>{{ bulkLoading ? '启动中...' : '全部启动' }}</span>
        </button>
        <button class="tunnel-bulk-btn tunnel-bulk-stop" @click="stopAll">
          <svg viewBox="0 0 24 24" fill="none" width="14" height="14"><rect x="6" y="6" width="12" height="12" rx="1" stroke="currentColor" stroke-width="2"/></svg>
          <span>全部停止</span>
        </button>
      </div>
      <div class="tunnel-toolbar-right">
        <span class="tunnel-stats">{{ stats }}</span>
        <button class="tunnel-bulk-btn" title="导出全部隧道配置（JSON）" @click="exportTunnels">
          <svg viewBox="0 0 24 24" fill="none" width="13" height="13"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M7 10l5 5 5-5M12 15V3" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span>导出</span>
        </button>
        <button class="tunnel-bulk-btn" title="从 JSON 文件导入隧道配置" @click="importFileInput?.click()">
          <svg viewBox="0 0 24 24" fill="none" width="13" height="13"><path d="M21 15v4a2 2 0 01-2 2H5a2 2 0 01-2-2v-4M17 8l-5-5-5 5M12 3v12" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span>导入</span>
        </button>
        <input ref="importFileInput" type="file" accept=".json,application/json" style="display:none" @change="onImportFile" />
        <button class="tunnel-bulk-btn tunnel-bulk-start" title="一个 SSH 连接一键创建多条端口映射" @click="openBatchModal">
          <svg viewBox="0 0 24 24" fill="none" width="13" height="13"><path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          <span>批量映射</span>
        </button>
        <button class="tunnel-add-btn" @click="openAddModal">
          <span class="tunnel-add-plus">+</span>
          <span>新建隧道</span>
        </button>
      </div>
    </div>

    <!-- 操作提示 -->
    <div v-if="toast" class="tunnel-toast" :class="toast.success ? 'tunnel-toast-ok' : 'tunnel-toast-fail'">
      {{ toast.message }}
      <button class="tunnel-toast-close" @click="toast = null">&times;</button>
    </div>

    <!-- 按连接筛选提示 -->
    <div v-if="filterConnectionId > 0" class="tunnel-filter-bar">
      <svg viewBox="0 0 24 24" fill="none" width="12" height="12">
        <path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      <span>当前显示「{{ filterConnName || 'SSH-' + filterConnectionId }}」的隧道</span>
      <button class="tunnel-filter-clear" @click="clearFilter">清除筛选</button>
    </div>

    <!-- ==================== 分组卡片列表 ==================== -->
    <div class="tunnel-groups">
      <div v-for="g in tunnelGroups" :key="g.key || 'ungrouped'" class="tunnel-group">
        <!-- 组头：收起按钮 + 名称 + 统计 + 组级启停 -->
        <div class="tunnel-group-head">
          <button
            class="tunnel-group-collapse"
            :class="{ 'tunnel-group-collapsed': isGroupCollapsed(g.key) }"
            :title="isGroupCollapsed(g.key) ? '展开该分组' : '收起该分组'"
            @click="toggleGroupCollapse(g.key)"
          >
            <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
              <polyline points="6 9 12 15 18 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </button>
          <span class="tunnel-group-name">{{ g.label }}</span>
          <span class="tunnel-group-count">{{ g.items.length }} 条</span>
          <span class="tunnel-group-stats">{{ groupStats(g) }}</span>
          <div class="tunnel-group-actions">
            <button
              class="tunnel-group-btn tunnel-group-start"
              :disabled="groupLoading !== null"
              @click="startGroup(g.key)"
            >{{ groupLoading === g.key ? '启动中...' : '全部启动' }}</button>
            <button
              class="tunnel-group-btn tunnel-group-stop"
              :disabled="groupLoading !== null"
              @click="stopGroup(g.key)"
            >{{ groupLoading === g.key ? '停止中...' : '全部停止' }}</button>
          </div>
        </div>

        <div v-show="!isGroupCollapsed(g.key)" class="tunnel-grid">
          <TunnelCard
            v-for="t in g.items"
            :key="t.id"
            :tunnel="t"
            @toggle="toggleTunnel(t)"
            @edit="openEditModal(t)"
            @delete="openDeleteModal(t)"
            @logs="openLogModal(t)"
          />
        </div>
      </div>

      <!-- 空状态 -->
      <div v-if="tunnels.length === 0 && !loading" class="tunnel-empty">
        <div class="tunnel-empty-icon">
          <svg viewBox="0 0 24 24" fill="none" width="44" height="44">
            <path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <p class="tunnel-empty-text">暂无隧道，点击「新建隧道」或「批量映射」创建 SSH 端口转发</p>
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="tunnel-loading">
        <span class="tunnel-loading-spinner"></span>
        <span>正在加载...</span>
      </div>
    </div>

    <!-- 表单弹窗（单个 + 批量） -->
    <TunnelFormModal
      :token="token"
      :ssh-connections="sshConnections"
      :show="formModal.show"
      :mode="formModal.mode"
      :is-edit="formModal.isEdit"
      :tunnel="formModal.tunnel"
      @close="formModal.show = false"
      @saved="onFormSaved"
    />

    <!-- 日志弹窗 -->
    <TunnelLogModal :token="token" :tunnel="logTunnel" @close="closeLogModal" />

    <!-- ==================== 弹窗：删除确认 ==================== -->
    <div v-if="showDeleteModal" class="modal-overlay" @click.self="showDeleteModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title modal-title-warning">
            <span class="warning-icon">!</span>
            删除隧道
          </h2>
          <button class="modal-close" @click="showDeleteModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">确定删除隧道「{{ deleteTarget?.name }}」吗？</p>
          <div class="confirm-warning-box">
            <span class="warning-icon-small">!</span>
            <span>删除后不可恢复；运行中的隧道将先停止再删除。</span>
          </div>
          <p v-if="deleteMsg" class="form-error">{{ deleteMsg }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" :disabled="deleting" @click="showDeleteModal = false">取消</button>
          <button class="btn-danger" :disabled="deleting" @click="deleteTunnel">
            {{ deleting ? '删除中...' : '确认删除' }}
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./tunnel-page.css"></style>
