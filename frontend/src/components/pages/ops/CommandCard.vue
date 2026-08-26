<script setup lang="ts">
import { computed } from 'vue'
import { Command } from '../../../../bindings/opsflash/server/models'

// ==================== 命令卡片（单张） ====================
// 由 OpsPage 通过 v-for 渲染，内部按钮行为全部以事件向上抛出。
// 用 computed 取 props 的最新值（避免解构后父级替换对象导致数据过期）。

const props = defineProps<{
  cmd: Command
  expanded: boolean
  streamCmdId: number
  runningCmdId: number
  pendingProcessId: number
  activeEnvId: number
  typeLabels: Record<string, string>
  modeLabels: Record<string, string>
  interpreterLabels: Record<string, string>
}>()

const emit = defineEmits([
  'toggle',
  'run',
  'stop-stream',
  'start-daemon',
  'stop-daemon',
  'start-interactive',
  'stop-interactive',
  'copy',
  'edit',
  'delete',
])

const cmd = computed(() => props.cmd)
const expanded = computed(() => props.expanded)
const streamCmdId = computed(() => props.streamCmdId)
const runningCmdId = computed(() => props.runningCmdId)
const pendingProcessId = computed(() => props.pendingProcessId)
const activeEnvId = computed(() => props.activeEnvId)
const typeLabels = computed(() => props.typeLabels)
const modeLabels = computed(() => props.modeLabels)
const interpreterLabels = computed(() => props.interpreterLabels)
</script>

