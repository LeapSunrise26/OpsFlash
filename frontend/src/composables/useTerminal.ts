import { ref, nextTick } from 'vue'

// ==================== 终端输出与操作日志（自包含） ====================

export interface TerminalLine {
  type: 'cmd' | 'output' | 'error' | 'success' | 'info' | 'empty' | 'table'
  text: string
  columns?: string[]
  rows?: string[][]
}

export interface LogEntry {
  time: string
  name: string
  action: string
  success: boolean
  message: string
}

// 终端/日志相关的状态与操作（供 OpsPage 编排层与 TerminalPanel 子组件使用）
// 终端右键菜单的显示/关闭逻辑位于 TerminalPanel 子组件内部（含 Escape 关闭）。

export function useTerminal() {
  const terminalLines = ref<TerminalLine[]>([])
  const logs = ref<LogEntry[]>([])
  const terminalMaximized = ref(false)

  function getNowTime(): string {
    return new Date().toLocaleTimeString('zh-CN', { hour12: false })
  }

  function addLog(name: string, action: string, success: boolean, message: string) {
    logs.value.unshift({ time: getNowTime(), name, action, success, message })
    if (logs.value.length > 50) logs.value.pop()
  }

  function appendTerminal(line: TerminalLine) {
    terminalLines.value.push(line)
    nextTick(() => {
      const el = document.querySelector('.terminal-body')
      if (el) el.scrollTop = el.scrollHeight
    })
  }

  // Escape 键：退出终端最大化（右键菜单的 Escape 关闭由 TerminalPanel 内部处理）
  function handleEscapeKey(e: KeyboardEvent) {
    if (e.key === 'Escape' && terminalMaximized.value) {
      terminalMaximized.value = false
    }
  }

  return {
    terminalLines,
    logs,
    terminalMaximized,
    addLog,
    appendTerminal,
    handleEscapeKey,
  }
}

export type TerminalActions = ReturnType<typeof useTerminal>
