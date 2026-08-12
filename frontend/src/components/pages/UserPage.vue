<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { UserService } from '../../../bindings/opsflash/server'

const props = defineProps<{ token: string }>()

interface User { id: number; username: string; role: string; created_at: string }

// --- 数据 ---
const users = ref<User[]>([])
const loading = ref(true)

async function loadUsers() {
  loading.value = true
  try {
    const res: any = await UserService.GetUsers({ token: props.token })
    if (res.success) users.value = res.users || []
  } catch (e) { console.error('加载用户列表失败', e) }
  finally { loading.value = false }
}

// --- 搜索 ---
const searchQuery = ref('')
const filteredUsers = computed(() => {
  if (!searchQuery.value) return users.value
  const q = searchQuery.value.toLowerCase()
  return users.value.filter(u => u.username.toLowerCase().includes(q))
})

// --- 统计 ---
const stats = computed(() => ({
  total: users.value.length,
  admins: users.value.filter(u => u.role === 'admin').length,
  todayNew: users.value.filter(u => {
    const today = new Date().toISOString().slice(0, 10)
    return u.created_at && u.created_at.startsWith(today)
  }).length,
}))

// --- 分页 ---
const currentPage = ref(1)
const pageSize = 10
const totalPages = computed(() => Math.ceil(filteredUsers.value.length / pageSize) || 1)
const paginatedUsers = computed(() => {
  const start = (currentPage.value - 1) * pageSize
  return filteredUsers.value.slice(start, start + pageSize)
})

// --- 新增/编辑弹窗 ---
const showModal = ref(false)
const modalTitle = ref('新增用户')
const editingUser = ref<User | null>(null)
const formUsername = ref('')
const formRole = ref('user')
const formPassword = ref('')
const formSubmitting = ref(false)
const formError = ref('')

function openAddModal() {
  editingUser.value = null; modalTitle.value = '新增用户'
  formUsername.value = ''; formRole.value = 'user'; formPassword.value = ''
  formError.value = ''; showModal.value = true
}
function openEditModal(user: User) {
  editingUser.value = user; modalTitle.value = '编辑用户'
  formUsername.value = user.username; formRole.value = user.role || 'user'
  formPassword.value = ''; formError.value = ''; showModal.value = true
}
function closeModal() { showModal.value = false; formError.value = '' }

async function handleSubmit() {
  formError.value = ''
  if (!formUsername.value.trim()) { formError.value = '请输入用户名'; return }
  formSubmitting.value = true
  try {
    if (editingUser.value) {
      const res: any = await UserService.UpdateUser({ token: props.token, id: editingUser.value.id, username: formUsername.value.trim(), role: formRole.value, password: formPassword.value || '' })
      if (!res.success) { formError.value = res.message || '更新失败'; return }
    } else {
      if (!formPassword.value) { formError.value = '请输入密码'; return }
      const res: any = await UserService.CreateUser({ token: props.token, username: formUsername.value.trim(), password: formPassword.value, role: formRole.value })
      if (!res.success) { formError.value = res.message || '创建失败'; return }
    }
    closeModal(); await loadUsers()
  } catch (e) { formError.value = '操作失败，请重试'; console.error(e) }
  finally { formSubmitting.value = false }
}

// --- 删除确认 ---
const showDeleteConfirm = ref(false)
const deleteTarget = ref<User | null>(null)
const deleteSubmitting = ref(false)
const deleteError = ref('')

function openDeleteConfirm(user: User) {
  deleteTarget.value = user; deleteError.value = ''; showDeleteConfirm.value = true
}
function closeDeleteConfirm() { showDeleteConfirm.value = false; deleteTarget.value = null; deleteError.value = '' }

async function confirmDelete() {
  if (!deleteTarget.value) return
  deleteError.value = ''; deleteSubmitting.value = true
  try {
    const res: any = await UserService.DeleteUser({ token: props.token, id: deleteTarget.value.id })
    if (!res.success) { deleteError.value = res.message || '删除失败'; return }
    closeDeleteConfirm(); await loadUsers()
  } catch (e) { deleteError.value = '删除失败，请重试'; console.error(e) }
  finally { deleteSubmitting.value = false }
}

onMounted(() => { loadUsers() })

defineExpose({ loadUsers })
</script>

