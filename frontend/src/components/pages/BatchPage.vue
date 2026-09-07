<script setup lang="ts">
import { ref, computed, onMounted, onUnmounted } from 'vue'
import { BatchService, OpsService, ScriptsService } from '../../../bindings/opsflash/server'
import {
  BatchTask,
  BatchRunItemProgress,
  Command,
  Environment,
  Script,
  BatchTaskItem,
} from '../../../bindings/opsflash/server/models'

const props = defineProps<{ token: string }>()

// ==================== 数据状态 ====================
const tasks = ref<BatchTask[]>([])
const loading = ref(false)
const error = ref('')

// 命令/脚本/环境数据（步骤选择器用）
const environments = ref<Environment[]>([])
const allCommands = ref<Command[]>([])
const allScripts = ref<Script[]>([])

// 执行状态
const activeRunId = ref(0)
const activeRunItems = ref<BatchRunItemProgress[]>([])
const runDone = ref(false)
const runStopped = ref(false)
const runStartedAt = ref('')
const runFinishedAt = ref('')
const runningTaskId = ref(0)
const stopping = ref(false)

// 弹窗
const showModal = ref(false)
const isEdit = ref(false)
const form = ref<Record<string, any>>({})
const formError = ref('')

// 步骤编辑器状态：命令/脚本混合
interface StepItem {
  kind: 'command' | 'script'
  commandId: number
  scriptId: number
  name: string
  args: string
}
const steps = ref<StepItem[]>([])

// 执行参数弹窗
const showParamsModal = ref(false)
const paramsText = ref('')
const pendingStartTask = ref<BatchTask | null>(null)

const showDeleteModal = ref(false)
const deleteTarget = ref<BatchTask | null>(null)
const deleteMsg = ref('')

let pollTimer: ReturnType<typeof setInterval> | null = null

// ==================== 计算属性 ====================

// 错误策略徽章
function policyLabel(p: string): string {
  return p === 'continue_on_error' ? '忽略错误' : '遇错停止'
}

// 命令按环境分组
const commandsByEnv = computed(() => {
  const map = new Map<number, Command[]>()
  for (const cmd of allCommands.value) {
    if (!map.has(cmd.environmentId)) map.set(cmd.environmentId, [])
    map.get(cmd.environmentId)!.push(cmd)
  }
  return environments.value
    .map((env) => ({ env, cmds: map.get(env.id) || [] }))
    .filter((g) => g.cmds.length > 0)
})

function envName(id: number): string {
  return environments.value.find((e) => e.id === id)?.name || ''
}

function isSelected(cmdId: number): boolean {
  return steps.value.some((s) => s.kind === 'command' && s.commandId === cmdId)
}

function toggleSelect(cmd: Command) {
  if (cmd.type !== 'non-interactive') return
  const idx = steps.value.findIndex((s) => s.kind === 'command' && s.commandId === cmd.id)
  if (idx >= 0) {
    steps.value.splice(idx, 1)
  } else {
    steps.value.push({ kind: 'command', commandId: cmd.id, scriptId: 0, name: cmd.name, args: '' })
  }
}

function toggleScriptSelect(sc: Script) {
  const idx = steps.value.findIndex((s) => s.kind === 'script' && s.scriptId === sc.id)
  if (idx >= 0) {
    steps.value.splice(idx, 1)
  } else {
    steps.value.push({ kind: 'script', commandId: 0, scriptId: sc.id, name: `${sc.name}.${sc.type}`, args: '' })
  }
}

function isScriptSelected(scId: number): boolean {
  return steps.value.some((s) => s.kind === 'script' && s.scriptId === scId)
}

// 已选步骤（有序）
const selectedSteps = computed(() => steps.value)

function moveSelected(from: number, dir: -1 | 1) {
  const to = from + dir
  if (to < 0 || to >= steps.value.length) return
  const arr = steps.value
  ;[arr[from], arr[to]] = [arr[to], arr[from]]
  steps.value = [...arr]
}

function removeSelected(idx: number) {
  steps.value.splice(idx, 1)
}

// 汇总统计
const runSummary = computed(() => {
  const items = activeRunItems.value
  return {
    total: items.length,
    success: items.filter((i) => i.status === 'success').length,
    failed: items.filter((i) => i.status === 'failed').length,
    skipped: items.filter((i) => i.status === 'skipped').length,
    running: items.filter((i) => i.status === 'running').length,
  }
})

