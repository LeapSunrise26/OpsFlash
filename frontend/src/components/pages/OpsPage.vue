<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { OpsService, ConnService, ScriptsService } from '../../../bindings/opsflash/server'
import { Command, Connection, Environment } from '../../../bindings/opsflash/server/models'
import { useTerminal } from '../../composables/useTerminal'
import { useCommandExec } from '../../composables/useCommandExec'
import CommandCard from './ops/CommandCard.vue'
import TerminalPanel from './ops/TerminalPanel.vue'
import CommandFormModal from './ops/CommandFormModal.vue'

const props = defineProps<{ token: string }>()

// ==================== 终端 / 日志 ====================
const terminal = useTerminal()
const { terminalLines, logs, terminalMaximized, addLog, appendTerminal, handleEscapeKey } = terminal

// ==================== 数据状态 ====================
const environments = ref<Environment[]>([])
const activeEnvId = ref<number>(0)
const commands = ref<Command[]>([])
const loading = ref(false)
const error = ref('')
const connections = ref<Connection[]>([])
const searchText = ref('')

// ==================== 命令执行逻辑（流式/同步/守护/交互/轮询） ====================
const exec = useCommandExec(props.token, commands, activeEnvId, terminal)
const {
  runningCmdId,
  streamCmdId,
  interactiveCmdId,
  pendingProcessId,
  isTerminalInteractive,
  hasRunningProcesses,
  runCommand,
  stopStream,
  startDaemon,
  stopDaemon,
  startInteractive,
  stopInteractive,
  sendInput,
  onSessionDone,
  onTerminalResize,
  copyCommand,
  pollProcessStatus,
  stopAllPolling,
} = exec

// 当前活动终端会话 ID（interactive 或流式会话；xterm 事件流按此过滤）
const terminalSessionId = computed(() => interactiveCmdId.value || streamCmdId.value)

// ==================== 类型映射 ====================
const typeLabels: Record<string, string> = {
  'non-interactive': '非交互',
  'interactive': '交互',
  'daemon': '守护',
}

const modeLabels: Record<string, string> = {
  terminal: '本地',
  ssh: 'SSH',
  redis: 'Redis',
  mysql: 'MySQL',
  tdengine: 'TAOS',
}

const interpreterLabels: Record<string, string> = {
  cmd: 'CMD',
  powershell: 'PowerShell',
  bash: 'Bash',
}

// ==================== 计算属性 ====================
const filteredCommands = computed(() => {
  if (!searchText.value) return commands.value
  const q = searchText.value.toLowerCase()
  return commands.value.filter(
    (c) =>
      c.name.toLowerCase().includes(q) ||
      c.command.toLowerCase().includes(q),
  )
})

const envStats = computed(() => {
  const envCount = environments.value.length
  const cmdCount = commands.value.length
  const runningCount = commands.value.filter((c) => c.running).length
  if (runningCount > 0) {
    return `${envCount} 个环境 · ${cmdCount} 条命令 · ${runningCount} 个运行中`
  }
  return `${envCount} 个环境 · ${cmdCount} 条命令`
})

// ==================== 命令卡片折叠状态 ====================
const expandedCmdIds = ref<Set<number>>(new Set())

function isCmdExpanded(id: number): boolean {
  return expandedCmdIds.value.has(id)
}

function toggleCmd(id: number) {
  const next = new Set(expandedCmdIds.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    next.add(id)
  }
  expandedCmdIds.value = next
}

const allExpanded = computed(
  () =>
    filteredCommands.value.length > 0 &&
    expandedCmdIds.value.size >= filteredCommands.value.length,
)

function toggleAllCmds() {
  if (allExpanded.value) {
    expandedCmdIds.value = new Set()
  } else {
    expandedCmdIds.value = new Set(filteredCommands.value.map((c) => c.id))
  }
}

// ==================== API 加载 ====================
async function loadEnvironments() {
  try {
    const res = await ScriptsService.GetEnvironments({ token: props.token })
    if (res.success) {
      environments.value = res.environments || []
      if (!activeEnvId.value && environments.value.length > 0) {
        activeEnvId.value = environments.value[0].id
      }
    } else {
      error.value = res.message || '获取环境失败'
    }
  } catch (e) {
    console.error('获取环境失败', e)
    error.value = '获取环境失败'
  }
}

