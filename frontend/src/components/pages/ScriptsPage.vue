<script setup lang="ts">
import { ref, shallowReactive, computed, nextTick, onMounted, onUnmounted } from 'vue'
import { ScriptsService } from '../../../bindings/opsflash/server'
import { Script, Environment } from '../../../bindings/opsflash/server/models'
import { Terminal } from 'xterm'
import { FitAddon } from '@xterm/addon-fit'
import 'xterm/css/xterm.css'

const props = defineProps<{ token: string }>()

// ==================== 状态 ====================
const scripts = ref<Script[]>([])
const environments = ref<Environment[]>([])
const activeEnvId = ref(0)
const loading = ref(false)
const error = ref('')

// 编辑器
const current = ref<Script | null>(null)
const content = ref('')
const dirty = ref(false)
const editorError = ref('')

// 弹窗
const showCreateModal = ref(false)
const createForm = ref({ name: '', type: 'bat', remark: '', environmentId: 0 })
const createError = ref('')

// 编辑信息弹窗（名称/类型/环境/备注）
const showRenameModal = ref(false)
const renameForm = ref({ name: '', type: 'bat', environmentId: 0, remark: '' })
const renameError = ref('')

// 删除确认
const showDeleteModal = ref(false)

// 执行（多会话：每个脚本运行一个独立会话，可并行 + tab 切换查看）
interface RunSession {
  key: number            // 自增唯一键
  scriptId: number
  name: string
  args: string
  running: boolean
  done: boolean
  output: string
  exitCode: number
  exitError: string
  durationMs: number
  timer: ReturnType<typeof setInterval> | null
  // 每个会话独立 xterm
  term: Terminal | null
  fitAddon: FitAddon | null
  container: HTMLDivElement | null
}
const showRunModal = ref(false)
const runArgs = ref('')
const runs = ref<RunSession[]>([])
const activeRunKey = ref(0) // 当前激活 tab
let runSeq = 0              // 会话 key 自增

// 面板显隐
const showTerminal = ref(false)

// 左侧脚本列表（滚动快捷）
const scriptListEl = ref<HTMLDivElement | null>(null)

// ==================== 计算属性 ====================
const filteredScripts = computed(() => {
  // 0=全部环境；否则仅显示选中环境的脚本
  if (!activeEnvId.value) return scripts.value
  return scripts.value.filter((s) => s.environmentId === activeEnvId.value)
})

function typeLabel(t: string): string {
  return ({ bat: 'BAT', ps1: 'PS1', sh: 'SH' } as Record<string, string>)[t] || t.toUpperCase()
}

function fileLabel(s: Script): string {
  return `${s.name}.${s.type}`
}

// 当前时间格式化（YYYY-MM-DD HH:mm:ss）
function fmtNow(): string {
  const d = new Date()
  const p = (n: number) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// 环境名辅助（供模板使用，找不到返回空）
function envNameById(id: number): string {
  return environments.value.find((e) => e.id === id)?.name || ''
}

// 环境平铺切换（0=全部）
function selectEnv(id: number) {
  if (activeEnvId.value === id) return
  activeEnvId.value = id
  loadScripts()
}

// ==================== 列表 ====================
async function loadScripts() {
  loading.value = true
  try {
    const res = await ScriptsService.ListScripts({ token: props.token, environmentId: activeEnvId.value })
    if (res.success) {
      scripts.value = res.scripts || []
      // 当前选中脚本失效时清空
      if (current.value) {
        const still = scripts.value.find((s) => s.id === current.value!.id)
        if (!still) closeEditor()
      }
    } else {
      error.value = res.message || '加载失败'
    }
  } catch (e) {
    error.value = '加载脚本列表失败'
    console.error(e)
  } finally {
    loading.value = false
  }
}

async function loadEnvironments() {
  try {
    const res = await ScriptsService.GetEnvironments({ token: props.token })
    if (res.success) environments.value = res.environments || []
  } catch (e) {
    console.error('加载环境失败', e)
  }
}

async function selectScript(s: Script) {
  if (dirty.value) {
    if (!confirm('当前脚本有未保存的修改，切换后将丢失。是否继续？')) return
    dirty.value = false
  }
  const res = await ScriptsService.ReadScript({ token: props.token, id: s.id })
  if (res.success) {
    current.value = s
    content.value = res.content || ''
    editorError.value = ''
    dirty.value = false
  } else {
    error.value = res.message || '读取失败'
    await loadScripts()
  }
}

function closeEditor() {
  current.value = null
  content.value = ''
  dirty.value = false
  editorError.value = ''
}

function onContentInput() {
  dirty.value = true
}

// Ctrl/Cmd+S 保存编辑器内容
function onEditorKeydown(e: KeyboardEvent) {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's') {
    e.preventDefault() // 阻止浏览器默认"保存页面"行为
    saveScript()
  }
}

