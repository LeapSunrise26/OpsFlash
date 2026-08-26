<script setup lang="ts">
// Tools.vue 统一传入 username（顶部栏展示用），此处无需使用
defineProps<{ username?: string }>()

const emit = defineEmits<{
  (e: 'navigate', key: string): void
}>()
</script>

<template>
  <!-- 快捷入口 -->
  <section class="section">
    <h3 class="section-title">快捷入口</h3>
    <div class="shortcut-row">
      <div class="shortcut-card" @click="emit('navigate', 'ops')">
        <div class="shortcut-icon shortcut-icon-blue">
          <svg viewBox="0 0 24 24" fill="none"><rect x="2" y="3" width="20" height="6" rx="2" stroke="currentColor" stroke-width="2"/><rect x="2" y="15" width="20" height="6" rx="2" stroke="currentColor" stroke-width="2"/><line x1="6" y1="6" x2="6.01" y2="6" stroke="currentColor" stroke-width="2" stroke-linecap="round"/><line x1="6" y1="18" x2="6.01" y2="18" stroke="currentColor" stroke-width="2" stroke-linecap="round"/></svg>
        </div>
        <div class="shortcut-info">
          <span class="shortcut-title">指令库</span>
          <span class="shortcut-desc">命令管理、本地/SSH 执行与守护进程</span>
        </div>
        <svg class="shortcut-arrow" viewBox="0 0 24 24" fill="none"><polyline points="9 18 15 12 9 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </div>
      <div class="shortcut-card" @click="emit('navigate', 'connections')">
        <div class="shortcut-icon shortcut-icon-red">
          <svg viewBox="0 0 24 24" fill="none"><path d="M10 13a5 5 0 007.54.54l3-3a5 5 0 00-7.07-7.07l-1.72 1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><path d="M14 11a5 5 0 00-7.54-.54l-3 3a5 5 0 007.07 7.07l1.71-1.71" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
        <div class="shortcut-info">
          <span class="shortcut-title">连接管理</span>
          <span class="shortcut-desc">维护 SSH 连接与凭据</span>
        </div>
        <svg class="shortcut-arrow" viewBox="0 0 24 24" fill="none"><polyline points="9 18 15 12 9 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </div>
      <div class="shortcut-card" @click="emit('navigate', 'tunnels')">
        <div class="shortcut-icon shortcut-icon-purple">
          <svg viewBox="0 0 24 24" fill="none"><path d="M3 12h4l3-9 4 18 3-9h4" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
        <div class="shortcut-info">
          <span class="shortcut-title">隧道管理</span>
          <span class="shortcut-desc">SSH 端口转发一键启停</span>
        </div>
        <svg class="shortcut-arrow" viewBox="0 0 24 24" fill="none"><polyline points="9 18 15 12 9 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </div>
      <div class="shortcut-card" @click="emit('navigate', 'users')">
        <div class="shortcut-icon shortcut-icon-green">
          <svg viewBox="0 0 24 24" fill="none"><path d="M17 21v-2a4 4 0 00-4-4H5a4 4 0 00-4 4v2" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><circle cx="9" cy="7" r="4" stroke="currentColor" stroke-width="2"/><path d="M23 21v-2a4 4 0 00-3-3.87M16 3.13a4 4 0 010 7.75" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
        <div class="shortcut-info">
          <span class="shortcut-title">用户管理</span>
          <span class="shortcut-desc">账号、角色与密码管理</span>
        </div>
        <svg class="shortcut-arrow" viewBox="0 0 24 24" fill="none"><polyline points="9 18 15 12 9 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
      </div>
    </div>
  </section>

  <!-- 应用信息 -->
  <section class="section">
    <h3 class="section-title">应用信息</h3>
    <div class="app-info-card">
      <div class="info-item">
        <div class="info-icon info-icon-brand">
          <svg viewBox="0 0 24 24" fill="none"><path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/></svg>
        </div>
        <div class="info-text">
          <span class="info-label">应用名称</span>
          <span class="info-value">OpsFlash</span>
        </div>
      </div>
      <div class="info-item">
        <div class="info-icon info-icon-version">
          <svg viewBox="0 0 24 24" fill="none"><path d="M12 3a9 9 0 109 9h-9V3z" stroke="currentColor" stroke-width="2" stroke-linejoin="round"/><path d="M21 12a9 9 0 00-9-9v9h9z" fill="currentColor" opacity="0.25"/></svg>
        </div>
        <div class="info-text">
          <span class="info-label">版本</span>
          <span class="info-value">0.3.0</span>
        </div>
      </div>
      <div class="info-item">
        <div class="info-icon info-icon-stack">
          <svg viewBox="0 0 24 24" fill="none"><polyline points="16 18 22 12 16 6" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/><polyline points="8 6 2 12 8 18" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/></svg>
        </div>
        <div class="info-text">
          <span class="info-label">技术栈</span>
          <span class="info-value">Wails 3 · Vue 3 · TypeScript</span>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