// ==================== 加载 ====================
async function loadTasks() {
  loading.value = true
  try {
    const res = await BatchService.GetBatchTasks({ token: props.token })
    if (res.success) {
      tasks.value = res.tasks || []
    } else {
      error.value = res.message || '获取任务失败'
    }
  } catch (e) {
    console.error('获取批量任务失败', e)
    error.value = '获取批量任务失败'
  } finally {
    loading.value = false
  }
}

async function loadCommands() {
  try {
    const [envRes, cmdRes, scriptRes] = await Promise.all([
      OpsService.GetEnvironments({ token: props.token }),
      OpsService.GetCommands({ token: props.token, environmentId: 0 }),
      ScriptsService.ListScripts({ token: props.token, environmentId: 0 }),
    ])
    if (envRes.success) environments.value = envRes.environments || []
    if (cmdRes.success) allCommands.value = cmdRes.commands || []
    if (scriptRes.success) allScripts.value = scriptRes.scripts || []
  } catch (e) {
    console.error('加载命令/脚本数据失败', e)
  }
}

// ==================== 弹窗表单 ====================
function openAddModal() {
  isEdit.value = false
  form.value = { name: '', remark: '', failurePolicy: 'stop_on_error' }
  steps.value = []
  formError.value = ''
  showModal.value = true
}

async function openEditModal(task: BatchTask) {
  isEdit.value = true
  form.value = {
    id: task.id,
    name: task.name,
    remark: task.remark,
    failurePolicy: task.failurePolicy || 'stop_on_error',
  }
  steps.value = []
  formError.value = ''
  try {
    const res = await BatchService.GetBatchTask({ token: props.token, id: task.id })
    if (res.success && res.items) {
      steps.value = res.items.map((it: BatchTaskItem) => ({
        kind: it.kind === 'script' ? 'script' : 'command',
        commandId: it.commandId || 0,
        scriptId: it.scriptId || 0,
        name: it.name || '',
        args: it.args || '',
      }))
    }
  } catch (e) {
    console.error('加载任务详情失败', e)
  }
  showModal.value = true
}

function validateForm(): string {
  if (!form.value.name.trim()) return '任务名称不能为空'
  if (steps.value.length === 0) return '请至少添加一个步骤（命令或脚本）'
  return ''
}

async function saveTask() {
  const msg = validateForm()
  if (msg) {
    formError.value = msg
    return
  }
  // 构造 items：仅传非零 ID（命令或脚本），兼容后端校验
  const items = steps.value.map((s) => ({
    commandId: s.kind === 'command' ? s.commandId : 0,
    scriptId: s.kind === 'script' ? s.scriptId : 0,
    args: s.args || '',
  }))
  const payload = {
    token: props.token,
    name: form.value.name.trim(),
    remark: form.value.remark.trim(),
    failurePolicy: form.value.failurePolicy,
    commandIds: [] as number[],
    items,
  }
  try {
    const res = isEdit.value
      ? await BatchService.UpdateBatchTask({ ...payload, id: form.value.id })
      : await BatchService.CreateBatchTask(payload)
    if (res.success) {
      showModal.value = false
      await loadTasks()
    } else {
      formError.value = res.message || '保存失败'
    }
  } catch (e) {
    formError.value = '保存失败'
    console.error(e)
  }
}

// ==================== 执行控制 ====================
function startBatch(task: BatchTask) {
  // 弹流程参数框（可选，注入脚本步骤 {{var}}）
  pendingStartTask.value = task
  paramsText.value = ''
  showParamsModal.value = true
}

async function confirmStart() {
  if (!pendingStartTask.value) return
  const task = pendingStartTask.value
  pendingStartTask.value = null
  showParamsModal.value = false
  try {
    const res = await BatchService.StartBatchTask({
      token: props.token,
      id: task.id,
      paramsJson: paramsText.value.trim(),
    })
    if (res.success) {
      activeRunId.value = res.runId
      runningTaskId.value = task.id
      runDone.value = false
      runStopped.value = false
      activeRunItems.value = []
      runStartedAt.value = ''
      runFinishedAt.value = ''
      startPolling()
    } else {
      error.value = res.message || '启动失败'
    }
  } catch (e) {
    console.error('启动批量任务失败', e)
    error.value = '启动批量任务失败'
  }
}

async function stopBatch() {
  if (!activeRunId.value || stopping.value) return
  stopping.value = true
  try {
    await BatchService.StopBatchTask({ token: props.token, runId: activeRunId.value })
  } catch (e) {
    console.error('停止批量任务失败', e)
  } finally {
    stopping.value = false
  }
}