// ==================== 新建 ====================
function openCreateModal() {
  // 当前筛选环境；「全部」视图下默认选首个真实环境（脚本必须归属环境）
  const defEnv = activeEnvId.value || environments.value[0]?.id || 0
  createForm.value = { name: '', type: 'bat', remark: '', environmentId: defEnv }
  createError.value = ''
  showCreateModal.value = true
}

async function createScript() {
  const name = createForm.value.name.trim()
  if (!name) {
    createError.value = '请输入脚本名称'
    return
  }
  const res = await ScriptsService.SaveScript({
    token: props.token,
    id: 0,
    name,
    type: createForm.value.type,
    content: '# 新脚本\n',
    environmentId: createForm.value.environmentId,
    remark: createForm.value.remark.trim(),
  })
  if (res.success) {
    showCreateModal.value = false
    await loadScripts()
    if (res.script) {
      await selectScript(res.script)
      content.value = '# 新脚本\n'
    }
  } else {
    createError.value = res.message || '创建失败'
  }
}

// ==================== 保存 ====================
async function saveScript() {
  if (!current.value) return
  if (!content.value.trim()) {
    editorError.value = '脚本内容不能为空'
    return
  }
  const res = await ScriptsService.SaveScript({
    token: props.token,
    id: current.value.id,
    name: current.value.name,
    type: current.value.type,
    content: content.value,
    environmentId: current.value.environmentId,
    remark: current.value.remark,
  })
  if (res.success) {
    editorError.value = ''
    dirty.value = false
    // 同步更新列表中的 ts/size
    const idx = scripts.value.findIndex((s) => s.id === current.value!.id)
    if (idx >= 0) scripts.value[idx] = { ...scripts.value[idx], ...res.script! }
  } else {
    editorError.value = res.message || '保存失败'
  }
}

// ==================== 编辑脚本信息（名称/类型/环境/备注） ====================
function openRenameModal() {
  if (!current.value) return
  renameForm.value = {
    name: current.value.name,
    type: current.value.type,
    environmentId: current.value.environmentId,
    remark: current.value.remark,
  }
  renameError.value = ''
  showRenameModal.value = true
}

async function renameScript() {
  if (!current.value) return
  const name = renameForm.value.name.trim()
  if (!name) {
    renameError.value = '脚本名称不能为空'
    return
  }
  const typ = renameForm.value.type
  if (!['bat', 'ps1', 'sh'].includes(typ)) {
    renameError.value = '脚本类型仅支持 bat / ps1 / sh'
    return
  }
  const changed =
    name !== current.value.name ||
    typ !== current.value.type ||
    renameForm.value.environmentId !== current.value.environmentId ||
    renameForm.value.remark !== current.value.remark
  if (!changed) {
    showRenameModal.value = false
    return
  }
  const res = await ScriptsService.SaveScript({
    token: props.token,
    id: current.value.id,
    name,
    type: typ,
    content: content.value,
    environmentId: renameForm.value.environmentId,
    remark: renameForm.value.remark.trim(),
  })
  if (res.success) {
    showRenameModal.value = false
    dirty.value = false
    // 更新当前脚本与列表
    if (res.script) {
      current.value = { ...current.value, ...res.script }
      const idx = scripts.value.findIndex((s) => s.id === current.value!.id)
      if (idx >= 0) scripts.value[idx] = { ...scripts.value[idx], ...res.script! }
    }
    await loadScripts()
  } else {
    renameError.value = res.message || '保存失败'
  }
}

