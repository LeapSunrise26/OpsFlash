<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ScriptsService } from '../../../bindings/opsflash/server'
import { Environment } from '../../../bindings/opsflash/server/models'

const props = defineProps<{ token: string }>()

// ==================== 状态 ====================
const environments = ref<Environment[]>([])
const loading = ref(false)
const error = ref('')

// 新建/编辑表单
const showForm = ref(false)
const isEdit = ref(false)
const form = ref({ id: 0, name: '', key: '', sortOrder: 0 })
const formError = ref('')

// 删除确认（需选择脚本迁移目标环境）
const showDelete = ref(false)
const deleteTarget = ref<Environment | null>(null)
const migrateTargetId = ref(0)
const deleteError = ref('')

// 可作为迁移目标的其他环境（排除自身）
const otherEnvs = computed(() => environments.value.filter((e) => e.id !== deleteTarget.value?.id))

// ==================== 加载 ====================
async function loadEnvironments() {
  loading.value = true
  error.value = ''
  try {
    const res = await ScriptsService.GetEnvironments({ token: props.token })
    if (res.success) {
      environments.value = res.environments || []
    } else {
      error.value = res.message || '加载失败'
    }
  } catch (e) {
    error.value = '加载环境失败'
    console.error(e)
  } finally {
    loading.value = false
  }
}

onMounted(loadEnvironments)

// ==================== 新建 / 编辑 ====================
function openCreate() {
  form.value = { id: 0, name: '', key: '', sortOrder: 0 }
  isEdit.value = false
  formError.value = ''
  showForm.value = true
}

function openEdit(env: Environment) {
  form.value = { id: env.id, name: env.name, key: env.key, sortOrder: env.sortOrder }
  isEdit.value = true
  formError.value = ''
  showForm.value = true
}

async function saveEnv() {
  const name = form.value.name.trim()
  const key = form.value.key.trim()
  if (!name) {
    formError.value = '环境名称不能为空'
    return
  }
  if (!key) {
    formError.value = '环境 key 不能为空（仅限小写字母、数字、_、-）'
    return
  }
  try {
    const res = isEdit.value
      ? await ScriptsService.UpdateEnvironment({ token: props.token, id: form.value.id, name, key, sortOrder: form.value.sortOrder })
      : await ScriptsService.CreateEnvironment({ token: props.token, name, key, sortOrder: form.value.sortOrder })
    if (res.success) {
      environments.value = res.environments || []
      showForm.value = false
    } else {
      formError.value = res.message || '保存失败'
    }
  } catch (e) {
    formError.value = isEdit.value ? '更新失败' : '创建失败'
    console.error(e)
  }
}

// ==================== 删除 ====================
function openDelete(env: Environment) {
  deleteTarget.value = env
  // 默认迁移目标：首个其他环境
  const others = environments.value.filter((e) => e.id !== env.id)
  migrateTargetId.value = others[0]?.id || 0
  deleteError.value = ''
  showDelete.value = true
}

async function confirmDelete() {
  if (!deleteTarget.value) return
  if (migrateTargetId.value === 0) {
    deleteError.value = '请选择脚本迁移的目标环境'
    return
  }
  try {
    const res = await ScriptsService.DeleteEnvironment({ token: props.token, id: deleteTarget.value.id, targetId: migrateTargetId.value })
    if (res.success) {
      await loadEnvironments()
      showDelete.value = false
    } else {
      deleteError.value = res.message || '删除失败'
    }
  } catch (e) {
    deleteError.value = '删除失败'
    console.error(e)
  }
}
</script>