function startPolling() {
  stopPolling()
  pollTimer = setInterval(pollProgress, 500)
}

function stopPolling() {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}

async function pollProgress() {
  if (!activeRunId.value) return
  try {
    const res = await BatchService.GetBatchTaskProgress({ token: props.token, runId: activeRunId.value })
    if (res.success) {
      activeRunItems.value = res.items || []
      runDone.value = res.done
      runStopped.value = res.stopped
      runStartedAt.value = res.startedAt
      runFinishedAt.value = res.finishedAt
      if (res.done) {
        stopPolling()
        runningTaskId.value = 0
        // 执行结束后刷新任务列表（下次启动状态复位）
        await loadTasks()
      }
    }
  } catch {
    // 静默失败
  }
}

// ==================== 删除 ====================
function openDeleteModal(task: BatchTask) {
  deleteTarget.value = task
  deleteMsg.value = ''
  showDeleteModal.value = true
}

async function deleteTask() {
  if (!deleteTarget.value) return
  const task = deleteTarget.value
  try {
    const res = await BatchService.DeleteBatchTask({ token: props.token, id: task.id })
    if (res.success) {
      showDeleteModal.value = false
      deleteTarget.value = null
      await loadTasks()
    } else {
      deleteMsg.value = res.message || '删除失败'
    }
  } catch (e) {
    deleteMsg.value = '删除失败'
    console.error(e)
  }
}

// ==================== 生命周期 ====================
onMounted(async () => {
  await loadTasks()
  await loadCommands()
})

onUnmounted(() => {
  stopPolling()
})
</script>