// ==================== 删除 ====================
async function deleteScript() {
  if (!current.value) return
  const res = await ScriptsService.DeleteScript({ token: props.token, id: current.value.id })
  if (res.success) {
    showDeleteModal.value = false
    closeEditor()
    await loadScripts()
  } else {
    editorError.value = res.message || '删除失败'
  }
}

// ==================== 执行（xterm 轮询） ====================
function openRunModal() {
  runArgs.value = ''
  showRunModal.value = true
}

async function startRun() {
  if (!current.value) return
  showRunModal.value = false
  await runScript(current.value.id, runArgs.value)
}

// 运行中的脚本 ID 集合（列表项显示状态 + 防重）
const runningScriptIds = computed(() => {
  return new Set(runs.value.filter((r) => r.running).map((r) => r.scriptId))
})

// 当前激活会话
const activeRun = computed(() => runs.value.find((r) => r.key === activeRunKey.value) || null)

// 创建会话并启动脚本
async function runScript(id: number, args: string) {
  // 同脚本防重：前端拦截 + 后端兜底
  if (runningScriptIds.value.has(id)) {
    const exist = runs.value.find((r) => r.scriptId === id && r.running)
    if (exist) {
      // 已存在运行中会话 → 切到该 tab 并提示
      activeRunKey.value = exist.key
      showTerminal.value = true
      writeToSession(exist, `\x1b[33m! 该脚本正在执行中（已有会话 #${exist.key}）\x1b[0m\r\n`)
      return
    }
  }

  // 创建会话（shallowReactive：仅顶层字段响应式，避免深度代理 xterm 实例；
  // 模板 v-for 读 runs.value 元素时若为普通对象会被自动代理——必须用同一响应式对象才能触发视图更新）
  runSeq++
  const session: RunSession = shallowReactive({
    key: runSeq,
    scriptId: id,
    name: current.value?.name || String(id),
    args,
    running: true,
    done: false,
    output: '',
    exitCode: 0,
    exitError: '',
    durationMs: 0,
    timer: null,
    term: null,
    fitAddon: null,
    container: null,
  })
  runs.value.push(session)
  activeRunKey.value = session.key
  showTerminal.value = true
  await nextTick() // 等 tab 容器渲染
  ensureSessionTerminal(session)

  writeToSession(session, `\x1b[1;35m══════════ ${fmtNow()} : 运行 ${session.name}${args ? ' [' + args + ']' : ''} ══════════\x1b[0m\r\n`)
  writeToSession(session, '$ 正在启动脚本...\r\n')

  const res = await ScriptsService.RunScript({ token: props.token, id, args })
  if (!res.success) {
    writeToSession(session, `\x1b[31m${res.message || '启动失败'}\x1b[0m\r\n`)
    session.running = false
    session.done = true
    return
  }
  // 启动轮询
  session.timer = setInterval(() => pollSession(session), 500)
  pollSession(session)
}

// 轮询单个会话输出
async function pollSession(session: RunSession) {
  if (!session.running) return
  try {
    const res = await ScriptsService.GetScriptRunProgress({ token: props.token, id: session.scriptId })
    if (res.success) {
      if (res.output) {
        session.output += res.output
        writeToSession(session, res.output)
      }
      if (res.done) {
        session.done = true
        session.exitCode = res.exitCode || 0
        session.exitError = res.exitError || ''
        session.durationMs = res.durationMs || 0
        finishSession(session)
      }
    } else {
      // 会话不存在（可能已结束被清理）→ 结束轮询
      finishSession(session)
    }
  } catch (e) {
    finishSession(session)
  }
}

// 会话结束：停止轮询 + 输出退出码
function finishSession(session: RunSession) {
  session.running = false
  if (session.timer) {
    clearInterval(session.timer)
    session.timer = null
  }
  if (session.done) {
    const tail = `\r\n\x1b[1;36m===== 脚本执行结束 =====\x1b[0m\r\n`
      + `耗时: ${(session.durationMs / 1000).toFixed(1)}s\r\n`
      + (session.exitCode === 0
        ? `\x1b[32m退出码: 0（成功）\x1b[0m\r\n`
        : `\x1b[31m退出码: ${session.exitCode}（失败）\x1b[0m\r\n`)
      + (session.exitError ? `\x1b[31m${session.exitError}\x1b[0m\r\n` : '')
      + '\r\n'
    writeToSession(session, tail)
  }
}