async function loadCommands() {
  loading.value = true
  try {
    const res = await OpsService.GetCommands({
      token: props.token,
      environmentId: activeEnvId.value,
    })
    if (res.success) {
      commands.value = res.commands || []
    } else {
      error.value = res.message || '获取命令失败'
    }
  } catch (e) {
    console.error('获取命令失败', e)
    error.value = '获取命令失败'
  } finally {
    loading.value = false
  }
}

async function loadConnections() {
  try {
    const res = await ConnService.GetConnections({ token: props.token, type: '' })
    if (res.success) {
      connections.value = res.connections || []
    }
  } catch (e) {
    console.error('获取连接列表失败', e)
  }
}

async function selectEnv(envId: number) {
  if (envId === activeEnvId.value) return
  activeEnvId.value = envId
  searchText.value = ''
  expandedCmdIds.value = new Set() // 切换环境后默认全部收起
  await loadCommands()
}

// ==================== 环境筛选（环境的新增/编辑/删除在「环境管理」独立菜单） ====================

// ==================== 命令管理（表单弹窗） ====================
const showCmdModal = ref(false)
const cmdModalMode = ref<'create' | 'edit'>('create')
const cmdModalTarget = ref<Command | null>(null)

function openCommandModal() {
  cmdModalMode.value = 'create'
  cmdModalTarget.value = null
  showCmdModal.value = true
}

function openEditModal(cmd: Command) {
  cmdModalMode.value = 'edit'
  cmdModalTarget.value = cmd
  showCmdModal.value = true
}

function closeCmdModal() {
  showCmdModal.value = false
  cmdModalTarget.value = null
}

// 保存成功：刷新列表 + 记录日志
function onCmdSaved(info: { name: string; type: string; action: string; message: string }) {
  addLog(info.name, info.action, true, info.message)
  loadCommands()
}

// 终端输出复制成功
function onTerminalCopied(n: number) {
  addLog('终端输出', '复制', true, `已复制 ${n} 字符`)
}

// ==================== 命令删除 ====================
const showDeleteCmdModal = ref(false)
const deleteCmdTarget = ref<Command | null>(null)

function openDeleteCmdModal(cmd: Command) {
  deleteCmdTarget.value = cmd
  showDeleteCmdModal.value = true
}

async function deleteCommand() {
  if (!deleteCmdTarget.value) return
  const cmd = deleteCmdTarget.value

  try {
    const res = await OpsService.DeleteCommand({
      token: props.token,
      id: cmd.id,
    })
    if (res.success) {
      addLog(cmd.name, '删除命令', true, res.message)
      commands.value = commands.value.filter((c) => c.id !== cmd.id)
      showDeleteCmdModal.value = false
      deleteCmdTarget.value = null
    } else {
      addLog(cmd.name, '删除命令', false, res.message)
    }
  } catch (e) {
    addLog(cmd.name, '删除命令', false, '操作异常')
    console.error(e)
  }
}

// ==================== 生命周期 ====================
let pollTimer: ReturnType<typeof setInterval> | null = null

onMounted(async () => {
  await loadEnvironments()
  if (activeEnvId.value) {
    await loadCommands()
  }
  loadConnections()
  pollTimer = setInterval(pollProcessStatus, 5000)
  // Esc 键退出终端最大化
  window.addEventListener('keydown', handleEscapeKey)
})

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
  stopAllPolling()
  window.removeEventListener('keydown', handleEscapeKey)
})
</script>