<template>
  <div class="batch-page">
    <div class="batch-main">
      <!-- ============ 左栏：任务列表 ============ -->
      <div class="batch-left">
        <div class="batch-toolbar">
          <span class="batch-stats">{{ tasks.length }} 个任务</span>
          <button class="batch-add-btn" @click="openAddModal">
            <span class="batch-add-plus">+</span>
            <span>新建任务</span>
          </button>
        </div>

        <div class="batch-list">
          <div
            v-for="task in tasks"
            :key="task.id"
            class="batch-card"
            :class="{ 'batch-card-running': runningTaskId === task.id }"
          >
            <div class="batch-card-head">
              <h3 class="batch-card-name">{{ task.name }}</h3>
              <span
                class="batch-policy-badge"
                :class="task.failurePolicy === 'continue_on_error' ? 'batch-policy-continue' : 'batch-policy-stop'"
              >{{ task.failurePolicy === 'continue_on_error' ? '🟢 忽略错误' : '🔴 遇错停止' }}</span>
            </div>
            <div class="batch-card-meta">
              <span class="batch-count">{{ task.commandCount }} 个步骤</span>
              <span v-if="runningTaskId === task.id" class="batch-running-tag">执行中...</span>
            </div>
            <p v-if="task.remark" class="batch-card-remark">{{ task.remark }}</p>
            <div class="batch-card-actions">
              <button
                class="batch-action-btn batch-action-run"
                :disabled="runningTaskId === task.id"
                @click="startBatch(task)"
              >▶ 执行</button>
              <button class="batch-action-btn" @click="openEditModal(task)">编辑</button>
              <button class="batch-action-btn batch-action-danger" @click="openDeleteModal(task)">删除</button>
            </div>
          </div>

          <div v-if="tasks.length === 0 && !loading" class="batch-empty">
            <p>暂无批量任务，点击「新建任务」组合现有命令</p>
          </div>
        </div>
      </div>

      <!-- ============ 右栏：执行结果面板 ============ -->
      <div class="batch-right">
        <div class="batch-result-panel">
          <div class="batch-result-header">
            <span class="batch-result-title">
              执行结果
              <span v-if="activeRunId && !runDone" class="batch-result-live">● 执行中</span>
              <span v-else-if="runStopped" class="batch-result-stopped">已停止</span>
              <span v-else-if="runDone" class="batch-result-done">已完成</span>
            </span>
            <div class="batch-result-actions">
              <span v-if="runStartedAt" class="batch-result-time">{{ runStartedAt }}</span>
              <button
                v-if="activeRunId && !runDone"
                class="batch-action-btn batch-action-stop"
                :disabled="stopping"
                @click="stopBatch"
              >{{ stopping ? '停止中...' : '⏹ 停止' }}</button>
            </div>
          </div>

          <!-- 汇总 -->
          <div v-if="activeRunId" class="batch-summary">
            <span class="batch-summary-item">共 {{ runSummary.total }}</span>
            <span class="batch-summary-item batch-summary-ok">✓ {{ runSummary.success }}</span>
            <span class="batch-summary-item batch-summary-fail">✗ {{ runSummary.failed }}</span>
            <span class="batch-summary-item batch-summary-skip">— {{ runSummary.skipped }}</span>
            <span v-if="runSummary.running" class="batch-summary-item batch-summary-running">⏳ {{ runSummary.running }} 运行中</span>
            <span v-if="runFinishedAt" class="batch-summary-item batch-summary-time">结束 {{ runFinishedAt }}</span>
          </div>

          <!-- 逐条结果 -->
          <div class="batch-result-body">
            <div v-if="!activeRunId" class="batch-result-empty">
              尚未执行任务，点击左侧任务卡片上的「执行」按钮开始
            </div>
            <div
              v-for="(item, idx) in activeRunItems"
              :key="idx"
              class="batch-result-item"
              :class="`batch-result-${item.status}`"
            >
              <span class="batch-result-status">
                {{ item.status === 'success' ? '✓' : item.status === 'failed' ? '✗' : item.status === 'running' ? '⏳' : '—' }}
              </span>
              <span class="batch-result-name">{{ item.name }}</span>
              <span v-if="item.kind === 'script'" class="batch-result-kind">脚本</span>
              <span v-if="item.status === 'failed' && item.exitCode !== 0" class="batch-result-exitcode" :class="item.exitCode === 0 ? 'exit-ok' : 'exit-fail'">
                退出码 {{ item.exitCode }}
              </span>
              <span v-else-if="item.status === 'success' && item.exitCode === 0" class="batch-result-exitcode exit-ok">
                退出码 0
              </span>
              <span v-if="item.durationMs > 0" class="batch-result-duration">{{ item.durationMs }}ms</span>
              <div v-if="item.output || item.error" class="batch-result-detail">
                <pre v-if="item.error" class="batch-result-error">{{ item.error }}</pre>
                <pre v-if="item.output">{{ item.output }}</pre>
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：新建/编辑任务 ==================== -->
    <div v-if="showModal" class="modal-overlay" @click.self="showModal = false">
      <div class="modal-box modal-box-lg">
        <div class="modal-header">
          <h2 class="modal-title">{{ isEdit ? '编辑任务' : '新建任务' }}</h2>
          <button class="modal-close" @click="showModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">任务名称 <span class="required">*</span></label>
            <input v-model="form.name" type="text" class="form-input" placeholder="如：发布上线检查" />
          </div>

          <!-- 错误策略 -->
          <div class="form-group">
            <label class="form-label">错误策略 <span class="required">*</span></label>
            <div class="batch-policy-selector">
              <label class="batch-policy-option" :class="{ 'batch-policy-option-active': form.failurePolicy === 'stop_on_error' }">
                <input v-model="form.failurePolicy" type="radio" value="stop_on_error" class="batch-type-radio" />
                <span>🔴 遇错停止</span>
                <small>某条失败立即中止，后续跳过</small>
              </label>
              <label class="batch-policy-option" :class="{ 'batch-policy-option-active': form.failurePolicy === 'continue_on_error' }">
                <input v-model="form.failurePolicy" type="radio" value="continue_on_error" class="batch-type-radio" />
                <span>🟢 忽略错误</span>
                <small>失败也继续执行至全部完成</small>
              </label>
            </div>
          </div>

          <!-- 命令选择器（按环境分组） -->
          <div class="form-group">
            <label class="form-label">添加命令步骤
              <span class="form-hint-inline">仅非交互式命令可批量执行</span>
            </label>
            <div class="batch-cmd-picker">
              <div v-for="group in commandsByEnv" :key="group.env.id" class="batch-cmd-group">
                <div class="batch-cmd-group-title">
                  <span class="cmd-env-dot"></span>{{ group.env.name }}
                </div>
                <label
                  v-for="cmd in group.cmds"
                  :key="cmd.id"
                  class="batch-cmd-item"
                  :class="{
                    'batch-cmd-item-disabled': cmd.type !== 'non-interactive',
                    'batch-cmd-item-checked': isSelected(cmd.id),
                  }"
                >
                  <input
                    type="checkbox"
                    :checked="isSelected(cmd.id)"
                    :disabled="cmd.type !== 'non-interactive'"
                    @change="toggleSelect(cmd)"
                  />
                  <span class="batch-cmd-item-name">{{ cmd.name }}</span>
                  <span v-if="cmd.type !== 'non-interactive'" class="batch-cmd-item-tip">不支持批量</span>
                </label>
              </div>
            </div>
          </div>

          <!-- 脚本步骤选择器 -->
          <div class="form-group">
            <label class="form-label">添加脚本步骤
              <span class="form-hint-inline">脚本本地执行，参数支持 &lbrace;&lbrace;var&rbrace;&rbrace; 注入</span>
            </label>
            <div v-if="allScripts.length === 0" class="batch-script-empty">
              暂无脚本，请先到「脚本库」创建
            </div>
            <div v-else class="batch-script-picker">
              <label
                v-for="sc in allScripts"
                :key="sc.id"
                class="batch-script-item"
                :class="{ 'batch-script-item-checked': isScriptSelected(sc.id) }"
              >
                <input
                  type="checkbox"
                  :checked="isScriptSelected(sc.id)"
                  @change="toggleScriptSelect(sc)"
                />
                <span class="batch-script-item-name">{{ sc.name }}</span>
                <span class="batch-script-item-type" :class="`type-${sc.type}`">{{ sc.type.toUpperCase() }}</span>
              </label>
            </div>
          </div>

          <!-- 已选步骤（有序，可调顺序 + 脚本参数） -->
          <div v-if="selectedSteps.length > 0" class="form-group">
            <label class="form-label">执行顺序（点击箭头调整，脚本步骤可填参数）</label>
            <div class="batch-selected-list">
              <div v-for="(step, idx) in selectedSteps" :key="idx" class="batch-selected-item">
                <span class="batch-selected-idx">{{ idx + 1 }}</span>
                <span class="batch-selected-kind" :class="step.kind === 'script' ? 'kind-script' : 'kind-command'">
                  {{ step.kind === 'script' ? '脚本' : '命令' }}
                </span>
                <span class="batch-selected-name">{{ step.name }}</span>
                <input
                  v-if="step.kind === 'script'"
                  v-model="step.args"
                  class="batch-selected-args"
                  type="text"
                  placeholder="参数，如 --env &lbrace;&lbrace;version&rbrace;&rbrace;"
                />
                <div class="batch-selected-ops">
                  <button class="batch-selected-btn" :disabled="idx === 0" @click="moveSelected(idx, -1)">↑</button>
                  <button class="batch-selected-btn" :disabled="idx === selectedSteps.length - 1" @click="moveSelected(idx, 1)">↓</button>
                  <button class="batch-selected-btn batch-selected-del" @click="removeSelected(idx)">✕</button>
                </div>
              </div>
            </div>
          </div>

          <div class="form-group">
            <label class="form-label">备注</label>
            <input v-model="form.remark" type="text" class="form-input" placeholder="任务用途说明" />
          </div>
          <p v-if="formError" class="form-error">{{ formError }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showModal = false">取消</button>
          <button class="btn-primary" @click="saveTask">{{ isEdit ? '保存修改' : '创建任务' }}</button>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：执行参数（流程参数注入） ==================== -->
    <div v-if="showParamsModal" class="modal-overlay" @click.self="showParamsModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title">执行 {{ pendingStartTask?.name || '' }}</h2>
          <button class="modal-close" @click="showParamsModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">流程参数（可选，JSON 格式）</label>
            <textarea
              v-model="paramsText"
              class="batch-params-textarea"
              rows="4"
              spellcheck="false"
              placeholder='如：{"version":"1.2.3","host":"10.0.0.1"}'
            ></textarea>
            <p class="form-hint">将替换脚本步骤参数中的 &lbrace;&lbrace;key&rbrace;&rbrace; 占位符；命令步骤不注入</p>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showParamsModal = false">取消</button>
          <button class="btn-primary" @click="confirmStart">开始执行</button>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：删除确认 ==================== -->
    <div v-if="showDeleteModal" class="modal-overlay" @click.self="showDeleteModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title modal-title-warning">
            <span class="warning-icon">!</span>
            删除任务
          </h2>
          <button class="modal-close" @click="showDeleteModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">确定删除任务「{{ deleteTarget?.name }}」吗？</p>
          <div class="confirm-warning-box">
            <span class="warning-icon-small">!</span>
            <span>删除后不可恢复；执行中的任务将自动停止。</span>
          </div>
          <p v-if="deleteMsg" class="form-error">{{ deleteMsg }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showDeleteModal = false">取消</button>
          <button class="btn-danger" @click="deleteTask">确认删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./batch-page.css"></style>