// 停止当前激活会话
async function stopRun() {
  const session = activeRun.value
  if (!session || !session.running) return
  await ScriptsService.StopScriptRun({ token: props.token, id: session.scriptId })
  writeToSession(session, '\r\n\x1b[33m! 已请求停止脚本\x1b[0m\r\n')
}

// 关闭会话 tab（停止轮询，保留已输出内容；运行中会先停止）
async function closeSession(session: RunSession) {
  if (session.running) {
    await ScriptsService.StopScriptRun({ token: props.token, id: session.scriptId })
    session.running = false
    if (session.timer) {
      clearInterval(session.timer)
      session.timer = null
    }
  }
  if (session.term) {
    session.term.dispose()
    session.term = null
  }
  const idx = runs.value.indexOf(session)
  if (idx >= 0) {
    runs.value.splice(idx, 1)
    // 激活 tab 被关 → 切到最后一个
    if (activeRunKey.value === session.key) {
      activeRunKey.value = runs.value.length > 0 ? runs.value[runs.value.length - 1].key : 0
    }
  }
  if (runs.value.length === 0) {
    showTerminal.value = false
  }
}

// 全部关闭：运行中先停止，全部会话 dispose 并清空
async function closeAllSessions() {
  const all = [...runs.value]
  // 先停止所有运行中的会话
  for (const s of all) {
    if (s.running) {
      await ScriptsService.StopScriptRun({ token: props.token, id: s.scriptId })
      if (s.timer) {
        clearInterval(s.timer)
        s.timer = null
      }
      s.running = false
    }
    if (s.term) {
      s.term.dispose()
      s.term = null
    }
  }
  runs.value = []
  activeRunKey.value = 0
  showTerminal.value = false
}

// ==================== xterm（每会话独立实例） ====================
function ensureSessionTerminal(session: RunSession) {
  if (session.term) {
    session.fitAddon?.fit()
    return
  }
  if (!session.container) return
  const term = new Terminal({
    fontSize: 13,
    fontFamily: 'JetBrains Mono, Consolas, monospace',
    theme: { background: '#0b0e14', foreground: '#d4d7de' },
    cursorBlink: false,
    convertEol: true,
  })
  const fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  term.open(session.container)
  fitAddon.fit()
  session.term = term
  session.fitAddon = fitAddon
}

function writeToSession(session: RunSession, s: string) {
  session.term?.write(s)
  session.term?.scrollToBottom()
}

// 清空当前激活会话输出
function clearTerminal() {
  const session = activeRun.value
  if (!session) return
  session.term?.clear()
  session.output = ''
}

// 收起输出面板（v-show 隐藏，保留所有会话 DOM）
function collapseTerminal() {
  showTerminal.value = false
}

// 展开输出面板（重新显示后 fit 当前激活会话）
async function expandTerminal() {
  showTerminal.value = true
  await nextTick()
  activeRun.value?.fitAddon?.fit()
}

// 是否有可展开的历史输出（收起后工具栏显示"展开输出"入口）
const hasTerminalHistory = computed(() => showTerminal.value === false && runs.value.length > 0)

// 会话滚动快捷
function scrollActiveTop() {
  activeRun.value?.term?.scrollToTop()
}
function scrollActiveBottom() {
  activeRun.value?.term?.scrollToBottom()
}

// 脚本列表滚动快捷
function scrollListTop() {
  scriptListEl.value?.scrollTo({ top: 0, behavior: 'smooth' })
}
function scrollListBottom() {
  const el = scriptListEl.value
  if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
}

// 窗口 resize：fit 所有会话
function fitAllTerminals() {
  for (const r of runs.value) r.fitAddon?.fit()
}

// ==================== 生命周期 ====================
onMounted(() => {
  loadScripts()
  loadEnvironments()
  window.addEventListener('resize', fitAllTerminals)
})

onUnmounted(() => {
  // 停止所有会话轮询 + 释放 xterm
  for (const r of runs.value) {
    if (r.timer) clearInterval(r.timer)
    if (r.term) r.term.dispose()
  }
  runs.value = []
  window.removeEventListener('resize', fitAllTerminals)
})

