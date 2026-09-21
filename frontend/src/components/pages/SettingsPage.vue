<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { OpsService, DashboardService } from '../../../bindings/opsflash/server'
import { Browser } from '@wailsio/runtime'
import UserPage from './UserPage.vue'

const props = defineProps<{ token: string }>()

// --- 标签页 ---
const activeTab = ref<'users' | 'environments' | 'about'>('users')

// --- 版本号 ---
const appVersion = ref('')
onMounted(async () => {
  try { appVersion.value = await DashboardService.GetVersion() } catch {}
})

function openLink(url: string) {
  Browser.OpenURL(url)
}

// --- 环境管理 ---
interface Environment { id: number; name: string; key: string; sortOrder: number }
const environments = ref<Environment[]>([])
const showEnvForm = ref(false)
const envFormMode = ref<'create' | 'edit'>('create')
const envForm = ref({ id: 0, name: '', key: '', sortOrder: 0 })
const envFormError = ref('')
const showDeleteEnvModal = ref(false)
const deleteEnvTarget = ref<Environment | null>(null)
const migrateTargetId = ref(0)
const deleteEnvError = ref('')

async function loadEnvironments() {
  try {
    const res = await OpsService.GetEnvironments({ token: props.token })
    if (res.success) environments.value = res.environments || []
  } catch (e) {
    console.error('获取环境失败', e)
  }
}

function openEnvForm(mode: 'create' | 'edit', env?: Environment) {
  envFormMode.value = mode
  if (mode === 'edit' && env) {
    envForm.value = { id: env.id, name: env.name, key: env.key, sortOrder: env.sortOrder }
  } else {
    envForm.value = { id: 0, name: '', key: '', sortOrder: 0 }
  }
  envFormError.value = ''
  showEnvForm.value = true
}

async function saveEnv() {
  const name = envForm.value.name.trim()
  const key = envForm.value.key.trim()
  if (!name) { envFormError.value = '环境名称不能为空'; return }
  if (!key) { envFormError.value = '环境 key 不能为空'; return }
  try {
    const res = envFormMode.value === 'edit'
      ? await OpsService.UpdateEnvironment({ token: props.token, id: envForm.value.id, name, key, sortOrder: envForm.value.sortOrder })
      : await OpsService.CreateEnvironment({ token: props.token, name, key, sortOrder: envForm.value.sortOrder })
    if (res.success) {
      showEnvForm.value = false
      await loadEnvironments()
    } else {
      envFormError.value = res.message || '操作失败'
    }
  } catch (e) {
    envFormError.value = '请求失败'
  }
}

function openDeleteEnvModal(env: Environment) {
  deleteEnvTarget.value = env
  migrateTargetId.value = environments.value.find(e => e.id !== env.id)?.id || 0
  deleteEnvError.value = ''
  showDeleteEnvModal.value = true
}

async function deleteEnv() {
  if (!deleteEnvTarget.value) return
  if (!migrateTargetId.value) { deleteEnvError.value = '请选择迁移目标'; return }
  try {
    const res = await OpsService.DeleteEnvironment({ token: props.token, id: deleteEnvTarget.value.id, targetId: migrateTargetId.value })
    if (res.success) {
      showDeleteEnvModal.value = false
      await loadEnvironments()
    } else {
      deleteEnvError.value = res.message || '删除失败'
    }
  } catch (e) {
    deleteEnvError.value = '请求失败'
  }
}

onMounted(loadEnvironments)
</script>

