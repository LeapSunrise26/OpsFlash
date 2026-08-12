<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { TunnelService } from '../../../../bindings/opsflash/server'
import { Tunnel, Connection } from '../../../../bindings/opsflash/server/models'

// 新建/编辑隧道弹窗：支持单个（local/remote/dynamic）与批量映射两种模式
const props = defineProps<{
  token: string
  sshConnections: Connection[]
  show: boolean
  mode: 'single' | 'batch'
  isEdit: boolean
  tunnel: Tunnel | null
}>()

const emit = defineEmits<{
  (e: 'close'): void
  (e: 'saved', message: string): void
}>()

// ==================== 模式与表单状态 ====================
const modalMode = ref<'single' | 'batch'>(props.mode)
watch(() => props.mode, (v) => { modalMode.value = v })

const form = ref<Record<string, any>>({})
const formError = ref('')
const batchSaving = ref(false)

function emptyForm(): Record<string, any> {
  return {
    id: 0,
    name: '',
    connectionId: 0,
    type: 'local',
    localHost: '127.0.0.1',
    localPort: 13306,
    remoteHost: '127.0.0.1',
    remotePort: 3306,
    groupName: '',
    autoStart: false,
    autoReconnect: true,
    remark: '',
  }
}

// ==================== 类型映射 ====================
const typeLabels: Record<string, string> = {
  local: '本地转发',
  remote: '远程转发',
  dynamic: '动态转发',
}

const typeCmds: Record<string, string> = {
  local: 'ssh -L',
  remote: 'ssh -R',
  dynamic: 'ssh -D',
}

// 快速模板（单个模式）
const templates = [
  { label: 'MySQL', remotePort: 3306, localPort: 13306, remoteHost: '127.0.0.1' },
  { label: 'Redis', remotePort: 6379, localPort: 16379, remoteHost: '127.0.0.1' },
  { label: 'PostgreSQL', remotePort: 5432, localPort: 15432, remoteHost: '127.0.0.1' },
  { label: 'HTTP', remotePort: 80, localPort: 18080, remoteHost: '127.0.0.1' },
  { label: 'HTTPS', remotePort: 443, localPort: 18443, remoteHost: '127.0.0.1' },
]

// 表单字段标签（按类型动态显示语义）
const localPortLabel = computed(() => {
  if (form.value.type === 'remote') return '本地目标地址'
  if (form.value.type === 'dynamic') return '本地代理地址'
  return '本地监听地址'
})

const remotePortLabel = computed(() => {
  if (form.value.type === 'remote') return '远程监听地址'
  return '远程目标地址'
})

// ==================== 批量状态 ====================
interface BatchTemplate {
  key: string
  label: string
  localPort: number
  remoteHost: string
  remotePort: number
  checked: boolean
}

const batchTemplates = ref<BatchTemplate[]>([])
const batchConnectionId = ref(0)
const batchGroupName = ref('')
const batchStartAll = ref(true)
const batchAutoStart = ref(false)
const batchAutoReconnect = ref(true)
const customBatchItems = ref<{ name: string; localPort: number; remoteHost: string; remotePort: number }[]>([])

// 打开弹窗时初始化状态
watch(() => props.show, (show) => {
  if (!show) return
  modalMode.value = props.mode
  formError.value = ''

  if (props.isEdit && props.tunnel) {
    // 编辑：始终单个模式
    modalMode.value = 'single'
    const t = props.tunnel
    form.value = {
      id: t.id,
      name: t.name,
      connectionId: t.connectionId,
      type: t.type,
      localHost: t.localHost || '127.0.0.1',
      localPort: t.localPort,
      remoteHost: t.remoteHost || '127.0.0.1',
      remotePort: t.remotePort,
      groupName: t.groupName || '',
      autoStart: t.autoStart,
      autoReconnect: t.autoReconnect,
      remark: t.remark || '',
    }
    return
  }

  // 新建
  form.value = emptyForm()
  if (props.sshConnections.length === 1) {
    form.value.connectionId = props.sshConnections[0].id
  }

  // 批量初始化
  batchTemplates.value = [
    { key: 'mysql', label: 'MySQL', localPort: 13306, remoteHost: '127.0.0.1', remotePort: 3306, checked: false },
    { key: 'redis', label: 'Redis', localPort: 16379, remoteHost: '127.0.0.1', remotePort: 6379, checked: false },
    { key: 'pg', label: 'PostgreSQL', localPort: 15432, remoteHost: '127.0.0.1', remotePort: 5432, checked: false },
    { key: 'http', label: 'HTTP', localPort: 18080, remoteHost: '127.0.0.1', remotePort: 80, checked: false },
    { key: 'https', label: 'HTTPS', localPort: 18443, remoteHost: '127.0.0.1', remotePort: 443, checked: false },
  ]
  customBatchItems.value = []
  const conn = props.sshConnections.length === 1 ? props.sshConnections[0] : null
  batchConnectionId.value = conn ? conn.id : 0
  // 分组默认取连接名，让该服务器的所有转发自动成组，可一键全启
  batchGroupName.value = conn ? conn.name : ''
  batchStartAll.value = true
  batchAutoStart.value = false
  batchAutoReconnect.value = true
})

