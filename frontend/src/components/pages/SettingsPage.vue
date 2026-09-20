<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { OpsService } from '../../../bindings/opsflash/server'
import UserPage from './UserPage.vue'

const props = defineProps<{ token: string }>()

// --- 标签页 ---
const activeTab = ref<'users' | 'environments'>('users')

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
    </div>
  </div>
</template>

<style scoped>
.settings-page {
  display: flex;
  height: 100%;
  gap: 0;
}

.settings-sidebar {
  width: 200px;
  flex-shrink: 0;
  background: #0d1117;
  border-right: 1px solid #21262d;
  padding: 16px 12px;
}

.settings-sidebar-title {
  font-size: 16px;
  font-weight: 700;
  color: #FF77B0;
  padding: 0 12px 16px;
  border-bottom: 1px solid #21262d;
  margin-bottom: 8px;
}

.settings-nav-item {
  display: flex;
  align-items: center;
  gap: 10px;
  width: 100%;
  padding: 10px 12px;
  border: none;
  border-radius: 8px;
  background: transparent;
  color: rgba(255,255,255,0.65);
  font-size: 14px;
  cursor: pointer;
  transition: all 0.2s ease;
  text-align: left;
}

.settings-nav-item:hover {
  background: rgba(255,255,255,0.06);
  color: #fff;
}

.settings-nav-item.active {
  background: rgba(255,0,110,0.12);
  color: #FF77B0;
  font-weight: 600;
}

.settings-main {
  flex: 1;
  min-width: 0;
  overflow-y: auto;
  padding: 24px 32px;
}

.settings-content {
  height: 100%;
}

/* --- 环境管理 --- */
.env-toolbar {
  display: flex;
  justify-content: space-between;
  align-items: center;
  margin-bottom: 16px;
}

.env-count {
  color: rgba(255,255,255,0.5);
  font-size: 13px;
}

.btn-add {
  padding: 8px 16px;
  border-radius: 8px;
  border: none;
  background: #FF0050;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s;
}

.btn-add:hover {
  background: #E60047;
}

.env-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.env-card {
  background: #161b22;
  border: 1px solid #21262d;
  border-radius: 10px;
  padding: 14px 18px;
}

.env-card-main {
  display: flex;
  justify-content: space-between;
  align-items: center;
}

.env-card-name {
  font-size: 14px;
  font-weight: 600;
  color: #e6edf3;
  display: flex;
  align-items: center;
  gap: 12px;
}

.env-card-key {
  font-size: 12px;
  color: rgba(255,255,255,0.4);
  font-weight: 400;
}

.env-card-actions {
  display: flex;
  gap: 8px;
}

.btn-edit, .btn-delete {
  padding: 6px 12px;
  border: 1px solid #30363d;
  border-radius: 6px;
  background: transparent;
  color: rgba(255,255,255,0.65);
  font-size: 12px;
  cursor: pointer;
  transition: all 0.2s;
}

.btn-edit:hover { border-color: #FF77B0; color: #FF77B0; }
.btn-delete:hover { border-color: #f85149; color: #f85149; }

/* --- 弹窗 --- */
.modal-overlay {
  position: fixed;
  inset: 0;
  background: rgba(0,0,0,0.6);
  display: flex;
  justify-content: center;
  align-items: center;
  z-index: 1000;
}

.modal-box {
  background: #161b22;
  border: 1px solid #30363d;
  border-radius: 14px;
  width: 420px;
  max-width: 90vw;
}

.modal-box-sm { width: 380px; }

.modal-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 18px 22px;
  border-bottom: 1px solid #21262d;
}

.modal-title { font-size: 16px; font-weight: 700; color: #e6edf3; }
.modal-title-warning { color: #f0883e; }

.modal-close {
  background: none;
  border: none;
  color: rgba(255,255,255,0.5);
  font-size: 22px;
  cursor: pointer;
  padding: 0 4px;
}

.modal-body { padding: 20px 22px; }

.form-group { margin-bottom: 14px; }
.form-label { display: block; font-size: 13px; color: rgba(255,255,255,0.6); margin-bottom: 6px; }

.form-input, .form-select {
  width: 100%;
  padding: 10px 12px;
  background: #0d1117;
  border: 1px solid #30363d;
  border-radius: 8px;
  color: #e6edf3;
  font-size: 14px;
  box-sizing: border-box;
}

.form-input:focus, .form-select:focus {
  outline: none;
  border-color: #FF0050;
  box-shadow: 0 0 0 3px rgba(255,0,80,0.15);
}

.form-error { color: #f85149; font-size: 13px; margin-top: 6px; }
.confirm-text { color: #e6edf3; font-size: 14px; margin-bottom: 8px; }
.confirm-hint { color: rgba(255,255,255,0.5); font-size: 13px; margin-bottom: 8px; }

.modal-footer {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
  padding: 16px 22px;
  border-top: 1px solid #21262d;
}

.btn-cancel {
  padding: 8px 18px;
  border: 1px solid #30363d;
  border-radius: 8px;
  background: transparent;
  color: rgba(255,255,255,0.65);
  font-size: 13px;
  cursor: pointer;
}

.btn-primary {
  padding: 8px 22px;
  border: none;
  border-radius: 8px;
  background: #FF0050;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.btn-primary:hover { background: #E60047; }

.btn-danger {
  padding: 8px 22px;
  border: none;
  border-radius: 8px;
  background: #da3633;
  color: #fff;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
}

.btn-danger:hover { background: #b62324; }
</style>
