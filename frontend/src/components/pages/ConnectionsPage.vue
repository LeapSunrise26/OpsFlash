<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ConnService } from '../../../bindings/opsflash/server'
import { Connection } from '../../../bindings/opsflash/server/models'

const props = defineProps<{ token: string }>()

const emit = defineEmits<{
  (e: 'navigate', page: string, connectionId?: number): void
}>()

// ==================== 数据状态 ====================
const connections = ref<Connection[]>([])
const loading = ref(false)
const error = ref('')
const activeType = ref('') // '' = 全部

// 测试结果提示
const testResult = ref<{ success: boolean; message: string } | null>(null)

// 弹窗
const showModal = ref(false)
const isEdit = ref(false)
const form = ref<Record<string, any>>({})
const formError = ref('')
const testing = ref(false)

const showDeleteModal = ref(false)
const deleteTarget = ref<Connection | null>(null)
const deleteMsg = ref('')

// ==================== 类型映射 ====================
const typeTabs = [
  { value: '', label: '全部' },
  { value: 'ssh', label: 'SSH' },
  { value: 'redis', label: 'Redis' },
  { value: 'mysql', label: 'MySQL' },
  { value: 'tdengine', label: 'TAOS' },
]

const typeLabels: Record<string, string> = {
  ssh: 'SSH',
  redis: 'Redis',
  mysql: 'MySQL',
  tdengine: 'TAOS',
}

// 类型默认端口
const typeDefaultPort: Record<string, number> = {
  ssh: 22,
  redis: 6379,
  mysql: 3306,
  tdengine: 6041, // REST 接口（taosAdapter）
}

// ==================== 计算属性 ====================
const filteredConnections = computed(() => {
  if (!activeType.value) return connections.value
  return connections.value.filter((c) => c.type === activeType.value)
})

const stats = computed(() => {
  const total = connections.value.length
  return `共 ${total} 个连接`
})

function connDisplayName(c: Connection): string {
  if (c.type === 'ssh') {
    return c.username ? `${c.username}@${c.host}:${c.port}` : `${c.host}:${c.port}`
  }
  return `${c.host}:${c.port}`
}

function authDesc(c: Connection): string {
  if (c.type === 'ssh') {
    if (c.authMethod === 'key') return c.hasSecret ? '密钥登录' : '密钥（未配置）'
    return c.hasSecret ? '密码登录' : '密码（未配置）'
  }
  if (c.database) return `库: ${c.database}`
  return ''
}

// ==================== 加载 ====================
async function loadConnections() {
  loading.value = true
  try {
    const res = await ConnService.GetConnections({ token: props.token, type: activeType.value || '' })
    if (res.success) {
      connections.value = res.connections || []
    } else {
      error.value = res.message || '获取连接失败'
    }
  } catch (e) {
    console.error('获取连接失败', e)
    error.value = '获取连接失败'
  } finally {
    loading.value = false
  }
}

function selectType(t: string) {
  activeType.value = t
  loadConnections()
}

// ==================== 弹窗表单 ====================
function emptyForm(): Record<string, any> {
  return {
    id: 0,
    name: '',
    type: 'ssh',
    host: '',
    port: 22,
    username: '',
    authMethod: 'password',
    password: '',
    privateKey: '',
    privateKeyPath: '',
    passphrase: '',
    database: '',
    remark: '',
  }
}

// 切换连接类型时重置端口为类型默认值
function onTypeChange() {
  form.value.port = typeDefaultPort[form.value.type] || 22
}

function openAddModal() {
  isEdit.value = false
  form.value = emptyForm()
  formError.value = ''
  testResult.value = null
  showModal.value = true
}

function openEditModal(c: Connection) {
  isEdit.value = true
  form.value = {
    id: c.id,
    name: c.name,
    type: c.type,
    host: c.host,
    port: c.port || typeDefaultPort[c.type] || 22,
    username: c.username || '',
    authMethod: c.authMethod || 'password',
    password: '',
    privateKey: '',
    privateKeyPath: c.privateKeyPath || '',
    passphrase: '',
    database: c.database || '',
    remark: c.remark || '',
  }
  formError.value = ''
  testResult.value = null
  showModal.value = true
}

function validateForm(): string {
  if (!form.value.name.trim()) return '连接名称不能为空'
  if (!form.value.host.trim()) return '主机地址不能为空'
  if (form.value.type === 'ssh') {
    if (form.value.authMethod === 'password' && !form.value.password) {
      return '请填写登录密码（或选择密钥登录）'
    }
  }
  return ''
}

