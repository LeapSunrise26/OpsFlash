<script setup lang="ts">
import { ref, watch } from 'vue'
import { OpsService } from '../../../../bindings/opsflash/server'
import { Command, Connection, Environment } from '../../../../bindings/opsflash/server/models'

// ==================== 命令表单弹窗（添加 / 编辑合并） ====================
// mode=create：新增命令；mode=edit：编辑 editTarget 指定的命令。
// 保存成功后 emit('saved')，由父级刷新列表并记录日志。

const props = defineProps<{
  token: string
  show: boolean
  mode: 'create' | 'edit'
  editTarget: Command | null
  environments: Environment[]
  connections: Connection[]
  activeEnvId: number
}>()

const emit = defineEmits(['close', 'saved'])

interface CommandForm {
  id: number
  name: string
  command: string
  remark: string
  type: 'non-interactive' | 'interactive' | 'daemon'
  interpreter: 'cmd' | 'powershell' | 'bash'
  sortOrder: number
  environmentId: number
  mode: 'terminal' | 'ssh' | 'redis' | 'mysql' | 'tdengine'
  connectionId: number
}

const form = ref<CommandForm>({
  id: 0,
  name: '',
  command: '',
  remark: '',
  type: 'non-interactive',
  interpreter: 'cmd',
  sortOrder: 10,
  environmentId: 0,
  mode: 'terminal',
  connectionId: 0,
})
const formError = ref('')

// 打开弹窗时按模式初始化表单
watch(
  () => [props.show, props.mode, props.editTarget] as const,
  () => {
    if (!props.show) return
    formError.value = ''
    if (props.mode === 'edit' && props.editTarget) {
      const cmd = props.editTarget
      form.value = {
        id: cmd.id,
        name: cmd.name,
        command: cmd.command,
        remark: cmd.remark,
        type: cmd.type as CommandForm['type'],
        interpreter:
          cmd.interpreter === 'powershell' || cmd.interpreter === 'bash'
            ? cmd.interpreter
            : 'cmd',
        sortOrder: cmd.sortOrder || 10,
        environmentId: cmd.environmentId,
        mode:
          cmd.mode === 'ssh' || cmd.mode === 'redis' || cmd.mode === 'mysql' || cmd.mode === 'tdengine'
            ? cmd.mode
            : 'terminal',
        connectionId: cmd.connectionId || 0,
      }
    } else {
      form.value = {
        id: 0,
        name: '',
        command: '',
        remark: '',
        type: 'non-interactive',
        interpreter: 'cmd',
        sortOrder: 10,
        environmentId: props.activeEnvId,
        mode: 'terminal',
        connectionId: 0,
      }
    }
  },
  { immediate: true },
)

function close() {
  emit('close')
}

// 当前表单模式对应的可用连接（ssh→ssh 连接、redis→redis 连接...）
function connForMode(mode: string) {
  return props.connections.filter((c) => c.type === mode)
}

async function submit() {
  const { name, command, remark, type, interpreter, sortOrder, environmentId, mode, connectionId } = form.value

  if (!name.trim()) {
    formError.value = '命令名称不能为空'
    return
  }
  if (!command.trim()) {
    formError.value = '命令本身不能为空'
    return
  }
  if (!environmentId) {
    formError.value = '请选择所属环境'
    return
  }
  if (mode !== 'terminal' && !connectionId) {
    formError.value = '请选择目标连接'
    return
  }

  try {
    if (props.mode === 'edit') {
      const res = await OpsService.UpdateCommand({
        token: props.token,
        id: form.value.id,
        name: name.trim(),
        command: command.trim(),
        remark: remark.trim(),
        type,
        interpreter: mode === 'terminal' ? interpreter : 'cmd',
        sortOrder: Number(sortOrder) || 10,
        environmentId,
        mode,
        connectionId,
      })
      if (res.success) {
        emit('saved', { name: name.trim(), type, action: '编辑命令', message: '命令修改成功' })
        close()
      } else {
        formError.value = res.message || '修改失败'
      }
    } else {
      const res = await OpsService.CreateCommand({
        token: props.token,
        name: name.trim(),
        command: command.trim(),
        remark: remark.trim(),
        type,
        interpreter: mode === 'terminal' ? interpreter : 'cmd',
        sortOrder: Number(sortOrder) || 10,
        environmentId,
        mode,
        connectionId,
      })
      if (res.success) {
        emit('saved', { name: name.trim(), type, action: '添加命令', message: `命令创建成功（${typeFullNames[type]}类型）` })
        close()
      } else {
        formError.value = res.message || '创建失败'
      }
    }
  } catch (e) {
    formError.value = props.mode === 'edit' ? '修改命令失败' : '创建命令失败'
    console.error(e)
  }
}

const typeFullNames: Record<string, string> = {
  'non-interactive': '非交互式',
  'interactive': '交互式',
  'daemon': '守护进程',
}
</script>