function confirmDiscard(): boolean {
  if (!dirty.value) return true
  return confirm('当前脚本有未保存的修改，确定放弃吗？')
}
</script>

<template>
  <div class="scripts-page">
    <!-- ==================== 工具栏 ==================== -->
    <div class="scripts-toolbar">
      <div class="scripts-left">
        <!-- 环境筛选（环境的增删改在「环境管理」菜单） -->
        <div
          class="scripts-env-tag"
          :class="{ 'scripts-env-tag-active': activeEnvId === 0 }"
          @click="selectEnv(0)"
        >
          <span class="scripts-env-dot" :class="{ 'scripts-env-dot-active': activeEnvId === 0 }"></span>
          <span class="scripts-env-name">全部</span>
        </div>
        <div
          v-for="env in environments"
          :key="env.id"
          class="scripts-env-tag"
          :class="{ 'scripts-env-tag-active': env.id === activeEnvId }"
          @click="selectEnv(env.id)"
        >
          <span class="scripts-env-dot" :class="{ 'scripts-env-dot-active': env.id === activeEnvId }"></span>
          <span class="scripts-env-name">{{ env.name }}</span>
        </div>
      </div>
      <div class="scripts-right">
        <span class="scripts-count">共 {{ filteredScripts.length }} 个脚本</span>
        <button v-if="hasTerminalHistory" class="btn-expand-terminal" title="展开脚本输出面板" @click="expandTerminal">
          <span>🖥 展开输出</span>
        </button>
        <button class="btn-add" @click="openCreateModal">
          <span class="add-plus">+</span>
          <span>新建脚本</span>
        </button>
      </div>
    </div>

    <p v-if="error" class="scripts-error">{{ error }}</p>

    <!-- ==================== 主体：左列表 + 右编辑 ==================== -->
    <div class="scripts-main">
      <!-- 左：脚本列表 -->
      <div class="scripts-list-pane">
        <div class="scripts-list-head">
          <span>脚本列表</span>
          <div class="scripts-list-ops">
            <button class="btn-refresh" title="滚动到顶部" @click="scrollListTop">⤒</button>
            <button class="btn-refresh" title="滚动到底部" @click="scrollListBottom">⤓</button>
            <button class="btn-refresh" title="刷新" @click="loadScripts">⟳</button>
          </div>
        </div>
        <div ref="scriptListEl" class="scripts-list">
          <div
            v-for="s in filteredScripts"
            :key="s.id"
            class="script-item"
            :class="{ active: current && current.id === s.id }"
            @click="selectScript(s)"
          >
            <div class="script-item-main">
              <span class="script-item-name">{{ s.name }}</span>
              <span v-if="runningScriptIds.has(s.id)" class="script-item-running">● 运行中</span>
              <span class="script-item-type" :class="`type-${s.type}`">{{ typeLabel(s.type) }}</span>
              <span v-if="activeEnvId === 0 && envNameById(s.environmentId)" class="script-item-env">{{ envNameById(s.environmentId) }}</span>
            </div>
            <p v-if="s.remark" class="script-item-remark" :title="s.remark">{{ s.remark }}</p>
          </div>
          <div v-if="!loading && filteredScripts.length === 0" class="scripts-empty">
            <p>暂无脚本</p>
            <p class="scripts-empty-hint">点击「新建脚本」或直接放入 data/scripts/{环境 key}/ 目录</p>
          </div>
        </div>
      </div>

      <!-- 右：编辑器 -->
      <div class="scripts-edit-pane">
        <template v-if="current">
          <div class="editor-head">
            <span class="editor-title" :title="current.name">{{ fileLabel(current) }}</span>
            <button class="editor-rename-btn" title="编辑脚本信息" @click="openRenameModal">✎</button>
            <span v-if="dirty" class="editor-dirty">● 未保存</span>
            <div class="editor-actions">
              <button v-if="runningScriptIds.has(current.id)" class="btn-secondary" disabled title="该脚本正在执行中">运行中…</button>
              <button v-else class="btn-secondary" @click="openRunModal">运行</button>
              <button class="btn-primary" :disabled="!dirty" @click="saveScript">保存</button>
              <button class="btn-danger" @click="showDeleteModal = true">删除</button>
            </div>
          </div>
          <div class="editor-meta">
            <span class="editor-meta-item">
              环境：<span class="editor-meta-val">{{ envNameById(current.environmentId) }}</span>
            </span>
            <span v-if="current.remark" class="editor-meta-item">
              备注：<span class="editor-meta-val">{{ current.remark }}</span>
            </span>
            <span class="editor-meta-spacer"></span>
            <span class="editor-meta-item">修改于 {{ new Date(current.ts * 1000).toLocaleString('zh-CN', { hour12: false }) }}</span>
          </div>
          <p v-if="editorError" class="editor-error">{{ editorError }}</p>
          <textarea
            v-model="content"
            class="editor-textarea"
            spellcheck="false"
            :placeholder="`# ${fileLabel(current)} 脚本内容`"
            @input="onContentInput"
            @keydown="onEditorKeydown"
          ></textarea>
          <div class="editor-foot">
            <span>工作目录：脚本所在目录 data/scripts/{环境 key}/</span>
            <span>退出码：bat 用 call+exit /b，ps1 用 exit $LASTEXITCODE</span>
          </div>
        </template>
        <div v-else class="editor-empty">
          <p>从左侧选择一个脚本进行编辑</p>
        </div>
      </div>
    </div>

    <!-- ==================== 执行终端面板（多会话 tab，v-show 保留 DOM） ==================== -->
    <div v-show="showTerminal && runs.length > 0" class="run-panel">
      <div class="run-panel-head">
        <span class="run-panel-title">脚本输出</span>
        <!-- tab 栏 -->
        <div class="run-tabs">
          <div
            v-for="r in runs"
            :key="r.key"
            class="run-tab"
            :class="{ 'run-tab-active': r.key === activeRunKey }"
            @click="activeRunKey = r.key"
          >
            <span class="run-tab-dot" :class="r.running ? 'run-tab-dot-running' : r.exitCode === 0 ? 'run-tab-dot-ok' : 'run-tab-dot-fail'"></span>
            <span class="run-tab-name" :title="r.name">{{ r.name }}<template v-if="r.args"> ({{ r.args }})</template></span>
            <button class="run-tab-close" title="关闭会话" @click.stop="closeSession(r)">×</button>
          </div>
          <button class="run-tab-close-all" title="全部关闭" @click="closeAllSessions">✕ 全部关闭</button>
        </div>
        <span v-if="activeRun?.running" class="run-panel-status running">● 执行中</span>
        <span v-else-if="activeRun?.done" class="run-panel-status" :class="activeRun.exitCode === 0 ? 'ok' : 'fail'">
          {{ activeRun.exitCode === 0 ? '✓ 成功' : `✗ 失败 (${activeRun.exitCode})` }} · {{ ((activeRun.durationMs || 0) / 1000).toFixed(1) }}s
        </span>
        <div class="run-panel-actions">
          <button v-if="activeRun?.running" class="btn-secondary btn-sm" @click="stopRun">停止</button>
          <button class="btn-secondary btn-sm" title="滚动到顶部" @click="scrollActiveTop">⤒ 顶部</button>
          <button class="btn-secondary btn-sm" title="滚动到底部" @click="scrollActiveBottom">⤓ 底部</button>
          <button class="btn-secondary btn-sm" title="清空输出" @click="clearTerminal">清空</button>
          <button class="btn-secondary btn-sm" @click="collapseTerminal">收起</button>
        </div>
      </div>
      <!-- 各会话独立终端容器 -->
      <div class="run-terminal-wrap">
        <div
          v-for="r in runs"
          :key="r.key"
          v-show="r.key === activeRunKey"
          :ref="(el: any) => { if (el) r.container = el }"
          class="run-terminal"
        ></div>
      </div>
    </div>

    <!-- ==================== 弹窗：新建脚本 ==================== -->
    <div v-if="showCreateModal" class="modal-overlay" @click.self="showCreateModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title">新建脚本</h2>
          <button class="modal-close" @click="showCreateModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">脚本名称 <span class="required">*</span></label>
            <input v-model="createForm.name" type="text" class="form-input" placeholder="如：deploy" />
          </div>
          <div class="form-group">
            <label class="form-label">所属环境 <span class="required">*</span></label>
            <div class="env-selector">
              <label
                v-for="env in environments"
                :key="env.id"
                class="env-option"
                :class="{ 'env-option-active': createForm.environmentId === env.id }"
              >
                <input v-model.number="createForm.environmentId" type="radio" :value="env.id" class="env-radio" />
                <span>{{ env.name }}</span>
              </label>
            </div>
          </div>
          <div class="form-group">
            <label class="form-label">类型 <span class="required">*</span></label>
            <div class="type-selector">
              <label
                v-for="t in ['bat', 'ps1', 'sh']"
                :key="t"
                class="type-option"
                :class="{ 'type-option-active': createForm.type === t }"
              >
                <input v-model="createForm.type" type="radio" :value="t" class="type-radio" />
                <span>{{ t.toUpperCase() }}</span>
              </label>
            </div>
          </div>
          <div class="form-group">
            <label class="form-label">备注</label>
            <input v-model="createForm.remark" type="text" class="form-input" placeholder="用途说明" />
          </div>
          <p v-if="createError" class="form-error">{{ createError }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showCreateModal = false">取消</button>
          <button class="btn-primary" @click="createScript">创建</button>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：运行参数 ==================== -->
    <div v-if="showRunModal" class="modal-overlay" @click.self="showRunModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title">运行 {{ current ? fileLabel(current) : '' }}</h2>
          <button class="modal-close" @click="showRunModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">参数（可选）</label>
            <input v-model="runArgs" type="text" class="form-input" placeholder="如：prod --verbose" />
            <p class="form-hint">原样追加到脚本命令尾部，由脚本自身解析</p>
          </div>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showRunModal = false">取消</button>
          <button class="btn-primary" @click="startRun">开始运行</button>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：编辑脚本信息 ==================== -->
    <div v-if="showRenameModal" class="modal-overlay" @click.self="showRenameModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title">编辑脚本信息</h2>
          <button class="modal-close" @click="showRenameModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">脚本名称 <span class="required">*</span></label>
            <input v-model="renameForm.name" type="text" class="form-input" placeholder="如：deploy" @keyup.enter="renameScript" />
          </div>
          <div class="form-group">
            <label class="form-label">所属环境 <span class="required">*</span></label>
            <div class="env-selector">
              <label
                v-for="env in environments"
                :key="env.id"
                class="env-option"
                :class="{ 'env-option-active': renameForm.environmentId === env.id }"
              >
                <input v-model.number="renameForm.environmentId" type="radio" :value="env.id" class="env-radio" />
                <span>{{ env.name }}</span>
              </label>
            </div>
          </div>
          <div class="form-group">
            <label class="form-label">类型 <span class="required">*</span></label>
            <div class="type-selector">
              <label
                v-for="t in ['bat', 'ps1', 'sh']"
                :key="t"
                class="type-option"
                :class="{ 'type-option-active': renameForm.type === t }"
              >
                <input v-model="renameForm.type" type="radio" :value="t" class="type-radio" />
                <span>{{ t.toUpperCase() }}</span>
              </label>
            </div>
            <p class="form-hint">修改类型会改变文件名扩展名</p>
          </div>
          <div class="form-group">
            <label class="form-label">备注</label>
            <input v-model="renameForm.remark" type="text" class="form-input" placeholder="用途说明" />
          </div>
          <p v-if="renameError" class="form-error">{{ renameError }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showRenameModal = false">取消</button>
          <button class="btn-primary" @click="renameScript">保存</button>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：删除确认 ==================== -->
    <div v-if="showDeleteModal" class="modal-overlay" @click.self="showDeleteModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title modal-title-warning">
            <span class="warning-icon">!</span>
            删除脚本
          </h2>
          <button class="modal-close" @click="showDeleteModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">确定删除脚本「{{ current ? fileLabel(current) : '' }}」吗？文件将从 data/scripts/{环境 key}/ 中移除。</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showDeleteModal = false">取消</button>
          <button class="btn-danger" @click="deleteScript">确认删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./scripts-page.css"></style>