<template>
  <div class="ops-page">
    <!-- ==================== 环境管理栏 ==================== -->
    <div class="env-bar">
      <div
        v-for="env in environments"
        :key="env.id"
        class="env-tag"
        :class="{ 'env-tag-active': env.id === activeEnvId }"
        @click="selectEnv(env.id)"
      >
        <span class="env-dot" :class="{ 'env-dot-active': env.id === activeEnvId }"></span>
        <span class="env-tag-name">{{ env.name }}</span>
      </div>

      <div class="env-spacer"></div>
      <span class="env-stats">{{ envStats }}</span>
    </div>

    <!-- ==================== 主内容区 ==================== -->
    <div class="main-wrap">
      <!-- ============ 左栏：命令卡片 ============ -->
      <div class="left-col">
        <!-- 工具栏 -->
        <div class="cmd-toolbar">
          <div class="search-box">
            <svg class="search-icon" viewBox="0 0 24 24" fill="none">
              <circle cx="11" cy="11" r="8" stroke="currentColor" stroke-width="2"/>
              <line x1="21" y1="21" x2="16.65" y2="16.65" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
            </svg>
            <input
              v-model="searchText"
              type="text"
              class="search-input"
              placeholder="搜索名称或内容..."
            />
          </div>
          <button
            class="collapse-btn"
            :title="allExpanded ? '收起' : '展开'"
            @click="toggleAllCmds"
          >
            <svg v-if="!allExpanded" viewBox="0 0 24 24" fill="none" width="13" height="13">
              <polyline points="6 9 12 15 18 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <svg v-else viewBox="0 0 24 24" fill="none" width="13" height="13">
              <polyline points="6 15 12 9 18 15" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            <span>{{ allExpanded ? '收起' : '展开' }}</span>
          </button>
          <button class="add-cmd-btn" @click="openCommandModal">
            <span class="add-cmd-plus">+</span>
            <span>添加</span>
          </button>
        </div>

        <!-- 卡片网格 -->
        <div class="card-grid">
          <CommandCard
            v-for="cmd in filteredCommands"
            :key="cmd.id"
            :cmd="cmd"
            :expanded="isCmdExpanded(cmd.id)"
            :stream-cmd-id="streamCmdId"
            :running-cmd-id="runningCmdId"
            :pending-process-id="pendingProcessId"
            :active-env-id="activeEnvId"
            :type-labels="typeLabels"
            :mode-labels="modeLabels"
            :interpreter-labels="interpreterLabels"
            @toggle="toggleCmd"
            @run="runCommand"
            @stop-stream="stopStream"
            @start-daemon="startDaemon"
            @stop-daemon="stopDaemon"
            @start-interactive="startInteractive"
            @stop-interactive="stopInteractive"
            @copy="copyCommand"
            @edit="openEditModal"
            @delete="openDeleteCmdModal"
          />

          <!-- 空状态 -->
          <div v-if="filteredCommands.length === 0 && !loading" class="empty-state">
            <div class="empty-icon">
              <svg viewBox="0 0 24 24" fill="none" width="48" height="48">
                <rect x="3" y="3" width="18" height="18" rx="2" stroke="currentColor" stroke-width="1.5"/>
                <line x1="9" y1="9" x2="15" y2="9" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
                <line x1="9" y1="13" x2="15" y2="13" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
                <line x1="9" y1="17" x2="13" y2="17" stroke="currentColor" stroke-width="1.5" stroke-linecap="round"/>
              </svg>
            </div>
            <p class="empty-text">{{ searchText ? '没有找到匹配的命令' : '当前环境暂无命令，点击「添加命令」开始' }}</p>
          </div>

          <!-- 加载中 -->
          <div v-if="loading" class="loading-state">
            <span class="loading-spinner"></span>
            <span>正在加载...</span>
          </div>
        </div>
      </div>

      <!-- ============ 右栏：终端 + 日志 ============ -->
      <div class="right-col">
        <TerminalPanel
          :lines="terminalLines"
          :logs="logs"
          :interactive="isTerminalInteractive"
          :maximized="terminalMaximized"
          :session-id="terminalSessionId"
          @data="sendInput"
          @done="onSessionDone"
          @resize="onTerminalResize"
          @toggle-maximize="terminalMaximized = !terminalMaximized"
          @clear-lines="terminalLines = []"
          @clear-logs="logs = []"
        />
      </div>
    </div>

    <!-- ==================== 弹窗：添加 / 编辑命令 ==================== -->
    <CommandFormModal
      :token="props.token"
      :show="showCmdModal"
      :mode="cmdModalMode"
      :edit-target="cmdModalTarget"
      :environments="environments"
      :connections="connections"
      :active-env-id="activeEnvId"
      @close="closeCmdModal"
      @saved="onCmdSaved"
    />

    <!-- ==================== 弹窗：删除命令确认 ==================== -->
    <div v-if="showDeleteCmdModal" class="modal-overlay" @click.self="showDeleteCmdModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title modal-title-warning">
            <span class="warning-icon">!</span>
            删除命令
          </h2>
          <button class="modal-close" @click="showDeleteCmdModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">确定删除命令「{{ deleteCmdTarget?.name }}」吗？</p>
          <div class="confirm-warning-box" v-if="deleteCmdTarget && deleteCmdTarget.running">
            <span class="warning-icon-small">!</span>
            <span>该命令正在运行中，删除时将自动停止。</span>
          </div>
          <p class="confirm-hint" v-else>删除后不可恢复</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showDeleteCmdModal = false">取消</button>
          <button class="btn-danger" @click="deleteCommand">确认删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./ops-page.css"></style>