<template>
  <div
    class="cmd-card"
    :class="{
      'cmd-card-collapsed': !expanded,
      'cmd-card-daemon-running': cmd.type === 'daemon' && cmd.running,
      'cmd-card-interactive-running': cmd.type === 'interactive' && cmd.running,
    }"
  >
    <!-- 卡片头（点击展开/收起） -->
    <div class="card-head" @click="emit('toggle', cmd.id)">
      <div class="card-title-wrap">
        <svg
          class="card-chevron"
          :class="{ 'card-chevron-open': expanded }"
          viewBox="0 0 24 24" fill="none" width="14" height="14"
        >
          <polyline points="6 9 12 15 18 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
        </svg>
        <h3 class="cmd-name">{{ cmd.name }}</h3>
        <span
          class="cmd-type-badge"
          :class="`cmd-type-${cmd.type}`"
        >{{ typeLabels[cmd.type] || cmd.type }}</span>
        <!-- 模式徽标：仅非本地（SSH/数据库）显示；本地由解释器徽标替代 -->
        <span
          v-if="cmd.mode && cmd.mode !== 'terminal'"
          class="cmd-mode-badge"
          :class="`cmd-mode-${cmd.mode}`"
          :title="cmd.mode === 'ssh' && cmd.connectionName ? cmd.connectionName : modeLabels[cmd.mode] || ''"
        >{{ modeLabels[cmd.mode] || cmd.mode }}</span>
        <!-- 解释器徽标：本地模式显示 cmd/powershell/bash（cmd 也显示），非本地不显示 -->
        <span
          v-if="!cmd.mode || cmd.mode === 'terminal'"
          class="cmd-interpreter-badge"
          :class="`cmd-interpreter-${cmd.interpreter || 'cmd'}`"
        >{{ interpreterLabels[cmd.interpreter || 'cmd'] || cmd.interpreter || 'cmd' }}</span>
      </div>

      <div class="card-head-actions">
        <!-- 非交互式：运行/停止按钮（本地模式流式，运行中可停止） -->
        <template v-if="cmd.type === 'non-interactive'">
          <button
            v-if="streamCmdId === cmd.id"
            class="run-btn run-btn-stop"
            @click.stop="emit('stop-stream', cmd)"
            title="停止执行"
          >
            <svg viewBox="0 0 24 24" fill="none">
              <rect x="6" y="6" width="12" height="12" rx="1" fill="currentColor"/>
            </svg>
          </button>
          <button
            v-else
            class="run-btn"
            :class="{ 'run-btn-loading': runningCmdId === cmd.id }"
            :disabled="runningCmdId === cmd.id"
            @click.stop="emit('run', cmd)"
            title="运行命令"
          >
            <span v-if="runningCmdId === cmd.id" class="run-spinner"></span>
            <svg v-else viewBox="0 0 24 24" fill="none">
              <polygon points="5 3 19 12 5 21 5 3" fill="currentColor"/>
            </svg>
          </button>
        </template>

        <!-- 守护进程 & 交互式：启动/停止按钮 -->
        <template v-else>
          <button
            v-if="!cmd.running"
            class="run-btn"
            :class="{ 'run-btn-loading': pendingProcessId === cmd.id }"
            :disabled="pendingProcessId === cmd.id"
            @click.stop="cmd.type === 'daemon' ? emit('start-daemon', cmd) : emit('start-interactive', cmd)"
            :title="cmd.type === 'daemon' ? '启动守护进程' : '启动交互式命令'"
          >
            <span v-if="pendingProcessId === cmd.id" class="run-spinner"></span>
            <svg v-else viewBox="0 0 24 24" fill="none">
              <polygon points="5 3 19 12 5 21 5 3" fill="currentColor"/>
            </svg>
          </button>
          <button
            v-else
            class="run-btn run-btn-stop"
            :class="{ 'run-btn-loading': pendingProcessId === cmd.id }"
            :disabled="pendingProcessId === cmd.id"
            @click.stop="cmd.type === 'daemon' ? emit('stop-daemon', cmd) : emit('stop-interactive', cmd)"
            :title="cmd.type === 'daemon' ? '停止守护进程' : '停止交互式命令'"
          >
            <span v-if="pendingProcessId === cmd.id" class="run-spinner run-spinner-stop"></span>
            <svg v-else viewBox="0 0 24 24" fill="none">
              <rect x="6" y="6" width="12" height="12" rx="1" fill="currentColor"/>
            </svg>
          </button>
        </template>
      </div>
    </div>

    <!-- 运行状态指示（收起时也常显） -->
    <div v-if="cmd.running && cmd.type !== 'non-interactive'" class="process-status-bar">
      <span class="process-pulse"></span>
      <span class="process-status-text">{{ cmd.type === 'daemon' ? '守护运行中' : '交互运行中' }}</span>
    </div>

    <!-- 可展开详情 -->
    <div v-show="expanded" class="card-detail">
      <!-- 命令块 -->
      <div class="cmd-block">
        <code class="cmd-text">$ {{ cmd.command }}</code>
      </div>

      <!-- 备注 -->
      <p v-if="cmd.remark" class="cmd-remark">{{ cmd.remark }}</p>

      <!-- 底部 -->
      <div class="card-footer">
        <div class="cmd-env-tag">
          <!-- 环境标签（常显于卡片最下方） -->
          <span class="cmd-env-badge" :title="'所属环境：' + cmd.envName">
            <span class="cmd-env-dot" :class="{ 'cmd-env-dot-active': cmd.environmentId === activeEnvId }"></span>
            <span class="cmd-env-name">{{ cmd.envName }}</span>
          </span>
          <span class="cmd-sort-tag" :title="'排序 ' + cmd.sortOrder">{{ cmd.sortOrder }}</span>
          <span v-if="cmd.mode === 'ssh' && cmd.connectionName" class="cmd-conn-tag" title="SSH 目标连接">
            <svg viewBox="0 0 24 24" fill="none" width="10" height="10">
              <path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
            {{ cmd.connectionName }}
          </span>
        </div>
        <div class="cmd-actions">
          <button class="action-btn" title="复制命令" @click="emit('copy', cmd)">
            <svg viewBox="0 0 24 24" fill="none" width="14" height="14">
              <rect x="9" y="9" width="13" height="13" rx="2" stroke="currentColor" stroke-width="2"/>
              <path d="M5 15H4a2 2 0 01-2-2V4a2 2 0 012-2h9a2 2 0 012 2v1" stroke="currentColor" stroke-width="2"/>
            </svg>
          </button>
          <button class="action-btn" title="编辑命令" @click="emit('edit', cmd)">
            <svg viewBox="0 0 24 24" fill="none" width="14" height="14">
              <path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </button>
          <button class="action-btn action-btn-danger" title="删除命令" @click="emit('delete', cmd)">
            <svg viewBox="0 0 24 24" fill="none" width="14" height="14">
              <polyline points="3 6 5 6 21 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./command-card.css"></style>