<template>
  <div class="user-page">
    <!-- 统计卡片 -->
    <div class="stats-row">
      <div class="stat-card">
        <div class="stat-info"><span class="stat-label">总用户数</span><span class="stat-value">{{ stats.total }}</span></div>
        <div class="stat-icon stat-icon-red"><svg viewBox="0 0 24 24" fill="none"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="9" cy="7" r="4" stroke="currentColor" stroke-width="2"/><path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></div>
      </div>
      <div class="stat-card">
        <div class="stat-info"><span class="stat-label">管理员</span><span class="stat-value">{{ stats.admins }}</span></div>
        <div class="stat-icon stat-icon-purple"><svg viewBox="0 0 24 24" fill="none"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M9 12l2 2 4-4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></div>
      </div>
      <div class="stat-card">
        <div class="stat-info"><span class="stat-label">今日新增</span><span class="stat-value">{{ stats.todayNew }}</span></div>
        <div class="stat-icon stat-icon-green"><svg viewBox="0 0 24 24" fill="none"><path d="M16 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="8.5" cy="7" r="4" stroke="currentColor" stroke-width="2"/><line x1="20" y1="8" x2="20" y2="14" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="17" y1="11" x2="23" y2="11" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></div>
      </div>
    </div>

    <!-- 工具栏 -->
    <div class="toolbar">
      <div class="search-box">
        <svg class="search-icon" viewBox="0 0 24 24" fill="none"><circle cx="11" cy="11" r="8" stroke="currentColor" stroke-width="2"/><path d="M21 21l-4.35-4.35" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        <input v-model="searchQuery" type="text" class="search-input" placeholder="搜索用户名..."/>
      </div>
      <button class="btn-primary" @click="openAddModal">
        <svg viewBox="0 0 24 24" fill="none"><line x1="12" y1="5" x2="12" y2="19" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"/><line x1="5" y1="12" x2="19" y2="12" stroke="currentColor" stroke-width="2.5" stroke-linecap="round"/></svg>
        新增用户
      </button>
    </div>

    <!-- 表格 -->
    <div class="table-card">
      <div class="table-header-row">
        <div class="th-id">ID</div><div class="th-name">用户名</div><div class="th-role">角色</div><div class="th-time">创建时间</div><div class="th-action">操作</div>
      </div>
      <div class="table-divider"></div>
      <div v-if="loading" class="table-empty">加载中...</div>
      <template v-else>
        <div v-for="(user, idx) in paginatedUsers" :key="user.id" class="table-row" :class="{ 'row-alt': idx % 2 === 1 }">
          <div class="td-id">{{ user.id }}</div>
          <div class="td-name">{{ user.username }}</div>
          <div class="td-role"><span class="role-badge" :class="user.role === 'admin' ? 'role-admin' : 'role-user'">{{ user.role === 'admin' ? '管理员' : '普通用户' }}</span></div>
          <div class="td-time">{{ user.created_at }}</div>
          <div class="td-action">
            <button class="action-btn action-edit" title="编辑" @click="openEditModal(user)"><svg viewBox="0 0 24 24" fill="none"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
            <button class="action-btn action-delete" title="删除" @click="openDeleteConfirm(user)"><svg viewBox="0 0 24 24" fill="none"><path d="M3 6h18M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><line x1="10" y1="11" x2="10" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="14" y1="11" x2="14" y2="17" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></button>
          </div>
        </div>
        <div v-if="!loading && paginatedUsers.length === 0" class="table-empty">{{ searchQuery ? '未找到匹配的用户' : '暂无用户数据' }}</div>
      </template>
    </div>

    <!-- 分页 -->
    <div class="pagination" v-if="totalPages > 1">
      <span class="page-info">共 {{ filteredUsers.length }} 条，每页 {{ pageSize }} 条</span>
      <div class="page-nav">
        <button class="page-btn" :disabled="currentPage <= 1" @click="currentPage--"><svg viewBox="0 0 24 24" fill="none"><polyline points="15 18 9 12 15 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
        <button v-for="page in totalPages" :key="page" class="page-btn" :class="{ 'page-active': page === currentPage }" @click="currentPage = page">{{ page }}</button>
        <button class="page-btn" :disabled="currentPage >= totalPages" @click="currentPage++"><svg viewBox="0 0 24 24" fill="none"><polyline points="9 18 15 12 9 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg></button>
      </div>
    </div>

    <!-- 新增/编辑弹窗 -->
    <Teleport to="body">
      <div v-if="showModal" class="modal-overlay" @click.self="closeModal">
        <div class="modal-card">
          <div class="modal-header">
            <div class="modal-header-left">
              <div class="modal-icon" :class="editingUser ? 'icon-edit' : 'icon-add'">
                <svg v-if="!editingUser" viewBox="0 0 24 24" fill="none"><line x1="12" y1="5" x2="12" y2="19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="5" y1="12" x2="19" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
                <svg v-else viewBox="0 0 24 24" fill="none"><path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
              </div>
              <h2 class="modal-title">{{ modalTitle }}</h2>
            </div>
            <button class="modal-close" @click="closeModal"><svg viewBox="0 0 24 24" fill="none"><line x1="18" y1="6" x2="6" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="6" y1="6" x2="18" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></button>
          </div>
          <form class="modal-body" @submit.prevent="handleSubmit">
            <div class="form-field">
              <label class="form-label">用户名</label>
              <input v-model="formUsername" type="text" class="form-input" placeholder="请输入用户名" autocomplete="off" :disabled="formSubmitting"/>
            </div>
            <div class="form-field">
              <label class="form-label">角色</label>
              <div class="role-selector">
                <button type="button" class="role-option" :class="{ 'role-selected': formRole === 'user' }" @click="formRole = 'user'">
                  <svg viewBox="0 0 24 24" fill="none"><path d="M20 21v-2a4 4 0 00-4-4H8a4 4 0 00-4 4v2" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="7" r="4" stroke="currentColor" stroke-width="2"/></svg>
                  <span>普通用户</span>
                </button>
                <button type="button" class="role-option" :class="{ 'role-selected': formRole === 'admin' }" @click="formRole = 'admin'">
                  <svg viewBox="0 0 24 24" fill="none"><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><path d="M9 12l2 2 4-4" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
                  <span>管理员</span>
                </button>
              </div>
            </div>
            <div class="form-field">
              <label class="form-label">{{ editingUser ? '新密码' : '密码' }}<span v-if="editingUser" class="label-hint">（留空则不修改）</span></label>
              <input v-model="formPassword" type="password" class="form-input" :placeholder="editingUser ? '留空则不修改密码' : '请输入密码'" autocomplete="new-password" :disabled="formSubmitting"/>
            </div>
            <div v-if="formError" class="form-error">{{ formError }}</div>
            <div class="modal-actions">
              <button type="button" class="btn-cancel" :disabled="formSubmitting" @click="closeModal">取消</button>
              <button type="submit" class="btn-primary" :disabled="formSubmitting">
                <template v-if="formSubmitting"><span class="spinner"></span>提交中...</template>
                <template v-else>{{ editingUser ? '保存修改' : '确认新增' }}</template>
              </button>
            </div>
          </form>
        </div>
      </div>
    </Teleport>

    <!-- 删除确认弹窗 -->
    <Teleport to="body">
      <div v-if="showDeleteConfirm" class="modal-overlay" @click.self="closeDeleteConfirm">
        <div class="modal-card modal-card-sm">
          <div class="modal-header">
            <div class="modal-header-left"><div class="modal-icon icon-delete"><svg viewBox="0 0 24 24" fill="none"><circle cx="12" cy="12" r="10" stroke="currentColor" stroke-width="2"/><line x1="12" y1="8" x2="12" y2="12" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><circle cx="12" cy="16" r="0.5" fill="currentColor"/></svg></div><h2 class="modal-title">确认删除</h2></div>
            <button class="modal-close" @click="closeDeleteConfirm"><svg viewBox="0 0 24 24" fill="none"><line x1="18" y1="6" x2="6" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="6" y1="6" x2="18" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg></button>
          </div>
          <div class="modal-body">
            <p class="delete-warning">确定要删除用户 <strong>{{ deleteTarget?.username }}</strong> 吗？此操作不可撤销。</p>
            <div v-if="deleteError" class="form-error">{{ deleteError }}</div>
            <div class="modal-actions">
              <button type="button" class="btn-cancel" :disabled="deleteSubmitting" @click="closeDeleteConfirm">取消</button>
              <button type="button" class="btn-danger" :disabled="deleteSubmitting" @click="confirmDelete">
                <template v-if="deleteSubmitting"><span class="spinner"></span>删除中...</template>
                <template v-else>确认删除</template>
              </button>
            </div>
          </div>
        </div>
      </div>
    </Teleport>
  </div>
</template>

<style scoped>
.user-page { display: flex; flex-direction: column; gap: 24px; }
</style>
