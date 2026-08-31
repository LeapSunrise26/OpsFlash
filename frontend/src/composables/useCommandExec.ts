import { ref, computed } from 'vue'
import type { Ref } from 'vue'
import { OpsService } from '../../bindings/opsflash/server'
import { Command } from '../../bindings/opsflash/server/models'
import type { TerminalActions, TerminalLine } from './useTerminal'

// ==================== 命令执行逻辑（流式/同步/守护/交互 + 轮询） ====================
// 依赖注入：token（鉴权）、commands（列表引用）、terminal（appendTerminal/addLog），
// 供 OpsPage 编排层使用，避免 300+ 行执行逻辑堆在页面组件里。

export function useCommandExec(
  token: string,
  commands: Ref<Command[]>,
  activeEnvId: Ref<number>,
  terminal: Pick<TerminalActions, 'appendTerminal' | 'addLog'>,
) {
  // 命令显示文本：多行脚本每行去掉前导缩进（避免终端里回显/显示错位）
  function cmdDisplay(cmd: Command): string {
    return '$ ' + (cmd.command || '').split('\n').map((l) => l.trimStart()).join('\n')
  }
  // 正在运行的非交互式命令 ID（ssh/数据库模式一次性执行）
  const runningCmdId = ref<number>(0)
  // 流式执行中的命令 ID（本地 terminal 模式：实时输出 + 可停止）
  const streamCmdId = ref<number>(0)
  // 正在启停的进程命令 ID（守护进程/交互式，用于按钮 loading）
  const pendingProcessId = ref<number>(0)
  // 交互式命令状态
  const interactiveCmdId = ref<number>(0)
  const interactiveInput = ref('')
  const interactiveDone = ref(false)

  // 轮询定时器（daemon 状态轮询由 OpsPage 调用 pollProcessStatus 驱动）
  let pollTimer: ReturnType<typeof setInterval> | null = null

  const { appendTerminal, addLog } = terminal

  // 终端面板是否可输入（交互式命令运行中）
  const isTerminalInteractive = computed(
    () => interactiveCmdId.value > 0 && !interactiveDone.value,
  )
  // 是否有进程（守护/交互）在运行
  const hasRunningProcesses = computed(() => commands.value.some((c) => c.running))

  // ==================== 非交互式命令执行 ====================

  async function runCommand(cmd: Command) {
    // 本地 terminal / SSH 模式：流式执行（实时输出 + 可停止）
    // 数据库模式：保持原一次性执行（查询同步返回，渲染结构化表格）
    if (cmd.mode === 'terminal' || cmd.mode === 'ssh') {
      await startStream(cmd)
      return
    }
    await runCommandSync(cmd)
  }

  // 流式执行：管道捕获实时输出，xterm 事件流展示（pty:output），运行中可停止（无 60s 超时）
  async function startStream(cmd: Command) {
    streamCmdId.value = cmd.id
    appendTerminal({ type: 'cmd', text: cmdDisplay(cmd) })

    try {
      const res = await OpsService.StartStream({
        token,
        id: cmd.id,
      })
      if (res.success) {
        // 输出由 pty:output 事件流写入 xterm（TerminalPanel 处理）
      } else {
        streamCmdId.value = 0
        appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '运行', false, res.message)
      }
    } catch (e) {
      streamCmdId.value = 0
      appendTerminal({ type: 'error', text: '启动执行异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '运行', false, '启动执行异常')
      console.error(e)
    }
  }

  // 停止流式执行
  async function stopStream(cmd: Command) {
    appendTerminal({ type: 'info', text: `[停止执行] ${cmd.name}` })
    streamCmdId.value = 0
    try {
      const res = await OpsService.StopStream({
        token,
        id: cmd.id,
      })
      if (res.success) {
        appendTerminal({ type: 'success', text: `✓ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '停止', true, res.message)
      } else {
        appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '停止', false, res.message)
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '停止执行异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '停止', false, '停止执行异常')
      console.error(e)
    }
  }

  // 同步一次性执行（ssh / 数据库模式）
  async function runCommandSync(cmd: Command) {
    runningCmdId.value = cmd.id
    appendTerminal({ type: 'cmd', text: cmdDisplay(cmd) })

    try {
      const res = await OpsService.RunCommand({
        token,
        id: cmd.id,
      })
      if (res.success) {
        // 数据库类命令：渲染结构化表格
        if (res.resultType === 'table' && res.result) {
          appendTerminal({
            type: 'table',
            text: '',
            columns: res.result.columns || [],
            rows: (res.result.rows || []).filter((r): r is string[] => !!r),
          })
          appendTerminal({ type: 'empty', text: '' })
          addLog(cmd.name, '运行', true, res.message)
          return
        }
        if (res.output) {
          const lines = res.output.split('\n')
          for (const line of lines) {
            appendTerminal({ type: 'success', text: line })
          }
        }
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '运行', true, res.message)
      } else {
        if (res.output) {
          const lines = res.output.split('\n')
          for (const line of lines) {
            appendTerminal({ type: 'error', text: line })
          }
        }
        // 无输出时显示失败原因（如 SSH 连接失败/命令报错），避免终端空白
        if (!res.output && res.message) {
          appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        }
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '运行', false, res.message)
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '命令执行异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '运行', false, '命令执行异常')
      console.error(e)
    } finally {
      runningCmdId.value = 0
    }
  }

  // ==================== 守护进程启停 ====================

  async function startDaemon(cmd: Command) {
    pendingProcessId.value = cmd.id
    appendTerminal({ type: 'info', text: `[启动守护进程] ${cmd.name}` })
    appendTerminal({ type: 'cmd', text: cmdDisplay(cmd) })

    try {
      const res = await OpsService.StartDaemon({
        token,
        id: cmd.id,
      })
      if (res.success) {
        const target = commands.value.find((c) => c.id === cmd.id)
        if (target) target.running = true
        appendTerminal({ type: 'success', text: `✓ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '启动守护', true, res.message)
      } else {
        appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '启动守护', false, res.message)
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '启动守护进程异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '启动守护', false, '启动守护进程异常')
      console.error(e)
    } finally {
      pendingProcessId.value = 0
    }
  }

  async function stopDaemon(cmd: Command) {
    pendingProcessId.value = cmd.id
    appendTerminal({ type: 'info', text: `[停止守护进程] ${cmd.name}` })

    try {
      const res = await OpsService.StopDaemon({
        token,
        id: cmd.id,
      })
      if (res.success) {
        const target = commands.value.find((c) => c.id === cmd.id)
        if (target) target.running = false
        appendTerminal({ type: 'success', text: `✓ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '停止守护', true, res.message)
      } else {
        appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '停止守护', false, res.message)
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '停止守护进程异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '停止守护', false, '停止守护进程异常')
      console.error(e)
    } finally {
      pendingProcessId.value = 0
    }
  }

  // ==================== 交互式命令启停 ====================

  async function startInteractive(cmd: Command) {
    pendingProcessId.value = cmd.id
    appendTerminal({ type: 'info', text: `[启动交互式命令] ${cmd.name}` })
    appendTerminal({ type: 'cmd', text: cmdDisplay(cmd) })

    try {
      const res = await OpsService.StartInteractive({
        token,
        id: cmd.id,
      })
      if (res.success) {
        const target = commands.value.find((c) => c.id === cmd.id)
        if (target) target.running = true
        interactiveCmdId.value = cmd.id
        interactiveDone.value = false
        appendTerminal({ type: 'success', text: `✓ ${res.message}` })
        appendTerminal({ type: 'info', text: '─ ─ ─ 可在终端中输入内容进行交互（xterm 真实终端） ─ ─ ─' })
        addLog(cmd.name, '启动交互', true, res.message)
        // 输出由 pty:output 事件流实时写入 xterm（TerminalPanel 处理）
      } else {
        appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '启动交互', false, res.message)
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '启动交互式命令异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '启动交互', false, '启动交互式命令异常')
      console.error(e)
    } finally {
      pendingProcessId.value = 0
    }
  }

  async function stopInteractive(cmd: Command) {
    pendingProcessId.value = cmd.id
    appendTerminal({ type: 'info', text: `[停止交互式命令] ${cmd.name}` })

    try {
      const res = await OpsService.StopInteractive({
        token,
        id: cmd.id,
      })
      if (res.success) {
        const target = commands.value.find((c) => c.id === cmd.id)
        if (target) target.running = false
        interactiveCmdId.value = 0
        interactiveDone.value = false
        appendTerminal({ type: 'success', text: `✓ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '停止交互', true, res.message)
      } else {
        appendTerminal({ type: 'error', text: `✗ ${res.message}` })
        appendTerminal({ type: 'empty', text: '' })
        addLog(cmd.name, '停止交互', false, res.message)
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '停止交互式命令异常' })
      appendTerminal({ type: 'empty', text: '' })
      addLog(cmd.name, '停止交互', false, '停止交互式命令异常')
      console.error(e)
    } finally {
      pendingProcessId.value = 0
    }
  }

  // 发送终端输入（xterm onData 原始按键流，由 TerminalPanel 收集后传入）
  // interactive 会话 → SendInteractiveInput；流式（多行 shell 会话）→ SendStreamInput
  async function sendInput(input: string) {
    if (!input) return
    const id = interactiveCmdId.value || streamCmdId.value
    if (!id) return

    // 注意：PTY 由终端自身回显（密码提示时关闭回显），xterm 直接透传原始数据
    try {
      const res = interactiveCmdId.value
        ? await OpsService.SendInteractiveInput({ token, id, input })
        : await OpsService.SendStreamInput({ token, id, input })
      if (!res.success) {
        appendTerminal({ type: 'error', text: `✗ 输入发送失败: ${res.message}` })
      }
    } catch (e) {
      appendTerminal({ type: 'error', text: '输入发送异常' })
      console.error(e)
    }
  }

  // ==================== xterm 事件回调（由 TerminalPanel 触发） ====================

  // 会话结束（pty:output done 事件）
  function onSessionDone(exitError: string) {
    const target = commands.value.find(
      (c) => c.id === interactiveCmdId.value || c.id === streamCmdId.value,
    )
    if (interactiveCmdId.value > 0) {
      interactiveCmdId.value = 0
      interactiveDone.value = true
      if (target) target.running = false
      if (exitError) {
        appendTerminal({ type: 'error', text: `进程已退出: ${exitError}` })
        addLog(target?.name || '交互命令', '进程退出', false, exitError)
      } else {
        appendTerminal({ type: 'success', text: '进程已正常退出' })
        addLog(target?.name || '交互命令', '进程退出', true, '正常退出')
      }
    } else if (streamCmdId.value > 0) {
      streamCmdId.value = 0
      if (exitError) {
        appendTerminal({ type: 'error', text: `进程已退出: ${exitError}` })
        addLog(target?.name || '命令', '运行', false, exitError)
      } else {
        appendTerminal({ type: 'success', text: '执行完成' })
        addLog(target?.name || '命令', '运行', true, '执行完成')
      }
    }
    appendTerminal({ type: 'empty', text: '' })
  }

  // 终端尺寸变化 → 同步到后端 PTY
  async function onTerminalResize(cols: number, rows: number) {
    const id = interactiveCmdId.value || streamCmdId.value
    if (!id) return
    try {
      await OpsService.ResizeTerminal({ token, id, cols, rows })
    } catch {
      // 静默失败
    }
  }

  // ==================== 通用操作 ====================

  async function copyCommand(cmd: Command) {
    try {
      await navigator.clipboard.writeText(cmd.command)
      addLog(cmd.name, '复制', true, '命令已复制到剪贴板')
    } catch {
      addLog(cmd.name, '复制', false, '复制失败')
    }
  }

  // ==================== 轮询进程状态 ====================

  async function pollProcessStatus() {
    if (!hasRunningProcesses.value || !activeEnvId.value) return
    try {
      const res = await OpsService.GetCommands({ token, environmentId: activeEnvId.value })
      if (res.success && res.commands) {
        for (const newCmd of res.commands) {
          const local = commands.value.find((c) => c.id === newCmd.id)
          if (local && local.running && !newCmd.running) {
            // 守护进程从运行变为停止
            if (local.type === 'daemon') {
              local.running = false
              appendTerminal({ type: 'info', text: `[守护进程退出] ${local.name}` })
              appendTerminal({ type: 'empty', text: '' })
              addLog(local.name, '进程退出', true, '进程已退出')
            }
          }
        }
      }
    } catch {
      // 静默失败
    }
  }

  // 卸载/退出时停止所有轮询（xterm 事件流无需轮询，仅保留 daemon 状态轮询由外部控制）
  function stopAllPolling() {
    // 无输出轮询需要停止（事件流驱动）
  }

  return {
    runningCmdId,
    streamCmdId,
    pendingProcessId,
    interactiveCmdId,
    interactiveInput,
    interactiveDone,
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
  }
}

export type CommandExecActions = ReturnType<typeof useCommandExec>

// 供 OpsPage 使用（类型推导辅助）
export type { TerminalLine }
