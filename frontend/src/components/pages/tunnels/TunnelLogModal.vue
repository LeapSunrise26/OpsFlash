<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { TunnelService } from '../../../../bindings/opsflash/server'
import { Tunnel } from '../../../../bindings/opsflash/server/models'

// 隧道日志弹窗：props.tunnel 非空时显示并 3s 轮询，置空时停止
const props = defineProps<{ token: string; tunnel: Tunnel | null }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const logs = ref<{ time: string; level: string; message: string }[]>([])
let pollTimer: ReturnType<typeof setInterval> | null = null

async function refreshLogs() {
  if (!props.tunnel) return
  try {
    const res = await TunnelService.GetTunnelLogs({ token: props.token, id: props.tunnel.id })
    if (res.success) logs.value = res.logs || []
  } catch (e) {
    console.error('获取隧道日志失败', e)
  }
}

watch(() => props.tunnel, (t) => {
  if (t) {
    logs.value = []
    refreshLogs()
    if (!pollTimer) pollTimer = setInterval(refreshLogs, 3000)
  } else if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
}, { immediate: true })

onUnmounted(() => {
  if (pollTimer) {
    clearInterval(pollTimer)
    pollTimer = null
  }
})

function logLevelClass(level: string): string {
  if (level === 'error') return 'tunnel-log-error'
  if (level === 'warn') return 'tunnel-log-warn'
  return 'tunnel-log-info'
}
</script>

<template>
  <div v-if="tunnel" class="modal-overlay" @click.self="emit('close')">
    <div class="modal-box modal-box-lg">
      <div class="modal-header">
        <h2 class="modal-title">
          <svg viewBox="0 0 24 24" fill="none" width="14" height="14">
            <path d="M4 5a2 2 0 012-2h12a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
          隧道日志 · {{ tunnel.name }}
        </h2>
        <button class="modal-close" @click="emit('close')">&times;</button>
      </div>
      <div class="modal-body">
        <div class="tunnel-log-list">
          <div v-if="logs.length === 0" class="tunnel-log-empty">暂无日志（隧道未运行或尚无事件）</div>
          <div v-for="(log, idx) in logs" :key="idx" class="tunnel-log-item">
            <span class="tunnel-log-time">{{ log.time }}</span>
            <span class="tunnel-log-level" :class="logLevelClass(log.level)">{{ log.level }}</span>
            <span class="tunnel-log-msg">{{ log.message }}</span>
          </div>
        </div>
        <p class="tunnel-log-hint">每 3 秒自动刷新；仅保留最近 200 条运行事件</p>
      </div>
      <div class="modal-footer">
        <button class="btn-cancel" @click="emit('close')">关闭</button>
      </div>
    </div>
  </div>
</template>

<style scoped src="./tunnel-log.css"></style>