<template>
  <div class="env-page">
    <!-- 工具栏 -->
    <div class="env-toolbar">
      <div class="env-left">
        <span class="env-count">共 {{ environments.length }} 个环境</span>
      </div>
      <div class="env-right">
        <button class="btn-add" @click="openCreate">
          <span class="add-plus">+</span>
          <span>新建环境</span>
        </button>
      </div>
    </div>

    <p v-if="error" class="env-error">{{ error }}</p>

    <!-- 环境列表 -->
    <div class="env-list" v-if="!loading && environments.length > 0">
      <div
        v-for="env in environments"
        :key="env.id"
        class="env-card"
      >
        <div class="env-card-main">
          <div class="env-card-name">
            {{ env.name }}
            <span class="env-card-key">key: {{ env.key }}</span>
          </div>
          <div class="env-card-meta">
            排序 {{ env.sortOrder }} · 脚本目录 <code>data/scripts/{{ env.key }}/</code>
          </div>
        </div>
        <div class="env-card-actions">
          <button class="btn-secondary btn-sm" @click="openEdit(env)">编辑</button>
          <button
            class="btn-danger btn-sm"
            :disabled="environments.length <= 1"
            :title="environments.length <= 1 ? '至少保留一个环境' : '删除环境'"
            @click="openDelete(env)"
          >删除</button>
        </div>
      </div>
    </div>

    <div v-else-if="!loading" class="env-empty">
      <p>暂无环境，点击「新建环境」创建第一个环境</p>
      <p class="env-empty-hint">环境用于隔离脚本：脚本将存放在 data/scripts/{环境 key}/ 目录下</p>
    </div>

    <!-- 新建/编辑弹窗 -->
    <div v-if="showForm" class="modal-overlay" @click.self="showForm = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title">{{ isEdit ? '编辑环境' : '新建环境' }}</h2>
          <button class="modal-close" @click="showForm = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">环境名称 <span class="required">*</span></label>
            <input v-model="form.name" type="text" class="form-input" placeholder="如：生产环境" @keyup.enter="saveEnv" />
          </div>
          <div class="form-group">
            <label class="form-label">英文 key <span class="required">*</span></label>
            <input v-model="form.key" type="text" class="form-input" placeholder="如：prod（仅小写字母、数字、_、-）" @keyup.enter="saveEnv" />
            <p class="form-hint">作为脚本磁盘目录名：data/scripts/{{ form.key || 'key' }}/，创建后修改会迁移目录</p>
          </div>
          <div class="form-group">
            <label class="form-label">排序</label>
            <input v-model.number="form.sortOrder" type="number" class="form-input" placeholder="数值越大越靠前" />
          </div>
          <p v-if="formError" class="form-error">{{ formError }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showForm = false">取消</button>
          <button class="btn-primary" @click="saveEnv">保存</button>
        </div>
      </div>
    </div>

    <!-- 删除确认弹窗 -->
    <div v-if="showDelete" class="modal-overlay" @click.self="showDelete = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title modal-title-warning">
            <span class="warning-icon">!</span>
            删除环境
          </h2>
          <button class="modal-close" @click="showDelete = false">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">确定删除环境「{{ deleteTarget?.name }}」吗？</p>
          <div class="confirm-warning-box">
            <span class="warning-icon-small">!</span>
            <span>该环境下的脚本将迁移到下方选择的目标环境（磁盘目录一并移动）。</span>
          </div>
          <div class="form-group" style="margin-top: 14px;">
            <label class="form-label">脚本迁移目标环境 <span class="required">*</span></label>
            <select v-model.number="migrateTargetId" class="form-select">
              <option v-for="e in otherEnvs" :key="e.id" :value="e.id">{{ e.name }}（{{ e.key }}）</option>
            </select>
          </div>
          <p v-if="deleteError" class="form-error">{{ deleteError }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showDelete = false">取消</button>
          <button class="btn-danger" @click="confirmDelete">确认删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
.env-page {
  display: flex;
  flex-direction: column;
  gap: 16px;
  color: var(--text);
  --wails-draggable: no-drag;
}

.env-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
}
.env-count { font-size: 13px; color: rgba(255, 255, 255, 0.5); }
.btn-add {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  background: rgba(255, 0, 110, 0.9);
  color: #fff;
  border: none;
  border-radius: 8px;
  padding: 8px 14px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s ease;
}
.btn-add:hover { background: #FF2D72; }
.add-plus { font-size: 16px; line-height: 1; }

.env-error { color: #EF4444; font-size: 13px; margin: 0; }

.env-list { display: flex; flex-direction: column; gap: 10px; }
.env-card {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 14px 16px;
  background: rgba(255, 255, 255, 0.04);
  border: 1px solid rgba(255, 255, 255, 0.08);
  border-radius: 12px;
}
.env-card-main { min-width: 0; }
.env-card-name { font-size: 15px; font-weight: 600; color: #f4f6fb; display: flex; align-items: center; gap: 10px; flex-wrap: wrap; }
.env-card-key {
  font-size: 11px;
  font-weight: 500;
  color: #FF77B0;
  background: rgba(255, 0, 110, 0.12);
  border: 1px solid rgba(255, 0, 110, 0.25);
  border-radius: 6px;
  padding: 2px 8px;
  font-family: 'JetBrains Mono', Consolas, monospace;
}
.env-card-meta { font-size: 12px; color: rgba(255, 255, 255, 0.45); margin-top: 6px; }
.env-card-meta code {
  font-family: 'JetBrains Mono', Consolas, monospace;
  background: rgba(255, 255, 255, 0.06);
  padding: 1px 6px;
  border-radius: 4px;
  color: rgba(255, 255, 255, 0.75);
}
.env-card-actions { display: flex; gap: 8px; flex-shrink: 0; }

.env-empty { text-align: center; padding: 60px 20px; color: rgba(255, 255, 255, 0.5); }
.env-empty-hint { font-size: 12px; color: rgba(255, 255, 255, 0.3); margin-top: 8px; }

.btn-sm { padding: 6px 12px; font-size: 12px; }

/* ---------- 弹窗/表单（与脚本库一致） ---------- */
.modal-overlay {
  position: fixed;
  inset: 0;
  z-index: 1000;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.6);
  backdrop-filter: blur(4px);
  -webkit-backdrop-filter: blur(4px);
}
.modal-box {
  background: #141824;
  border: 1px solid rgba(255, 255, 255, 0.1);
  border-radius: 14px;
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.55);
  display: flex;
  flex-direction: column;
  max-height: calc(100vh - 100px);
}
.modal-box-sm { width: 420px; }
.modal-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 14px 18px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.08);
  flex-shrink: 0;
}
.modal-title { margin: 0; font-size: 15px; font-weight: 600; color: #f4f6fb; display: flex; align-items: center; gap: 8px; }
.modal-title-warning .warning-icon {
  display: inline-flex; align-items: center; justify-content: center;
  width: 18px; height: 18px; border-radius: 50%;
  background: rgba(251, 191, 36, 0.2); color: #FBBF24;
  font-size: 12px; font-weight: 700;
}
.modal-close { background: none; border: none; color: rgba(255,255,255,0.5); font-size: 20px; line-height: 1; cursor: pointer; padding: 2px 4px; }
.modal-close:hover { color: #f4f6fb; }
.modal-body { padding: 16px 18px; overflow-y: auto; }
.modal-footer { display: flex; justify-content: flex-end; gap: 10px; padding: 12px 18px; border-top: 1px solid rgba(255,255,255,0.08); }

.form-group { margin-bottom: 14px; }
.form-label { display: block; font-size: 13px; color: rgba(255,255,255,0.6); margin-bottom: 6px; }
.form-label .required { color: #EF4444; }
.form-input, .form-select {
  width: 100%;
  background: rgba(255,255,255,0.06);
  border: 1px solid rgba(255,255,255,0.12);
  border-radius: 8px;
  color: #e6e9f0;
  font-size: 13px;
  padding: 8px 10px;
  outline: none;
  box-sizing: border-box;
}
.form-input:focus, .form-select:focus { border-color: rgba(255,0,110,0.5); }
.form-input::placeholder { color: rgba(255,255,255,0.25); }
.form-select option { background: #141824; }
.form-hint { font-size: 11px; color: rgba(255,255,255,0.3); margin-top: 4px; }
.form-error { font-size: 13px; color: #EF4444; margin: 8px 0 0; }
.confirm-text { font-size: 14px; color: rgba(255,255,255,0.85); margin: 0; }
.confirm-warning-box {
  display: flex; gap: 8px; align-items: flex-start;
  margin-top: 12px; padding: 10px 12px;
  background: rgba(251, 191, 36, 0.1);
  border: 1px solid rgba(251, 191, 36, 0.25);
  border-radius: 8px;
  font-size: 12px; color: rgba(251, 191, 36, 0.9);
}
.warning-icon-small {
  flex-shrink: 0;
  display: inline-flex; align-items: center; justify-content: center;
  width: 16px; height: 16px; border-radius: 50%;
  background: rgba(251, 191, 36, 0.2); color: #FBBF24;
  font-size: 11px; font-weight: 700;
}

.btn-primary {
  background: rgba(255,0,110,0.9);
  color: #fff;
  border: none;
  border-radius: 8px;
  padding: 8px 16px;
  font-size: 13px;
  font-weight: 600;
  cursor: pointer;
  transition: background 0.2s ease;
}
.btn-primary:hover:not(:disabled) { background: #FF2D72; }
.btn-primary:disabled { opacity: 0.4; cursor: not-allowed; }
.btn-secondary {
  background: rgba(255,255,255,0.08);
  color: rgba(255,255,255,0.85);
  border: 1px solid rgba(255,255,255,0.14);
  border-radius: 8px;
  padding: 8px 16px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s ease;
}
.btn-secondary:hover { background: rgba(255,255,255,0.14); }
.btn-danger {
  background: rgba(239,68,68,0.15);
  color: #EF4444;
  border: 1px solid rgba(239,68,68,0.3);
  border-radius: 8px;
  padding: 8px 16px;
  font-size: 13px;
  cursor: pointer;
  transition: background 0.2s ease;
}
.btn-danger:hover:not(:disabled) { background: rgba(239,68,68,0.25); }
.btn-danger:disabled { opacity: 0.4; cursor: not-allowed; }
.btn-cancel {
  background: transparent;
  color: rgba(255,255,255,0.6);
  border: 1px solid rgba(255,255,255,0.14);
  border-radius: 8px;
  padding: 8px 16px;
  font-size: 13px;
  cursor: pointer;
}
.btn-cancel:hover { color: #fff; background: rgba(255,255,255,0.06); }
</style>