async function saveConnection() {
  const msg = validateForm()
  if (msg) {
    formError.value = msg
    return
  }
  const common = {
    name: form.value.name.trim(),
    type: form.value.type,
    host: form.value.host.trim(),
    port: Number(form.value.port) || typeDefaultPort[form.value.type],
    username: form.value.username.trim(),
    authMethod: form.value.type === 'ssh' ? form.value.authMethod : '',
    password: form.value.password,
    privateKey: form.value.privateKey,
    privateKeyPath: form.value.privateKeyPath.trim(),
    passphrase: form.value.passphrase,
    database: form.value.database.trim(),
    remark: form.value.remark.trim(),
  }
  try {
    const res = isEdit.value
      ? await ConnService.UpdateConnection({ token: props.token, id: form.value.id, ...common })
      : await ConnService.CreateConnection({ token: props.token, ...common })
    if (res.success) {
      showModal.value = false
      await loadConnections()
    } else {
      formError.value = res.message || '保存失败'
    }
  } catch (e) {
    formError.value = '保存失败'
    console.error(e)
  }
}

// ==================== 测试连接 ====================
async function testConnection() {
  testing.value = true
  testResult.value = null
  try {
    let res: any
    if (isEdit.value && form.value.id) {
      res = await ConnService.TestConnection({ token: props.token, id: form.value.id })
    } else {
      res = await ConnService.TestConnectionConfig({
        token: props.token,
        type: form.value.type,
        host: form.value.host.trim(),
        port: Number(form.value.port) || typeDefaultPort[form.value.type],
        username: form.value.username.trim(),
        authMethod: form.value.type === 'ssh' ? form.value.authMethod : '',
        password: form.value.password,
        privateKey: form.value.privateKey,
        privateKeyPath: form.value.privateKeyPath.trim(),
        passphrase: form.value.passphrase,
        database: form.value.database.trim(),
      })
    }
    if (res.success) {
      testResult.value = { success: true, message: `${res.message}（${res.latencyMs}ms）` }
    } else {
      testResult.value = { success: false, message: res.message || '连接失败' }
    }
  } catch (e) {
    testResult.value = { success: false, message: '测试异常' }
    console.error(e)
  } finally {
    testing.value = false
  }
}

async function testSavedConnection(c: Connection) {
  testResult.value = null
  try {
    const res = await ConnService.TestConnection({ token: props.token, id: c.id })
    testResult.value = res.success
      ? { success: true, message: `${c.name}: ${res.message}（${res.latencyMs}ms）` }
      : { success: false, message: `${c.name}: ${res.message}` }
  } catch (e) {
    testResult.value = { success: false, message: `${c.name}: 测试异常` }
    console.error(e)
  }
}

// ==================== 删除 ====================
function openDeleteModal(c: Connection) {
  deleteTarget.value = c
  deleteMsg.value = ''
  showDeleteModal.value = true
}

async function deleteConnection() {
  if (!deleteTarget.value) return
  const c = deleteTarget.value
  try {
    const res = await ConnService.DeleteConnection({ token: props.token, id: c.id })
    if (res.success) {
      showDeleteModal.value = false
      deleteTarget.value = null
      await loadConnections()
    } else {
      deleteMsg.value = res.message || '删除失败'
    }
  } catch (e) {
    deleteMsg.value = '删除失败'
    console.error(e)
  }
}

onMounted(() => {
  loadConnections()
})
</script>

