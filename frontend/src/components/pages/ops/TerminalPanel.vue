<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { Terminal } from 'xterm'
import { FitAddon } from '@xterm/addon-fit'
import { Events } from '@wailsio/runtime'
import 'xterm/css/xterm.css' // 必须引入：否则 helper textarea 可见、光标/布局异常、无法输入
import type { TerminalLine, LogEntry } from '../../../composables/useTerminal'
import type { TerminalOutputEvent } from '../../../../bindings/opsflash/server/models'

// ==================== 右栏：xterm 终端输出面板 + 操作日志面板 ====================
// xterm 渲染真实终端（颜色/光标/选中复制）；输出通过 pty:output 事件流实时写入；
// 结构化操作记录（lines）增量写入 xterm 文本；输入由 xterm onData 上抛。
// 用 computed 取 props 的最新值（避免解构后父级替换数组导致数据过期）。

const props = defineProps<{
  lines: TerminalLine[]
  logs: LogEntry[]
  interactive: boolean
  maximized: boolean
  sessionId: number // 当前活动会话（命令）ID，事件流按此过滤；0=无会话
}>()

const emit = defineEmits([
  'data', // xterm 输入数据
  'done', // 会话结束 (exitError: string)
  'resize', // (cols, rows)
  'toggle-maximize',
  'clear-lines',
  'clear-logs',
])

const lines = computed(() => props.lines)
const logs = computed(() => props.logs)
const interactive = computed(() => props.interactive)
const maximized = computed(() => props.maximized)

// ==================== xterm 实例 ====================
const termContainer = ref<HTMLDivElement | null>(null)
let term: Terminal | null = null
let fitAddon: FitAddon | null = null
let offEvent: (() => void) | null = null
let resizeObserver: ResizeObserver | null = null
let lineCount = 0 // 已渲染的结构化行数（增量）

// 行 → xterm 文本（带颜色 ANSI）
function lineToANSI(line: TerminalLine): string {
  switch (line.type) {
    case 'cmd': return `\x1b[1;34m${line.text}\x1b[0m`
    case 'error': return `\x1b[31m${line.text}\x1b[0m`
    case 'success': return `\x1b[32m${line.text}\x1b[0m`
    case 'info': return `\x1b[36m${line.text}\x1b[0m`
    case 'empty': return ''
    case 'table': return formatTable(line)
    default: return line.text || ''
  }
}

// 结构化表格 → 等宽文本表格
function formatTable(line: TerminalLine): string {
  const cols: string[] = line.columns || []
  const rows: string[][] = (line.rows || []).map((r) => (Array.isArray(r) ? r : []))
  const parts = [cols.join('  |  ')]
  for (const row of rows) {
    parts.push(row.join('  |  '))
  }
  return parts.join('\r\n')
}

function writeLines(newLines: TerminalLine[]) {
  for (const l of newLines) {
    const text = lineToANSI(l)
    if (text) {
      term?.write(text)
    }
    term?.write('\r\n')
  }
}

// 结构性行变化：增量写入 xterm（清空时重置）
// deep：appendTerminal 是 push 修改数组内容（引用不变），必须 deep 监听才能触发
watch(
  () => props.lines,
  (newLines) => {
    if (!term) return
    if (newLines.length < lineCount) {
      // 外部清空了终端
      term.reset()
      lineCount = 0
    }
    if (newLines.length > lineCount) {
      writeLines(newLines.slice(lineCount))
      lineCount = newLines.length
    }
  },
  { deep: true },
)

// ==================== pty:output 事件流 ====================
function onPtyOutput(ev: { data: TerminalOutputEvent }) {
  const evt = ev.data
  if (!term || !props.sessionId) return
  if (evt.id !== props.sessionId) return
  if (evt.data) {
    term.write(evt.data)
  }
  if (evt.done) {
    emit('done', evt.exitError || '')
  }
}

// ==================== resize ====================
function fitTerminal() {
  if (!term || !fitAddon) return
  try {
    fitAddon.fit()
    if (props.sessionId) {
      emit('resize', term.cols, term.rows)
    }
  } catch {
    // 容器未就绪时忽略
  }
}

// ==================== 右键菜单（复制选中/全部） ====================
const termMenuVisible = ref(false)
const termMenuX = ref(0)
const termMenuY = ref(0)

function onTerminalContextMenu(e: MouseEvent) {
  const menuW = 96
  const menuH = 40
  termMenuX.value = Math.min(e.clientX, window.innerWidth - menuW - 8)
  termMenuY.value = Math.min(e.clientY, window.innerHeight - menuH - 8)
  termMenuVisible.value = true
}

function closeTermMenu() {
  termMenuVisible.value = false
}

function onDocMouseDown(e: MouseEvent) {
  if (!termMenuVisible.value) return
  const el = document.querySelector('.term-context-menu')
  if (el && !el.contains(e.target as Node)) {
    termMenuVisible.value = false
  }
}

async function copyTerminalText() {
  let text = ''
  if (term && term.hasSelection()) {
    text = term.getSelection()
  } else {
    text = props.lines.map((l) => l.text).filter((t) => t).join('\n')
  }
  if (!text) {
    closeTermMenu()
    return
  }
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const ta = document.createElement('textarea')
    ta.value = text
    ta.style.position = 'fixed'
    ta.style.opacity = '0'
    document.body.appendChild(ta)
    ta.select()
    document.execCommand('copy')
    document.body.removeChild(ta)
  }
  closeTermMenu()
}