<template>
  <div v-if="show" class="modal-overlay" @click.self="close">
    <div class="modal-box modal-box-md">
      <div class="modal-header">
        <h2 class="modal-title">{{ mode === 'edit' ? '编辑命令' : '添加命令' }}</h2>
        <button class="modal-close" @click="close">&times;</button>
      </div>
      <div class="modal-body">
        <div class="form-group">
          <label class="form-label">命令名称 <span class="required">*</span></label>
          <input
            v-model="form.name"
            type="text"
            class="form-input"
            placeholder="如：查看磁盘占用"
          />
        </div>
        <div class="form-group">
          <label class="form-label">命令本身 <span class="required">*</span></label>
          <textarea
            v-model="form.command"
            class="form-textarea"
            placeholder="输入命令或脚本，支持多行"
            rows="8"
          ></textarea>
        </div>
        <!-- 执行模式选择 -->
        <div class="form-group">
          <label class="form-label">执行模式 <span class="required">*</span></label>
          <div class="type-selector">
            <label
              class="type-option"
              :class="{ 'type-option-active': form.mode === 'terminal' }"
            >
              <input v-model="form.mode" type="radio" value="terminal" class="type-radio" />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <polyline points="4 17 10 11 4 5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                    <line x1="12" y1="19" x2="20" y2="19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
                  </svg>
                  <span class="type-option-name">本地</span>
                </div>
                <p class="type-option-desc">在本机使用 cmd / bash 执行</p>
              </div>
            </label>
            <label
              class="type-option"
              :class="{ 'type-option-active': form.mode === 'ssh' }"
            >
              <input v-model="form.mode" type="radio" value="ssh" class="type-radio" />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <rect x="2" y="4" width="20" height="16" rx="2" stroke="currentColor" stroke-width="2"/>
                    <line x1="2" y1="9" x2="22" y2="9" stroke="currentColor" stroke-width="2"/>
                  </svg>
                  <span class="type-option-name">SSH</span>
                </div>
                <p class="type-option-desc">通过 SSH 连接远程主机执行命令</p>
              </div>
            </label>
            <label
              class="type-option"
              :class="{ 'type-option-active': form.mode === 'redis' }"
            >
              <input v-model="form.mode" type="radio" value="redis" class="type-radio" />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <circle cx="12" cy="12" r="3" stroke="currentColor" stroke-width="2"/>
                    <path d="M12 2v3m0 14v3M2 12h3m14 0h3M4.9 4.9l2.1 2.1m10 10l2.1 2.1m0-14.2l-2.1 2.1m-10 10l-2.1 2.1" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
                  </svg>
                  <span class="type-option-name">Redis</span>
                </div>
                <p class="type-option-desc">执行 Redis 指令，如 PING、GET key</p>
              </div>
            </label>
            <label
              class="type-option"
              :class="{ 'type-option-active': form.mode === 'mysql' }"
            >
              <input v-model="form.mode" type="radio" value="mysql" class="type-radio" />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <ellipse cx="12" cy="5" rx="8" ry="3" stroke="currentColor" stroke-width="2"/>
                    <path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5" stroke="currentColor" stroke-width="2"/>
                    <path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3" stroke="currentColor" stroke-width="2"/>
                  </svg>
                  <span class="type-option-name">MySQL</span>
                </div>
                <p class="type-option-desc">执行 SQL 语句，返回结果表格</p>
              </div>
            </label>
            <label
              class="type-option"
              :class="{ 'type-option-active': form.mode === 'tdengine' }"
            >
              <input v-model="form.mode" type="radio" value="tdengine" class="type-radio" />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <ellipse cx="12" cy="5" rx="8" ry="3" stroke="currentColor" stroke-width="2"/>
                    <path d="M4 5v14c0 1.66 3.58 3 8 3s8-1.34 8-3V5" stroke="currentColor" stroke-width="2"/>
                    <path d="M4 12c0 1.66 3.58 3 8 3s8-1.34 8-3" stroke="currentColor" stroke-width="2"/>
                    <path d="M4 8.5c0 1.66 3.58 3 8 3s8-1.34 8-3" stroke="currentColor" stroke-width="1" opacity="0.5"/>
                  </svg>
                  <span class="type-option-name">TAOS</span>
                </div>
                <p class="type-option-desc">执行时序 SQL，返回结果表格</p>
              </div>
            </label>
          </div>
        </div>

        <!-- 脚本类型（仅本地 terminal 模式生效） -->
        <div v-if="form.mode === 'terminal'" class="form-group">
          <label class="form-label">脚本类型</label>
          <div class="form-select-wrap">
            <select v-model="form.interpreter" class="form-select">
              <option value="cmd">CMD（命令提示符）</option>
              <option value="powershell">PowerShell</option>
              <option value="bash">Bash（需 Git Bash）</option>
            </select>
            <svg class="select-arrow" viewBox="0 0 24 24" fill="none">
              <polyline points="6 9 12 15 18 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </div>
          <p class="form-hint">多行：CMD 逐行执行；PowerShell / Bash 整体脚本执行</p>
        </div>

        <!-- 目标连接选择（非本地模式） -->
        <div v-if="form.mode !== 'terminal'" class="form-group">
          <label class="form-label">目标连接 <span class="required">*</span></label>
          <div class="form-select-wrap">
            <select v-model="form.connectionId" class="form-select">
              <option value="0" disabled>请选择目标连接</option>
              <option
                v-for="conn in connForMode(form.mode)"
                :key="conn.id"
                :value="conn.id"
              >{{ conn.name }}（{{ conn.type === 'ssh' ? conn.username + '@' : '' }}{{ conn.host }}:{{ conn.port }}{{ conn.database ? ' / ' + conn.database : '' }}）</option>
            </select>
            <svg class="select-arrow" viewBox="0 0 24 24" fill="none">
              <polyline points="6 9 12 15 18 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </div>
          <p v-if="connForMode(form.mode).length === 0" class="form-hint">暂无该类型连接，请先到「连接管理」创建</p>
        </div>

        <!-- 命令类型选择 -->
        <div class="form-group">
          <label class="form-label">命令类型 <span class="required">*</span></label>
          <div class="type-selector">
            <label
              class="type-option"
              :class="{ 'type-option-active': form.type === 'non-interactive' }"
            >
              <input
                v-model="form.type"
                type="radio"
                value="non-interactive"
                class="type-radio"
              />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <polyline points="4 17 10 11 4 5" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                    <line x1="12" y1="19" x2="20" y2="19" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
                  </svg>
                  <span class="type-option-name">非交互式</span>
                </div>
                <p class="type-option-desc">执行后捕获输出并展示在终端面板</p>
              </div>
            </label>
            <label
              class="type-option"
              :class="{ 'type-option-active': form.type === 'interactive' }"
            >
              <input
                v-model="form.type"
                type="radio"
                value="interactive"
                class="type-radio"
              />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <path d="M21 11.5a8.38 8.38 0 01-.9 3.8 8.5 8.5 0 01-7.6 4.7 8.38 8.38 0 01-3.8-.9L3 21l1.9-5.7a8.38 8.38 0 01-.9-3.8 8.5 8.5 0 014.7-7.6 8.38 8.38 0 013.8-.9h.5a8.48 8.48 0 018 8v.5z" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                  </svg>
                  <span class="type-option-name">交互式</span>
                </div>
                <p class="type-option-desc">运行中可在终端输入内容进行交互，如输入密码</p>
              </div>
            </label>
            <label
              class="type-option"
              :class="{ 'type-option-active': form.type === 'daemon' }"
            >
              <input
                v-model="form.type"
                type="radio"
                value="daemon"
                class="type-radio"
              />
              <div class="type-option-content">
                <div class="type-option-header">
                  <svg class="type-icon" viewBox="0 0 24 24" fill="none" width="18" height="18">
                    <path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
                  </svg>
                  <span class="type-option-name">守护进程</span>
                </div>
                <p class="type-option-desc">后台持续运行，可启动/停止，适合 SSH 隧道等</p>
              </div>
            </label>
          </div>
        </div>

        <div class="form-group">
          <label class="form-label">排序值</label>
          <input
            v-model.number="form.sortOrder"
            type="number"
            class="form-input form-input-short"
            placeholder="10"
          />
          <p class="form-hint">数值越大，命令在列表中越靠前（默认 10）</p>
        </div>

        <div class="form-group">
          <label class="form-label">所属环境 <span class="required">*</span></label>
          <div class="form-select-wrap">
            <select v-model="form.environmentId" class="form-select">
              <option value="0" disabled>请选择环境</option>
              <option
                v-for="env in environments"
                :key="env.id"
                :value="env.id"
              >{{ env.name }}</option>
            </select>
            <svg class="select-arrow" viewBox="0 0 24 24" fill="none">
              <polyline points="6 9 12 15 18 9" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            </svg>
          </div>
        </div>
        <!-- 备注 -->
        <div class="form-group">
          <label class="form-label">备注</label>
          <input
            v-model="form.remark"
            type="text"
            class="form-input"
            placeholder="启动开发环境的服务"
          />
        </div>
        <p v-if="formError" class="form-error">{{ formError }}</p>
      </div>
      <div class="modal-footer">
        <button class="btn-cancel" @click="close">取消</button>
        <button class="btn-primary" @click="submit">{{ mode === 'edit' ? '保存修改' : '保存命令' }}</button>
      </div>
    </div>
  </div>
</template>

<style scoped src="./form-modals.css"></style>