// ==================== 单个模式 ====================
function applyTemplate(tpl: typeof templates[0]) {
  form.value.remotePort = tpl.remotePort
  form.value.localPort = tpl.localPort
  form.value.remoteHost = tpl.remoteHost
}

// 切换类型时的字段清理
function handleTypeChange() {
  if (form.value.type === 'dynamic') {
    form.value.remoteHost = '127.0.0.1'
    form.value.remotePort = 0
  }
  if (form.value.type === 'remote') {
    if (!form.value.remoteHost || form.value.remoteHost === '127.0.0.1') {
      form.value.remoteHost = '0.0.0.0'
    }
  }
}

function validateSingleForm(): string {
  if (!form.value.name.trim()) return '隧道名称不能为空'
  if (!form.value.connectionId) return '必须选择 SSH 连接'
  if (!form.value.localPort || form.value.localPort <= 0 || form.value.localPort > 65535) {
    return '本地端口无效（1-65535）'
  }
  if (form.value.type !== 'dynamic') {
    if (!form.value.remotePort || form.value.remotePort <= 0 || form.value.remotePort > 65535) {
      return '远程端口无效（1-65535）'
    }
  }
  return ''
}

async function saveTunnel() {
  const msg = validateSingleForm()
  if (msg) {
    formError.value = msg
    return
  }
  const common = {
    name: form.value.name.trim(),
    connectionId: Number(form.value.connectionId),
    type: form.value.type,
    localHost: form.value.localHost.trim() || '127.0.0.1',
    localPort: Number(form.value.localPort),
    remoteHost: form.value.remoteHost.trim() || '127.0.0.1',
    remotePort: Number(form.value.remotePort),
    groupName: (form.value.groupName || '').trim(),
    autoStart: !!form.value.autoStart,
    autoReconnect: !!form.value.autoReconnect,
    remark: form.value.remark.trim(),
  }
  try {
    const res = props.isEdit
      ? await TunnelService.UpdateTunnel({ token: props.token, id: form.value.id, ...common })
      : await TunnelService.CreateTunnel({ token: props.token, ...common })
    if (res.success) {
      emit('saved', res.message || '保存成功')
    } else {
      formError.value = res.message || '保存失败'
    }
  } catch (e) {
    formError.value = '保存失败'
    console.error(e)
  }
}

// ==================== 批量模式 ====================
function toggleBatchTemplate(t: BatchTemplate) {
  t.checked = !t.checked
}

function addCustomItem() {
  customBatchItems.value.push({ name: '', localPort: 0, remoteHost: '127.0.0.1', remotePort: 0 })
}

function removeCustomItem(idx: number) {
  customBatchItems.value.splice(idx, 1)
}