<template>
  <div class="conn-page">
    <!-- ==================== 顶部工具栏 ==================== -->
    <div class="conn-toolbar">
      <div class="conn-tabs">
        <button
          v-for="tab in typeTabs"
          :key="tab.value"
          class="conn-tab"
          :class="{ 'conn-tab-active': activeType === tab.value }"
          @click="selectType(tab.value)"
        >{{ tab.label }}</button>
      </div>
      <div class="conn-toolbar-right">
        <span class="conn-stats">{{ stats }}</span>
        <button class="conn-add-btn" @click="openAddModal">
          <span class="conn-add-plus">+</span>
          <span>添加连接</span>
        </button>
      </div>
    </div>

    <!-- 测试结果提示 -->
    <div v-if="testResult" class="conn-test-toast" :class="testResult.success ? 'conn-test-ok' : 'conn-test-fail'">
      {{ testResult.message }}
      <button class="conn-test-close" @click="testResult = null">&times;</button>
    </div>

    <!-- ==================== 卡片网格 ==================== -->
    <div class="conn-grid">
      <div
        v-for="c in filteredConnections"
        :key="c.id"
        class="conn-card"
        :class="`conn-card-${c.type}`"
      >
        <div class="conn-card-head">
          <h3 class="conn-name">{{ c.name }}</h3>
          <span class="conn-type-badge" :class="`conn-badge-${c.type}`">{{ typeLabels[c.type] || c.type }}</span>
        </div>
        <div class="conn-addr">
          <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
            <rect x="2" y="4" width="20" height="16" rx="2" stroke="currentColor" stroke-width="2"/>
            <line x1="2" y1="9" x2="22" y2="9" stroke="currentColor" stroke-width="2"/>
          </svg>
          <span>{{ connDisplayName(c) }}</span>
        </div>
        <div class="conn-meta">
          <span v-if="authDesc(c)" class="conn-auth">{{ authDesc(c) }}</span>
          <span v-else class="conn-auth-muted">无凭据</span>
        </div>
        <p v-if="c.remark" class="conn-remark">{{ c.remark }}</p>
        <div class="conn-card-footer">
          <div class="conn-actions">
            <button v-if="c.type === 'ssh'" class="conn-action-btn" title="管理该连接的隧道（端口映射）" @click="emit('navigate', 'tunnels', c.id)">
              <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
                <path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
              <span>隧道</span>
            </button>
            <button class="conn-action-btn" title="测试连接" @click="testSavedConnection(c)">
              <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
                <path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/>
              </svg>
              <span>测试</span>
            </button>
            <button class="conn-action-btn" title="编辑连接" @click="openEditModal(c)">
              <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
                <path d="M11 4H4a2 2 0 00-2 2v14a2 2 0 002 2h14a2 2 0 002-2v-7" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                <path d="M18.5 2.5a2.121 2.121 0 013 3L12 15l-4 1 1-4 9.5-9.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </button>
            <button class="conn-action-btn conn-action-danger" title="删除连接" @click="openDeleteModal(c)">
              <svg viewBox="0 0 24 24" fill="none" width="13" height="13">
                <polyline points="3 6 5 6 21 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                <path d="M19 6v14a2 2 0 01-2 2H7a2 2 0 01-2-2V6m3 0V4a2 2 0 012-2h4a2 2 0 012 2v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
              </svg>
            </button>
          </div>
        </div>
      </div>

      <!-- 空状态 -->
      <div v-if="filteredConnections.length === 0 && !loading" class="conn-empty">
        <div class="conn-empty-icon">
          <svg viewBox="0 0 24 24" fill="none" width="44" height="44">
            <path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
            <path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        </div>
        <p class="conn-empty-text">暂无连接，点击「添加连接」创建 SSH 服务器、Redis / MySQL / TDengine 等连接信息</p>
      </div>

      <!-- 加载中 -->
      <div v-if="loading" class="conn-loading">
        <span class="conn-loading-spinner"></span>
        <span>正在加载...</span>
      </div>
    </div>

    <!-- ==================== 弹窗：添加/编辑连接 ==================== -->
    <div v-if="showModal" class="modal-overlay" @click.self="showModal = false">
      <div class="modal-box modal-box-md">
        <div class="modal-header">
          <h2 class="modal-title">{{ isEdit ? '编辑连接' : '添加连接' }}</h2>
          <button class="modal-close" @click="showModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <div class="form-group">
            <label class="form-label">连接名称 <span class="required">*</span></label>
            <input v-model="form.name" type="text" class="form-input" placeholder="如：生产服务器-01" />
          </div>

          <!-- 连接类型选择 -->
          <div class="form-group">
            <label class="form-label">连接类型 <span class="required">*</span></label>
            <div class="conn-type-selector">
              <label
                v-for="t in typeTabs.filter(t => t.value !== '')"
                :key="t.value"
                class="conn-type-option"
                :class="{ 'conn-type-option-active': form.type === t.value }"
              >
                <input v-model="form.type" type="radio" :value="t.value" class="conn-type-radio" @change="onTypeChange" />
                <span>{{ t.label }}</span>
              </label>
            </div>
          </div>

          <div class="form-row">
            <div class="form-group form-group-flex">
              <label class="form-label">主机地址 <span class="required">*</span></label>
              <input v-model="form.host" type="text" class="form-input" placeholder="10.0.0.1 或 host.example.com" />
            </div>
            <div class="form-group form-group-port">
              <label class="form-label">端口</label>
              <input v-model.number="form.port" type="number" class="form-input" />
            </div>
          </div>

          <!-- SSH 专属字段 -->
          <template v-if="form.type === 'ssh'">
            <div class="form-group">
              <label class="form-label">用户名 <span class="required">*</span></label>
              <input v-model="form.username" type="text" class="form-input" placeholder="root / openflashuser" />
            </div>
            <div class="form-group">
              <label class="form-label">认证方式</label>
              <div class="conn-auth-selector">
                <label class="conn-auth-option" :class="{ 'conn-auth-option-active': form.authMethod === 'password' }">
                  <input v-model="form.authMethod" type="radio" value="password" class="conn-type-radio" />
                  <span>账号/密码</span>
                </label>
                <label class="conn-auth-option" :class="{ 'conn-auth-option-active': form.authMethod === 'key' }">
                  <input v-model="form.authMethod" type="radio" value="key" class="conn-type-radio" />
                  <span>密钥（免密）</span>
                </label>
              </div>
            </div>
            <div v-if="form.authMethod === 'password'" class="form-group">
              <label class="form-label">
                密码 <span v-if="!isEdit" class="required">*</span>
                <span v-if="isEdit" class="form-hint-inline">（留空保持不变）</span>
              </label>
              <input v-model="form.password" type="password" class="form-input" placeholder="登录密码" />
            </div>
            <template v-else>
              <div class="form-group">
                <label class="form-label">私钥内容（PEM）</label>
                <textarea v-model="form.privateKey" class="form-textarea" rows="3" placeholder="-----BEGIN OPENSSH PRIVATE KEY-----&#10;...&#10;-----END OPENSSH PRIVATE KEY-----"></textarea>
              </div>
              <div class="form-group">
                <label class="form-label">或私钥文件路径</label>
                <input v-model="form.privateKeyPath" type="text" class="form-input" placeholder="C:\Users\xxx\.ssh\id_rsa（留空使用默认 ~/.ssh）" />
              </div>
              <div class="form-group">
                <label class="form-label">私钥口令（可选）</label>
                <input v-model="form.passphrase" type="password" class="form-input" placeholder="加密私钥的 passphrase" />
              </div>
            </template>
          </template>

          <!-- redis / mysql / tdengine 数据库专属字段 -->
          <template v-if="form.type !== 'ssh'">
            <div v-if="form.type !== 'redis'" class="form-group">
              <label class="form-label">用户名</label>
              <input v-model="form.username" type="text" class="form-input" :placeholder="form.type === 'mysql' ? 'root' : 'root'" />
            </div>
            <div class="form-group">
              <label class="form-label">密码</label>
              <input v-model="form.password" type="password" class="form-input" placeholder="连接密码" />
            </div>
            <div class="form-group">
              <label class="form-label">{{ form.type === 'redis' ? 'DB 索引' : '数据库名' }}</label>
              <input v-model="form.database" type="text" class="form-input" :placeholder="form.type === 'redis' ? '0' : '如：test_db'" />
            </div>
          </template>

          <div class="form-group">
            <label class="form-label">备注</label>
            <input v-model="form.remark" type="text" class="form-input" placeholder="用途说明，如：生产环境应用服务器" />
          </div>

          <!-- 测试结果 -->
          <div v-if="testResult" class="form-test-result" :class="testResult.success ? 'form-test-ok' : 'form-test-fail'">
            {{ testResult.message }}
          </div>
          <p v-if="formError" class="form-error">{{ formError }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" :disabled="testing" @click="showModal = false">取消</button>
          <button class="btn-secondary" :disabled="testing" @click="testConnection">
            {{ testing ? '测试中...' : '测试连接' }}
          </button>
          <button class="btn-primary" @click="saveConnection">{{ isEdit ? '保存修改' : '保存' }}</button>
        </div>
      </div>
    </div>

    <!-- ==================== 弹窗：删除确认 ==================== -->
    <div v-if="showDeleteModal" class="modal-overlay" @click.self="showDeleteModal = false">
      <div class="modal-box modal-box-sm">
        <div class="modal-header">
          <h2 class="modal-title modal-title-warning">
            <span class="warning-icon">!</span>
            删除连接
          </h2>
          <button class="modal-close" @click="showDeleteModal = false">&times;</button>
        </div>
        <div class="modal-body">
          <p class="confirm-text">确定删除连接「{{ deleteTarget?.name }}」吗？</p>
          <div class="confirm-warning-box">
            <span class="warning-icon-small">!</span>
            <span>删除后不可恢复；被命令引用的连接将无法删除。</span>
          </div>
          <p v-if="deleteMsg" class="form-error">{{ deleteMsg }}</p>
        </div>
        <div class="modal-footer">
          <button class="btn-cancel" @click="showDeleteModal = false">取消</button>
          <button class="btn-danger" @click="deleteConnection">确认删除</button>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped src="./connections-page.css"></style>