/* ========== Section Layout ========== */
.section + .section {
  margin-top: 48px;
}
.section-title {
  font-size: 18px;
  font-weight: 700;
  color: rgba(255,255,255,0.85);
  margin: 0 0 20px 0;
  letter-spacing: 0.02em;
}

/* ========== Shortcut Cards ========== */
.shortcut-row {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 20px;
}
.shortcut-card {
  display: flex;
  align-items: center;
  gap: 18px;
  padding: 28px 24px;
  background: rgba(255,255,255,0.05);
  border: 1px solid rgba(255,255,255,0.10);
  border-radius: 14px;
  cursor: pointer;
  transition: all 0.25s ease;
}
.shortcut-card:hover {
  background: rgba(255,255,255,0.08);
  border-color: rgba(255,0,110,0.30);
  transform: translateY(-3px);
  box-shadow: 0 12px 32px rgba(0,0,0,0.30);
}
.shortcut-icon {
  width: 52px;
  height: 52px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.shortcut-icon svg {
  width: 26px;
  height: 26px;
}
.shortcut-icon-red   { background: rgba(255,0,110,0.14);   color: #FF4777; }
.shortcut-icon-purple { background: rgba(167,139,250,0.14); color: #B794F4; }
.shortcut-icon-green  { background: rgba(52,211,153,0.14);  color: #4ADE80; }
.shortcut-icon-blue   { background: rgba(96,165,250,0.14);  color: #60A5FA; }
.shortcut-info {
  display: flex;
  flex-direction: column;
  gap: 6px;
  flex: 1;
  min-width: 0;
}
.shortcut-title {
  font-size: 16px;
  font-weight: 600;
  color: #fff;
}
.shortcut-desc {
  font-size: 13px;
  color: rgba(255,255,255,0.45);
  line-height: 1.4;
}
.shortcut-arrow {
  width: 20px;
  height: 20px;
  color: rgba(255,255,255,0.25);
  flex-shrink: 0;
  transition: transform 0.25s ease, color 0.25s ease;
}
.shortcut-card:hover .shortcut-arrow {
  transform: translateX(5px);
  color: #FF77B0;
}

/* ========== App Info ========== */
.app-info-card {
  display: grid;
  grid-template-columns: repeat(3, 1fr);
  gap: 0;
  border: 1px solid rgba(255,255,255,0.10);
  border-radius: 14px;
  overflow: hidden;
}
.info-item {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 24px 28px;
  background: rgba(255,255,255,0.03);
  min-width: 0;
}
.info-item + .info-item {
  border-left: 1px solid rgba(255,255,255,0.08);
}
.info-icon {
  width: 44px;
  height: 44px;
  border-radius: 12px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
}
.info-icon svg {
  width: 22px;
  height: 22px;
}
.info-icon-brand   { background: rgba(255,0,110,0.14);   color: #FF4777; }
.info-icon-version { background: rgba(167,139,250,0.14); color: #B794F4; }
.info-icon-stack   { background: rgba(96,165,250,0.14);  color: #60A5FA; }
.info-text {
  display: flex;
  flex-direction: column;
  gap: 6px;
  min-width: 0;
}
.info-label {
  font-size: 13px;
  color: rgba(255,255,255,0.40);
  font-weight: 500;
}
.info-value {
  font-size: 16px;
  font-weight: 600;
  color: #fff;
  word-break: break-word;
}
</style>