function selectedBatchItems(): { name: string; type: string; localHost: string; localPort: number; remoteHost: string; remotePort: number }[] {
  const conn = props.sshConnections.find(c => c.id === batchConnectionId.value)
  const prefix = conn ? conn.name.replace(/[^\w\u4e00-\u9fa5-]/g, '-') : 'tunnel'
  const items: { name: string; type: string; localHost: string; localPort: number; remoteHost: string; remotePort: number }[] = []

  for (const t of batchTemplates.value) {
    if (t.checked) {
      items.push({
        name: `${prefix}-${t.label}`,
        type: 'local',
        localHost: '127.0.0.1',
        localPort: Number(t.localPort),
        remoteHost: t.remoteHost,
        remotePort: Number(t.remotePort),
      })
    }
  }
  for (const c of customBatchItems.value) {
    const name = c.name.trim()
    if (!name) continue
    items.push({
      name: `${prefix}-${name}`,
      type: 'local',
      localHost: '127.0.0.1',
      localPort: Number(c.localPort),
      remoteHost: c.remoteHost.trim() || '127.0.0.1',
      remotePort: Number(c.remotePort),
    })
  }
  return items
}

async function saveBatch() {
  const items = selectedBatchItems()
  if (!batchConnectionId.value) {
    formError.value = '必须选择 SSH 连接'
    return
  }
  if (items.length === 0) {
    formError.value = '请至少勾选一个服务模板或添加自定义映射'
    return
  }
  const seen = new Set<number>()
  for (const it of items) {
    if (seen.has(it.localPort)) {
      formError.value = `本地端口 ${it.localPort} 重复，请调整`
      return
    }
    if (it.localPort <= 0 || it.localPort > 65535) {
      formError.value = `端口 ${it.localPort} 无效（1-65535）`
      return
    }
    seen.add(it.localPort)
  }

  batchSaving.value = true
  formError.value = ''
  try {
    const res = await TunnelService.CreateTunnels({
      token: props.token,
      connectionId: batchConnectionId.value,
      tunnels: items.map(it => ({
        name: it.name,
        type: it.type,
        localHost: it.localHost,
        localPort: it.localPort,
        remoteHost: it.remoteHost,
        remotePort: it.remotePort,
        groupName: batchGroupName.value.trim(),
        autoStart: batchAutoStart.value,
        autoReconnect: batchAutoReconnect.value,
        remark: `批量创建（${props.sshConnections.find(c => c.id === batchConnectionId.value)?.name || ''}）`,
      })),
      startAll: batchStartAll.value,
    })
    if (res.success) {
      const failedCount = (res.failed || []).length
      const msg = failedCount > 0
        ? `${res.message}：${res.failed![0].name} - ${res.failed![0].message}`
        : res.message
      emit('saved', msg)
    } else {
      formError.value = res.message || '批量创建失败'
    }
  } catch (e) {
    formError.value = '批量创建失败'
    console.error(e)
  } finally {
    batchSaving.value = false
  }
}
</script>