<template>
  <div class="settings-page">
    <div class="settings-sidebar">
      <div class="settings-sidebar-title">设置</div>
      <button class="settings-nav-item" :class="{ active: activeTab === 'users' }" @click="activeTab = 'users'">
        <svg viewBox="0 0 24 24" fill="none" width="18" height="18"><path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="7" r="4" stroke="currentColor" stroke-width="2"/></svg>
        用户管理
      </button>
      <button class="settings-nav-item" :class="{ active: activeTab === 'environments' }" @click="activeTab = 'environments'">
        <svg viewBox="0 0 24 24" fill="none" width="18" height="18"><rect x="2" y="3" width="20" height="14" rx="2" stroke="currentColor" stroke-width="2"/><line x1="8" y1="21" x2="16" y2="21" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="12" y1="17" x2="12" y2="21" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        环境管理
      </button>
      <button class="settings-nav-item" :class="{ active: activeTab === 'about' }" @click="activeTab = 'about'">
        <svg viewBox="0 0 24 24" fill="none" width="18" height="18"><circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="2"/><line x1="12" y1="16" x2="12" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="12" y1="8" x2="12.01" y2="8" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        关于
      </button>
    </div>
    <div class="settings-main">
      <!-- 用户管理 -->
      <div v-if="activeTab === 'users'" class="settings-content">
        <UserPage :token="props.token" />
      </div>
      <!-- 环境管理 -->
      <div v-if="activeTab === 'environments'" class="settings-content">
        <div class="env-toolbar">
          <span class="env-count">共 {{ environments.length }} 个环境</span>
          <button class="btn-add" @click="openEnvForm('create')">+ 新建环境</button>
        </div>
        <div class="env-list">
          <div v-for="env in environments" :key="env.id" class="env-card">
            <div class="env-card-main">
              <div class="env-card-name">
                {{ env.name }}
                <span class="env-card-key">key: {{ env.key }}</span>
              </div>
              <div class="env-card-actions">
                <button class="btn-edit" @click="openEnvForm('edit', env)">编辑</button>
                <button class="btn-delete" @click="openDeleteEnvModal(env)">删除</button>
              </div>
            </div>
          </div>
        </div>

        <!-- 新建/编辑环境弹窗 -->
        <div v-if="showEnvForm" class="modal-overlay" @click.self="showEnvForm = false">
          <div class="modal-box">
            <div class="modal-header">
              <h3 class="modal-title">{{ envFormMode === 'edit' ? '编辑环境' : '新建环境' }}</h3>
              <button class="modal-close" @click="showEnvForm = false">&times;</button>
            </div>
            <div class="modal-body">
              <div class="form-group">
                <label class="form-label">环境名称</label>
                <input v-model="envForm.name" type="text" class="form-input" placeholder="如：开发环境" />
              </div>
              <div class="form-group">
                <label class="form-label">环境 Key</label>
                <input v-model="envForm.key" type="text" class="form-input" placeholder="如：dev" />
              </div>
              <div class="form-group">
                <label class="form-label">排序值</label>
                <input v-model.number="envForm.sortOrder" type="number" class="form-input" />
              </div>
              <p v-if="envFormError" class="form-error">{{ envFormError }}</p>
            </div>
            <div class="modal-footer">
              <button class="btn-cancel" @click="showEnvForm = false">取消</button>
              <button class="btn-primary" @click="saveEnv">保存</button>
            </div>
          </div>
        </div>

        <!-- 删除环境弹窗 -->
        <div v-if="showDeleteEnvModal" class="modal-overlay" @click.self="showDeleteEnvModal = false">
          <div class="modal-box modal-box-sm">
            <div class="modal-header">
              <h3 class="modal-title modal-title-warning">删除环境</h3>
              <button class="modal-close" @click="showDeleteEnvModal = false">&times;</button>
            </div>
            <div class="modal-body">
              <p class="confirm-text">确定删除环境「{{ deleteEnvTarget?.name }}」吗？</p>
              <p class="confirm-hint">该环境下的命令将迁移到：</p>
              <select v-model="migrateTargetId" class="form-select">
                <option v-for="env in environments.filter(e => e.id !== deleteEnvTarget?.id)" :key="env.id" :value="env.id">
                  {{ env.name }}
                </option>
              </select>
              <p v-if="deleteEnvError" class="form-error">{{ deleteEnvError }}</p>
            </div>
            <div class="modal-footer">
              <button class="btn-cancel" @click="showDeleteEnvModal = false">取消</button>
              <button class="btn-danger" @click="deleteEnv">确认删除</button>
            </div>
          </div>
        </div>
      </div>
      <!-- 关于 -->
      <div v-if="activeTab === 'about'" class="settings-content">
        <div class="about-page">
          <img src="/logo.png" class="about-logo" alt="OpsFlash" />
          <h1 class="about-name">OpsFlash</h1>
          <p class="about-slogan">轻量级运维极速引擎</p>
          <div class="about-version">{{ appVersion }}</div>
          <div class="about-links">
            <a href="#" @click.prevent="openLink('https://gitee.com/LeapSunrise/OpsFlash')" class="about-link">
              <svg viewBox="0 0 24 24" fill="none" width="18" height="18"><path d="M12 2C6.48 2 2 6.48 2 12s4.48 10 10 10 10-4.48 10-10S17.52 2 12 2zm-1 15h-2v-6h2v6zm-1-7c-.55 0-1-.45-1-1s.45-1 1-1 1 .45 1 1-.45 1-1 1zm5 7h-2v-3.5c0-.83-.67-1.5-1.5-1.5S10 12.67 10 13.5V17H8v-6h2v.81c.47-.75 1.31-1.31 2.5-1.31 1.93 0 2.5 1.32 2.5 3.09V17z" fill="currentColor"/></svg>
              Gitee
            </a>
            <a href="#" @click.prevent="openLink('https://github.com/LeapSunrise26/OpsFlash')" class="about-link">
              <svg viewBox="0 0 24 24" fill="none" width="18" height="18"><path d="M12 2C6.477 2 2 6.477 2 12c0 4.42 2.865 8.166 6.839 9.489.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.34-3.369-1.34-.454-1.156-1.11-1.463-1.11-1.463-.908-.62.069-.608.069-.608 1.003.07 1.531 1.03 1.531 1.03.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.11-4.555-4.943 0-1.091.39-1.984 1.029-2.683-.103-.253-.446-1.27.098-2.647 0 0 .84-.269 2.75 1.025A9.564 9.564 0 0112 6.844c.85.004 1.705.115 2.504.337 1.909-1.294 2.747-1.025 2.747-1.025.546 1.377.203 2.394.1 2.647.64.699 1.028 1.592 1.028 2.683 0 3.842-2.339 4.687-4.566 4.935.359.309.678.919.678 1.852 0 1.336-.012 2.415-.012 2.743 0 .267.18.578.688.48C19.138 20.161 22 16.416 22 12c0-5.523-4.477-10-10-10z" fill="currentColor"/></svg>
              GitHub
            </a>
          </div>
          <div class="about-tech">
            <h3 class="about-tech-title">使用技术</h3>
            <div class="about-tech-grid">
              <div class="about-tech-item"><span class="tech-label">桌面框架</span><span class="tech-value">Wails v3</span></div>
              <div class="about-tech-item"><span class="tech-label">后端语言</span><span class="tech-value">Go 1.25</span></div>
              <div class="about-tech-item"><span class="tech-label">前端框架</span><span class="tech-value">Vue 3 + TypeScript</span></div>
              <div class="about-tech-item"><span class="tech-label">构建工具</span><span class="tech-value">Vite 8</span></div>
              <div class="about-tech-item"><span class="tech-label">数据库</span><span class="tech-value">SQLite (modernc.org)</span></div>
              <div class="about-tech-item"><span class="tech-label">终端渲染</span><span class="tech-value">xterm.js</span></div>
            </div>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./settings-page.css"></style>
