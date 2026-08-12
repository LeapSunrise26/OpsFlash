<script setup lang="ts">
import { ref, watch } from 'vue'
import Tools from './components/Tools.vue'
import Login from './components/Login.vue'

const isLoggedIn = ref(false)
const currentUser = ref('')
const currentToken = ref('')

function handleLogin(username: string, token: string) {
  currentUser.value = username
  currentToken.value = token
  isLoggedIn.value = true
}

function handleLogout() {
  isLoggedIn.value = false
  currentUser.value = ''
  currentToken.value = ''
}

// 切换登录态时调整 body 样式
watch(isLoggedIn, (loggedIn) => {
  if (loggedIn) {
    document.body.classList.add('app-logged-in')
  } else {
    document.body.classList.remove('app-logged-in')
  }
}, { immediate: true })
</script>

<template>
  <Login v-if="!isLoggedIn" @login="handleLogin" />
  <Tools v-else :username="currentUser" :token="currentToken" @logout="handleLogout" />
</template>