<template>
  <div v-if="show" class="modal-overlay" @click.self="emit('close')">
    <div class="modal-box" :class="modalMode === 'batch' ? 'modal-box-lg' : 'modal-box-md'">
      <div class="modal-header">
        <h2 class="modal-title">{{ isEdit ? '编辑隧道' : '新建隧道' }}</h2>
        <button class="modal-close" @click="emit('close')">&times;</button>
      </div>

      <!-- 模式切换 Tab（仅新建时显示） -->
      <div v-if="!isEdit" class="tunnel-modal-tabs">
        <button
          class="tunnel-modal-tab"
          :class="{ 'tunnel-modal-tab-active': modalMode === 'single' }"
          @click="modalMode = 'single'"
        >单个隧道</button>
        <button
          class="tunnel-modal-tab"
          :class="{ 'tunnel-modal-tab-active': modalMode === 'batch' }"
          @click="modalMode = 'batch'"
        >
          <svg viewBox="0 0 24 24" fill="none" width="12" height="12"><path d="M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
          批量映射
        </button>
      </div>

      <div class="modal-body">
        <!-- ========== 单个模式表单 ========== -->
        <template v-if="modalMode === 'single'">
          <div class="form-group">
            <label class="form-label">名称 <span class="required">*</span></label>
            <input v-model="form.name" type="text" class="form-input" placeholder="如：MySQL隧道" />
          </div>

          <div class="form-group">
            <label class="form-label">SSH 连接 <span class="required">*</span></label>
            <select v-model="form.connectionId" class="form-select">
              <option :value="0" disabled>请选择 SSH 连接</option>
              <option v-for="c in sshConnections" :key="c.id" :value="c.id">
                {{ c.name }} ({{ c.host }}:{{ c.port }})
              </option>
            </select>
            <p v-if="sshConnections.length === 0" class="form-hint">请先在「连接管理」中创建 SSH 连接</p>
          </div>

          <div class="form-group">
            <label class="form-label">类型 <span class="required">*</span></label>
            <div class="tunnel-type-selector">
              <label
                v-for="(label, key) in typeLabels"
                :key="key"
                class="tunnel-type-option"
                :class="{ 'tunnel-type-option-active': form.type === key }"
              >
                <input v-model="form.type" type="radio" :value="key" class="conn-type-radio" @change="handleTypeChange" />
                <span>{{ label }}</span>
                <span class="tunnel-type-cmd">{{ typeCmds[key] }}</span>
              </label>
            </div>
          </div>

          <!-- 快速模板 -->
          <div v-if="form.type === 'local'" class="form-group">
            <label class="form-label">快速模板</label>
            <div class="tunnel-templates">
              <button
                v-for="tpl in templates"
                :key="tpl.label"
                type="button"
                class="tunnel-template-btn"
                @click="applyTemplate(tpl)"
              >{{ tpl.label }} {{ tpl.remotePort }}</button>
            </div>
          </div>

          <!-- 本地监听/目标 -->
          <div class="form-row">
            <div class="form-group form-group-flex">
              <label class="form-label">{{ localPortLabel }}</label>
              <input v-model="form.localHost" type="text" class="form-input" placeholder="127.0.0.1" />
            </div>
            <div class="form-group form-group-port">
              <label class="form-label">{{ form.type === 'remote' ? '本地目标端口' : '端口' }}</label>
              <input v-model.number="form.localPort" type="number" class="form-input" />
            </div>
          </div>

          <!-- 远程目标 -->
          <div v-if="form.type !== 'dynamic'" class="form-row">
            <div class="form-group form-group-flex">
              <label class="form-label">{{ remotePortLabel }}</label>
              <input v-model="form.remoteHost" type="text" class="form-input" placeholder="127.0.0.1" />
            </div>
            <div class="form-group form-group-port">
              <label class="form-label">{{ form.type === 'remote' ? '远程监听端口' : '端口' }}</label>
              <input v-model.number="form.remotePort" type="number" class="form-input" />
            </div>
          </div>

          <!-- dynamic 提示 -->
          <p v-if="form.type === 'dynamic'" class="form-hint">动态转发：本地 SOCKS5 代理，通过 SSH 动态访问任意目标（如浏览器代理设置）</p>

          <!-- 分组 -->
          <div class="form-group">
            <label class="form-label">分组（可选）</label>
            <input v-model="form.groupName" type="text" class="form-input" placeholder="如：生产环境；同组隧道可一键全部启动/停止" />
          </div>

          <!-- 选项 -->
          <div class="form-group">
            <label class="tunnel-checkbox-label">
              <input v-model="form.autoReconnect" type="checkbox" class="tunnel-checkbox" />
              <span>自动重连（断线后自动恢复，指数退避重试）</span>
            </label>
          </div>
          <div class="form-group">
            <label class="tunnel-checkbox-label">
              <input v-model="form.autoStart" type="checkbox" class="tunnel-checkbox" />
              <span>应用启动时自动启动</span>
            </label>
          </div>

          <div class="form-group">
            <label class="form-label">备注</label>
            <input v-model="form.remark" type="text" class="form-input" placeholder="用途说明" />
          </div>
        </template>

        <!-- ========== 批量模式表单 ========== -->
        <template v-else>
          <div class="form-group">
            <label class="form-label">SSH 连接 <span class="required">*</span></label>
            <select v-model="batchConnectionId" class="form-select">
              <option :value="0" disabled>请选择 SSH 连接</option>
              <option v-for="c in sshConnections" :key="c.id" :value="c.id">
                {{ c.name }} ({{ c.host }}:{{ c.port }})
              </option>
            </select>
            <p v-if="sshConnections.length === 0" class="form-hint">请先在「连接管理」中创建 SSH 连接</p>
          </div>

          <!-- 分组 -->
          <div class="form-group">
            <label class="form-label">分组 <span class="required">*</span></label>
            <input v-model="batchGroupName" type="text" class="form-input" placeholder="分组名（默认 SSH 连接名）" />
            <p class="form-hint">同组隧道可在列表按组一键「全部启动 / 全部停止」</p>
          </div>

          <!-- 服务模板 -->
          <div class="form-group">
            <label class="form-label">选择要映射的服务（本地端口 → 远程端口）</label>
            <p class="form-hint">模板远程主机默认 127.0.0.1（SSH 服务器本机）；如需映射内网其他机器，请修改模板/自定义映射的远程主机为对应内网 IP</p>
            <div class="tunnel-batch-templates">
              <label
                v-for="t in batchTemplates"
                :key="t.key"
                class="tunnel-batch-tpl"
                :class="{ 'tunnel-batch-tpl-on': t.checked }"
              >
                <input v-model="t.checked" type="checkbox" class="tunnel-checkbox" />
                <span class="tunnel-batch-tpl-name">{{ t.label }}</span>
                <span class="tunnel-batch-tpl-ports">
                  <input v-model.number="t.localPort" type="number" class="tunnel-batch-port" title="本地端口" @click.stop />
                  <span class="tunnel-batch-arrow">→</span>
                  <input v-model.number="t.remotePort" type="number" class="tunnel-batch-port" title="远程端口" @click.stop />
                </span>
              </label>
            </div>
          </div>

          <!-- 自定义映射 -->
          <div class="form-group">
            <label class="form-label">自定义映射（可选）</label>
            <div v-for="(item, idx) in customBatchItems" :key="idx" class="tunnel-batch-custom">
              <input v-model="item.name" type="text" class="form-input tunnel-batch-custom-name" placeholder="名称" />
              <input v-model.number="item.localPort" type="number" class="tunnel-batch-port" placeholder="本地端口" />
              <input v-model="item.remoteHost" type="text" class="form-input tunnel-batch-custom-host" placeholder="远程主机" />
              <input v-model.number="item.remotePort" type="number" class="tunnel-batch-port" placeholder="远程端口" />
              <button type="button" class="tunnel-batch-remove" title="移除" @click="removeCustomItem(idx)">&times;</button>
            </div>
            <button type="button" class="tunnel-batch-add" @click="addCustomItem">+ 添加自定义映射</button>
          </div>

          <!-- 预览 -->
          <div v-if="selectedBatchItems().length" class="form-group">
            <label class="form-label">将创建 {{ selectedBatchItems().length }} 条</label>
            <div class="tunnel-batch-preview">
              <div v-for="it in selectedBatchItems()" :key="it.name + it.localPort" class="tunnel-batch-preview-item">
                <span class="tunnel-batch-preview-name">{{ it.name }}</span>
                <span class="tunnel-batch-preview-rule">{{ it.localHost }}:{{ it.localPort }} → {{ it.remoteHost }}:{{ it.remotePort }}</span>
              </div>
            </div>
          </div>

          <!-- 选项 -->
          <div class="form-group">
            <label class="tunnel-checkbox-label">
              <input v-model="batchStartAll" type="checkbox" class="tunnel-checkbox" />
              <span>创建后立即启动</span>
            </label>
          </div>
          <div class="form-group">
            <label class="tunnel-checkbox-label">
              <input v-model="batchAutoStart" type="checkbox" class="tunnel-checkbox" />
              <span>应用启动时自动启动</span>
            </label>
          </div>
          <div class="form-group">
            <label class="tunnel-checkbox-label">
              <input v-model="batchAutoReconnect" type="checkbox" class="tunnel-checkbox" />
              <span>自动重连</span>
            </label>
          </div>
        </template>

        <p v-if="formError" class="form-error">{{ formError }}</p>
      </div>
      <div class="modal-footer">
        <button class="btn-cancel" @click="emit('close')">取消</button>
        <button v-if="modalMode === 'single'" class="btn-primary" @click="saveTunnel">{{ isEdit ? '保存修改' : '保存' }}</button>
        <button v-else class="btn-primary" :disabled="batchSaving" @click="saveBatch">
          {{ batchSaving ? '创建中...' : batchStartAll ? '生成并启动' : '生成' }}
        </button>
      </div>
    </div>
  </div>
</template>

<style scoped src="./tunnel-form.css"></style>