// ==================== 生命周期 ====================
onMounted(() => {
  term = new Terminal({
    fontSize: 13,
    fontFamily: "'JetBrains Mono', Consolas, monospace",
    lineHeight: 1.25,
    cursorBlink: true,
    scrollback: 3000,
    convertEol: false,
    theme: {
      background: 'rgba(0, 0, 0, 0.35)',
      foreground: '#d8dee9',
      cursor: '#60A5FA',
      selectionBackground: 'rgba(96, 165, 250, 0.35)',
      black: '#1a1b26',
      brightBlack: '#4c4f69',
      white: '#d8dee9',
      brightWhite: '#f4f6fb',
      red: '#ef4444',
      green: '#34d399',
      yellow: '#fbbf24',
      blue: '#60a5fa',
      magenta: '#c084fc',
      cyan: '#22d3ee',
    },
  })
  fitAddon = new FitAddon()
  term.loadAddon(fitAddon)
  if (termContainer.value) {
    term.open(termContainer.value)
  }

  // 初始历史（结构化记录）
  writeLines(props.lines)
  lineCount = props.lines.length

  fitTerminal()

  // 监听终端输出事件（事件流主通道）
  offEvent = Events.On('pty:output', onPtyOutput)

  // xterm 输入 → 上抛（会话存在时）
  term.onData((data) => {
    if (props.sessionId > 0) {
      emit('data', data)
    }
  })

  // 容器尺寸变化 → fit + 同步后端 PTY
  resizeObserver = new ResizeObserver(() => fitTerminal())
  if (termContainer.value) {
    resizeObserver.observe(termContainer.value)
  }
  window.addEventListener('resize', fitTerminal)

  document.addEventListener('mousedown', onDocMouseDown)
})

onUnmounted(() => {
  if (offEvent) {
    offEvent()
    offEvent = null
  }
  if (resizeObserver) {
    resizeObserver.disconnect()
    resizeObserver = null
  }
  window.removeEventListener('resize', fitTerminal)
  document.removeEventListener('mousedown', onDocMouseDown)
  if (term) {
    term.dispose()
    term = null
  }
})
</script>

<template>
  <!-- 终端输出面板 -->
  <div
    class="terminal-panel"
    :class="{
      'terminal-panel-interactive': interactive,
      'terminal-panel-maximized': maximized,
    }"
  >
    <div class="panel-header">
      <span class="panel-title">
        终端输出
        <span v-if="interactive" class="terminal-interactive-badge">交互模式</span>
      </span>
      <span class="panel-header-actions">
        <button v-if="lines.length > 0" class="panel-clear" @click="emit('clear-lines')">清空</button>
        <button
          class="panel-icon-btn"
          :title="maximized ? '还原 (Esc)' : '最大化'"
          @click="emit('toggle-maximize')"
        >
          <svg v-if="!maximized" viewBox="0 0 24 24" fill="none" width="14" height="14">
            <path
              d="M8 3H5a2 2 0 0 0-2 2v3m18 0V5a2 2 0 0 0-2-2h-3m0 18h3a2 2 0 0 0 2-2v-3M3 16v3a2 2 0 0 0 2 2h3"
              stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"
            />
          </svg>
          <svg v-else viewBox="0 0 24 24" fill="none" width="14" height="14">
            <rect x="9" y="9" width="11" height="11" rx="2" stroke="currentColor" stroke-width="2"/>
            <path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" stroke="currentColor" stroke-width="2"/>
          </svg>
        </button>
      </span>
    </div>
    <div
      ref="termContainer"
      class="terminal-body xterm-container"
      @contextmenu.prevent="onTerminalContextMenu($event)"
    ></div>
    <div v-if="lines.length === 0 && !sessionId" class="terminal-empty-hint">暂无输出，点击命令卡片上的运行/启动按钮执行命令</div>

    <!-- 右键菜单（复制） -->
    <div
      v-if="termMenuVisible"
      class="term-context-menu"
      :style="{ left: termMenuX + 'px', top: termMenuY + 'px' }"
      @mousedown.stop
    >
      <button class="term-menu-item" @click="copyTerminalText">复制</button>
    </div>
  </div>

  <!-- 操作日志面板 -->
  <div class="log-panel">
    <div class="panel-header">
      <span class="panel-title">操作日志</span>
      <button v-if="logs.length > 0" class="panel-clear" @click="emit('clear-logs')">清空</button>
    </div>
    <div class="log-body">
      <div v-if="logs.length === 0" class="log-empty">暂无操作记录</div>
      <div v-else class="log-list">
        <div
          v-for="(entry, idx) in logs"
          :key="idx"
          class="log-entry"
          :class="{ 'log-entry-fail': !entry.success }"
        >
          <span class="log-time">{{ entry.time }}</span>
          <span class="log-name">{{ entry.name }}</span>
          <span class="log-action" :class="entry.success ? 'log-action-ok' : 'log-action-fail'">
            {{ entry.action }}
          </span>
          <span class="log-status" :class="entry.success ? 'log-status-ok' : 'log-status-fail'">
            {{ entry.success ? '\u2713' : '\u2717' }}
          </span>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./terminal-panel.css"></style>
