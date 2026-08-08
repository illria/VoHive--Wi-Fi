<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useAuthStore } from './stores/auth'
import LoadingScreen from './components/LoadingScreen.vue'

const route = useRoute()
const auth = useAuthStore()

const isDark = ref(localStorage.getItem('theme') === 'dark')

function toggleTheme() {
  isDark.value = !isDark.value
  const mode = isDark.value ? 'dark' : 'light'
  localStorage.setItem('theme', mode)
  updateHtmlClass(mode)
}

function updateHtmlClass(mode: 'dark' | 'light') {
  if (mode === 'dark') {
    document.documentElement.classList.add('dark')
  } else {
    document.documentElement.classList.remove('dark')
  }

  const themeMeta = document.querySelector('meta[name="theme-color"]')
  themeMeta?.setAttribute('content', mode === 'dark' ? '#000000' : '#fafafa')
}

onMounted(() => {
  if (isDark.value) {
    updateHtmlClass('dark')
  }
})

const AuthenticatedShell = defineAsyncComponent(() => import('./layouts/AuthenticatedShell.vue'))
const UnauthenticatedShell = defineAsyncComponent(() => import('./layouts/UnauthenticatedShell.vue'))
const shell = computed(() =>
  auth.isAuthenticated && route.name !== 'Login' ? AuthenticatedShell : UnauthenticatedShell
)
</script>

<template>
  <div class="h-screen w-screen overflow-hidden bg-[var(--vh-bg)] text-[var(--vh-text)] font-sans selection:bg-[var(--vh-text-strong)] selection:text-[var(--vh-bg)] transition-colors duration-300">
    <Suspense>
      <template #default>
        <component :is="shell" :is-dark="isDark" @toggle-theme="toggleTheme" />
      </template>
      <template #fallback>
        <LoadingScreen />
      </template>
    </Suspense>
  </div>
</template>

<style>
/* Custom Scrollbar */
::-webkit-scrollbar {
  width: 8px;
  height: 8px;
}
::-webkit-scrollbar-track {
  background: transparent;
}
::-webkit-scrollbar-thumb {
  background: var(--vh-border-strong);
  border: 2px solid var(--vh-bg);
  border-radius: 999px;
}
.dark ::-webkit-scrollbar-thumb {
  background: var(--vh-border-strong);
}
::-webkit-scrollbar-thumb:hover {
  background: var(--vh-text-subtle);
}
.dark ::-webkit-scrollbar-thumb:hover {
  background: var(--vh-text-subtle);
}
</style>
