<script setup lang="ts">
import { Tunnel } from '../../../../bindings/opsflash/server/models'

// 单张隧道卡片：纯展示 + 操作按钮上抛
const props = defineProps<{ tunnel: Tunnel }>()
const emit = defineEmits<{
  (e: 'toggle'): void
  (e: 'edit'): void
  (e: 'delete'): void
  (e: 'logs'): void
}>()

const t = () => props.tunnel

const typeLabels: Record<string, string> = {
  local: '本地转发',
  remote: '远程转发',
  dynamic: '动态转发',
}

function statusClass(): string {
  const x = t()
  if (x.reconnecting) return 'tunnel-status-reconnecting'
  if (x.running) return 'tunnel-status-running'
  if (x.error) return 'tunnel-status-error'
  return 'tunnel-status-stopped'
}

function statusText(): string {
  const x = t()
  if (x.reconnecting) return '重连中'
  if (x.running) return `运行中 · ${x.connections} 连接`
  if (x.error) return '错误'
  return '已停止'
}

// 转发规则显示辅助：按类型渲染不同的语义
function forwardLeftLabel(): string {
  const x = t()
  if (x.type === 'remote') return '远程(SSH)'
  if (x.type === 'dynamic') return '本地代理'
  return '本地'
}

function forwardLeftAddr(): string {
  const x = t()
  if (x.type === 'remote') return `${x.remoteHost}:${x.remotePort}`
  return `${x.localHost}:${x.localPort}`
}

function forwardRightLabel(): string {
  const x = t()
  if (x.type === 'remote') return '本地目标'
  if (x.type === 'dynamic') return '转发目标'
  return '远程'
}

function forwardRightAddr(): string {
  const x = t()
  if (x.type === 'remote') return `${x.localHost}:${x.localPort}`
  if (x.type === 'dynamic') return 'SOCKS5 任意'
  return `${x.remoteHost}:${x.remotePort}`
}

function fmtBytes(n: number): string {
  if (!n) return '0 B'
  if (n < 1024) return n + ' B'
  if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB'
  if (n < 1024 * 1024 * 1024) return (n / 1024 / 1024).toFixed(1) + ' MB'
  return (n / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}
</script>

<template>
  <div
    class="tunnel-card"
    :class="{ 'tunnel-card-running': tunnel.running, 'tunnel-card-error': tunnel.error && !tunnel.running }"
  >
    <!-- 状态指示灯 + 名称 -->
    <div class="tunnel-card-head">
      <span class="tunnel-status-dot" :class="statusClass()"></span>
      <h3 class="tunnel-name">{{ tunnel.name }}</h3>
      <span class="tunnel-type-badge">{{ typeLabels[tunnel.type] || tunnel.type }}</span>
    </div>

    <!-- SSH 连接 -->
    <div class="tunnel-ssh">
      <svg viewBox="0 0 24 24" fill="none" width="12" height="12">
        <rect x="2" y="4" width="20" height="16" rx="2" stroke="currentColor" stroke-width="2"/>
        <line x1="2" y1="9" x2="22" y2="9" stroke="currentColor" stroke-width="2"/>
      </svg>
      <span>{{ tunnel.connectionName || 'SSH-' + tunnel.connectionId }}</span>
    </div>

    <!-- 转发规则可视化 -->
    <div class="tunnel-forward">
      <div class="tunnel-forward-local">
        <span class="tunnel-forward-label">{{ forwardLeftLabel() }}</span>
        <span class="tunnel-forward-addr">{{ forwardLeftAddr() }}</span>
      </div>
      <svg class="tunnel-forward-arrow" viewBox="0 0 24 24" fill="none" width="16" height="16">
        <line x1="3" y1="12" x2="21" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
        <polyline points="15 6 21 12 15 18" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
      <div class="tunnel-forward-remote">
        <span class="tunnel-forward-label">{{ forwardRightLabel() }}</span>
        <span class="tunnel-forward-addr">{{ forwardRightAddr() }}</span>
      </div>
    </div>

    <!-- 状态 + 标签（左）+ 流量（右）一行 -->
    <div class="tunnel-meta-row">
      <span class="tunnel-status-text" :class="statusClass()">{{ statusText() }}</span>
      <span v-if="tunnel.error" class="tunnel-error-msg" :class="{ 'tunnel-warn-msg': tunnel.running }" :title="tunnel.error">{{ tunnel.error }}</span>
      <span class="tunnel-meta-tags">
        <span v-if="tunnel.groupName" class="tunnel-tag tunnel-tag-group" :title="'分组：' + tunnel.groupName">{{ tunnel.groupName }}</span>
        <span v-if="tunnel.autoStart" class="tunnel-tag tunnel-tag-auto" title="应用启动时自动启动">自动</span>
        <span v-if="tunnel.autoReconnect" class="tunnel-tag tunnel-tag-reconn" title="断线自动重连">重连</span>
      </span>
      <span class="tunnel-traffic" title="↓ 接收（客户端→远程） · ↑ 发送（远程→客户端）">
        ↓{{ fmtBytes(tunnel.bytesIn) }}&nbsp;↑{{ fmtBytes(tunnel.bytesOut) }}
      </span>
    </div>

    <p v-if="tunnel.remark" class="tunnel-remark">{{ tunnel.remark }}</p>

    <!-- 操作按钮 -->
    <div class="tunnel-card-footer">
      <button
        class="tunnel-toggle-btn"
        :class="tunnel.running ? 'tunnel-toggle-stop' : 'tunnel-toggle-start'"
        @click="emit('toggle')"
      >
        {{ tunnel.running ? '停止' : '启动' }}
      </button>
      <button class="tunnel-action-btn" title="查看日志" @click="emit('logs')">
        <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
          <path d="M4 5a2 2 0 012-2h12a2 2 0 012 2v14a2 2 0 01-2 2H6a2 2 0 01-2-2V5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          <line x1="8" y1="9" x2="16" y2="9" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
          <line x1="8" y1="13" x2="16" y2="13" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
        </svg>
      </button>
      <button class="tunnel-action-btn" title="编辑隧道" @click="emit('edit')">
        <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
          <path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          <path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
      <button class="tunnel-action-btn tunnel-action-danger" title="删除隧道" @click="emit('delete')">
        <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
          <polyline points="3 6 5 6 21 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
      </button>
    </div>
  </div>
</template>

<style scoped src="./tunnel-card.css"></style>
