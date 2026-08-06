<script setup lang="ts">
import { computed, defineAsyncComponent, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter, useRoute } from 'vue-router'
import { useAuthStore } from '../stores/auth'
import { Expand, Fold } from '@element-plus/icons-vue'
import LoadingScreen from '../components/LoadingScreen.vue'
import ErrorBoundary from '../components/ErrorBoundary.vue'
import SwitchDark from '../components/SwitchDark.vue'
import { debugCollector } from '../debug/collector'
import {
  Mail24Regular,
  Settings24Regular,
  SignOut24Regular,
  Board24Regular,
  Phone24Regular,
  Globe24Regular,
  DocumentText24Regular
} from '@vicons/fluent'

defineProps({
  isDark: {
    type: Boolean,
    required: true
  }
})

const emit = defineEmits(['toggle-theme'])

const router = useRouter()
const route = useRoute()
const auth = useAuthStore()
const collapsed = ref(false)
const isMobile = ref(false)
const drawerOpen = ref(false)
const debugOpen = ref(false)
const DebugPanel = defineAsyncComponent(() => import('../components/DebugPanel.vue'))

const menuItems = [
  { index: '/', label: '仪表盘', icon: Board24Regular },
  { index: '/devices', label: '设备管理', icon: Phone24Regular },
  { index: '/proxy', label: '代理管理', icon: Globe24Regular },
  { index: '/sms', label: '短信中心', icon: Mail24Regular },
  { index: '/logs', label: '实时日志', icon: DocumentText24Regular },
  { index: '/settings', label: '系统设置', icon: Settings24Regular }
]

async function handleLogout() {
  const { ElMessageBox } = await import('element-plus')
  const confirmed = await ElMessageBox.confirm('确认退出登录？', '提示', {
    confirmButtonText: '退出',
    cancelButtonText: '取消',
    type: 'warning'
  })
    .then(() => true)
    .catch(() => false)
  if (!confirmed) return
  auth.logout()
  router.push('/login')
}

function syncIsMobile() {
  if (typeof window === 'undefined') return
  isMobile.value = window.matchMedia('(max-width: 767px)').matches
  if (!isMobile.value) {
    drawerOpen.value = false
  }
}

function handleNavToggle() {
  if (isMobile.value) {
    drawerOpen.value = true
  } else {
    collapsed.value = !collapsed.value
  }
}

function onKeydown(e: KeyboardEvent) {
  if (e.ctrlKey && e.shiftKey && String(e.key || '').toLowerCase() === 'd') {
    e.preventDefault()
    debugOpen.value = !debugOpen.value
    localStorage.setItem('debug_panel_open', debugOpen.value ? '1' : '0')
  }
}

onMounted(() => {
  syncIsMobile()
  window.addEventListener('resize', syncIsMobile, { passive: true })

  const saved = localStorage.getItem('debug_panel_open')
  debugOpen.value = saved === '1'

  window.addEventListener('keydown', onKeydown)
})

onUnmounted(() => {
  window.removeEventListener('resize', syncIsMobile)
  window.removeEventListener('keydown', onKeydown)
})

watch(
  () => route.fullPath,
  () => {
    drawerOpen.value = false
  }
)

watch(
  () => debugOpen.value,
  (v) => {
    localStorage.setItem('debug_panel_open', v ? '1' : '0')
  }
)

watch(
  () => debugCollector.openPanelRequestAt.value,
  (ts) => {
    if (!ts) return
    debugOpen.value = true
  }
)

const activePath = computed(() => route.path)
const currentPage = computed(
  () =>
    menuItems.find((item) => {
      if (item.index === '/') return activePath.value === '/'
      return activePath.value === item.index || activePath.value.startsWith(`${item.index}/`)
    }) || menuItems[0]
)

function isNavActive(index: string) {
  if (index === '/') return activePath.value === '/'
  return activePath.value === index || activePath.value.startsWith(`${index}/`)
}
</script>

<template>
  <div v-if="auth.isAuthenticated && route.name !== 'Login'" class="app-shell">
    <aside v-if="!isMobile" class="app-sidebar" :class="{ 'is-collapsed': collapsed }">
      <div class="app-brand" :class="{ 'is-collapsed': collapsed }">
        <span class="app-brand-mark" aria-hidden="true">V</span>
        <span v-if="!collapsed" class="app-brand-name">VoHive</span>
      </div>

      <div v-if="!collapsed" class="app-nav-label-heading">Workspace</div>
      <nav class="app-nav" aria-label="主导航">
        <RouterLink
          v-for="item in menuItems"
          :key="item.index"
          :to="item.index"
          class="app-nav-item"
          :class="{ 'is-active': isNavActive(item.index) }"
          :title="collapsed ? item.label : undefined"
        >
          <span class="app-nav-icon" aria-hidden="true"><component :is="item.icon" /></span>
          <span v-if="!collapsed" class="app-nav-text">{{ item.label }}</span>
        </RouterLink>
      </nav>

      <div v-if="!collapsed" class="app-sidebar-footer">
        <div class="app-system-status"><span class="app-status-dot" aria-hidden="true"></span>系统在线</div>
        <div class="app-user-row">
          <div class="app-user-avatar" aria-hidden="true"><Settings24Regular /></div>
          <div class="app-user-copy">
            <strong>Admin</strong>
            <span>Administrator</span>
          </div>
          <button type="button" class="app-logout" aria-label="退出登录" title="退出登录" @click="handleLogout">
            <SignOut24Regular aria-hidden="true" />
          </button>
        </div>
      </div>
    </aside>

    <el-drawer v-model="drawerOpen" direction="ltr" size="272px" :with-header="false" class="mobile-drawer">
      <div class="mobile-nav-shell">
        <div class="app-brand">
          <span class="app-brand-mark" aria-hidden="true">V</span>
          <span class="app-brand-name">VoHive</span>
        </div>
        <div class="app-nav-label-heading">Workspace</div>
        <nav class="app-nav" aria-label="主导航">
          <RouterLink
            v-for="item in menuItems"
            :key="item.index"
            :to="item.index"
            class="app-nav-item"
            :class="{ 'is-active': isNavActive(item.index) }"
            @click="drawerOpen = false"
          >
            <span class="app-nav-icon" aria-hidden="true"><component :is="item.icon" /></span>
            <span class="app-nav-text">{{ item.label }}</span>
          </RouterLink>
        </nav>
        <div class="app-sidebar-footer">
          <div class="app-system-status"><span class="app-status-dot" aria-hidden="true"></span>系统在线</div>
          <div class="app-user-row">
            <div class="app-user-avatar" aria-hidden="true"><Settings24Regular /></div>
            <div class="app-user-copy">
              <strong>Admin</strong>
              <span>Administrator</span>
            </div>
            <button type="button" class="app-logout" aria-label="退出登录" title="退出登录" @click="handleLogout">
              <SignOut24Regular aria-hidden="true" />
            </button>
          </div>
        </div>
      </div>
    </el-drawer>

    <div class="app-main">
      <header class="app-topbar">
        <div class="app-topbar-left">
          <button type="button" class="app-nav-toggle" :aria-label="isMobile ? '打开导航' : collapsed ? '展开导航' : '收起导航'" @click="handleNavToggle">
            <Fold v-if="!isMobile && !collapsed" aria-hidden="true" />
            <Expand v-else aria-hidden="true" />
          </button>
          <div class="app-breadcrumb" aria-label="当前位置">
            <span>VoHive</span>
            <span class="app-breadcrumb-separator" aria-hidden="true">/</span>
            <strong>{{ currentPage.label }}</strong>
          </div>
        </div>

        <div class="app-topbar-right">
          <span class="app-topbar-status"><span class="app-status-dot" aria-hidden="true"></span><span class="hidden sm:inline">服务运行正常</span></span>
          <SwitchDark :is-dark="isDark" @toggle="(e) => emit('toggle-theme', e)" />
        </div>
      </header>

      <main id="main-content" class="app-content">
        <div class="main-inner">
          <router-view v-slot="{ Component, route: r }">
            <ErrorBoundary v-if="Component" title="页面渲染失败">
              <component :is="Component" :key="r.fullPath" />
            </ErrorBoundary>
            <LoadingScreen v-else title="正在加载页面…" subtitle="正在准备页面组件与资源" />
          </router-view>
        </div>
      </main>
    </div>

    <DebugPanel v-model="debugOpen" />
  </div>
</template>

<style scoped>
.app-shell {
  display: flex;
  width: 100%;
  height: 100%;
  overflow: hidden;
  background: var(--vh-bg);
  color: var(--vh-text);
}

.app-sidebar,
.mobile-nav-shell {
  display: flex;
  flex-direction: column;
  flex: 0 0 var(--vh-sidebar-width);
  width: var(--vh-sidebar-width);
  height: 100%;
  box-sizing: border-box;
  border-right: 1px solid var(--vh-border);
  background: var(--vh-surface);
}

.app-sidebar {
  position: relative;
  overflow: hidden;
  transition: flex-basis 180ms ease, width 180ms ease;
}

.app-sidebar.is-collapsed {
  flex-basis: 68px;
  width: 68px;
}

.app-brand {
  display: flex;
  align-items: center;
  gap: 10px;
  min-height: var(--vh-topbar-height);
  padding: 0 20px;
  box-sizing: border-box;
}

.app-brand.is-collapsed {
  justify-content: center;
  padding: 0;
}

.app-brand-mark {
  display: grid;
  width: 28px;
  height: 28px;
  flex: 0 0 auto;
  place-items: center;
  border-radius: var(--vh-radius-sm);
  background: var(--vh-text-strong);
  color: var(--vh-bg);
  font-size: 12px;
  font-weight: 700;
  letter-spacing: 0.04em;
}

.app-brand-name {
  color: var(--vh-text-strong);
  font-size: 16px;
  font-weight: 600;
  letter-spacing: -0.03em;
}

.app-nav-label-heading {
  padding: 16px 20px 8px;
  color: var(--vh-text-subtle);
  font-size: 10px;
  font-weight: 600;
  letter-spacing: 0.12em;
  text-transform: uppercase;
}

.app-nav {
  display: grid;
  gap: 2px;
  padding: 0 10px;
}

.app-nav-item {
  position: relative;
  display: flex;
  min-height: 40px;
  align-items: center;
  gap: 11px;
  padding: 0 10px;
  border-radius: var(--vh-radius-sm);
  color: var(--vh-text-muted);
  font-size: 13px;
  font-weight: 500;
  text-decoration: none;
  transition: background-color 160ms ease, color 160ms ease;
}

.app-nav-item:hover {
  background: var(--vh-surface-muted);
  color: var(--vh-text-strong);
}

.app-nav-item.is-active {
  background: var(--vh-surface-muted);
  color: var(--vh-text-strong);
}

.app-nav-item.is-active::before {
  position: absolute;
  left: -10px;
  width: 2px;
  height: 18px;
  content: "";
  border-radius: 0 2px 2px 0;
  background: var(--vh-text-strong);
}

.app-sidebar.is-collapsed .app-nav {
  padding: 0 12px;
}

.app-sidebar.is-collapsed .app-nav-item {
  justify-content: center;
  padding: 0;
}

.app-sidebar.is-collapsed .app-nav-item.is-active::before {
  left: -12px;
}

.app-nav-icon {
  display: grid;
  width: 18px;
  height: 18px;
  flex: 0 0 auto;
  place-items: center;
}

.app-nav-icon :deep(svg) {
  width: 18px;
  height: 18px;
}

.app-nav-text {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-sidebar-footer {
  display: grid;
  gap: 16px;
  margin-top: auto;
  padding: 16px;
}

.app-system-status,
.app-topbar-status {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  color: var(--vh-text-subtle);
  font-size: 11px;
}

.app-status-dot {
  width: 6px;
  height: 6px;
  flex: 0 0 auto;
  border-radius: 50%;
  background: var(--vh-success);
}

.app-user-row {
  display: flex;
  align-items: center;
  gap: 9px;
  min-width: 0;
  padding-top: 14px;
  border-top: 1px solid var(--vh-border);
}

.app-user-avatar {
  display: grid;
  width: 30px;
  height: 30px;
  flex: 0 0 auto;
  place-items: center;
  border: 1px solid var(--vh-border-strong);
  border-radius: 50%;
  color: var(--vh-text-muted);
}

.app-user-avatar :deep(svg) {
  width: 16px;
  height: 16px;
}

.app-user-copy {
  display: grid;
  min-width: 0;
  flex: 1;
  gap: 2px;
}

.app-user-copy strong,
.app-user-copy span {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.app-user-copy strong {
  color: var(--vh-text);
  font-size: 12px;
  font-weight: 600;
}

.app-user-copy span {
  color: var(--vh-text-subtle);
  font-size: 11px;
}

.app-logout {
  display: grid;
  width: 32px;
  height: 32px;
  flex: 0 0 auto;
  place-items: center;
  border: 0;
  border-radius: var(--vh-radius-sm);
  background: transparent;
  color: var(--vh-text-subtle);
  cursor: pointer;
}

.app-logout:hover {
  background: var(--vh-surface-muted);
  color: var(--vh-danger);
}

.app-logout :deep(svg) {
  width: 16px;
  height: 16px;
}

.app-main {
  display: flex;
  min-width: 0;
  flex: 1;
  flex-direction: column;
  background: var(--vh-bg);
}

.app-topbar {
  display: flex;
  min-height: var(--vh-topbar-height);
  flex: 0 0 var(--vh-topbar-height);
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  padding: 0 28px;
  box-sizing: border-box;
  border-bottom: 1px solid var(--vh-border);
  background: color-mix(in srgb, var(--vh-bg) 92%, transparent);
}

.app-topbar-left,
.app-topbar-right,
.app-breadcrumb {
  display: flex;
  align-items: center;
}

.app-topbar-left,
.app-topbar-right {
  gap: 14px;
}

.app-nav-toggle {
  display: grid;
  width: 36px;
  height: 36px;
  place-items: center;
  border: 1px solid transparent;
  border-radius: var(--vh-radius-sm);
  background: transparent;
  color: var(--vh-text-muted);
  cursor: pointer;
}

.app-nav-toggle:hover {
  border-color: var(--vh-border);
  background: var(--vh-surface);
  color: var(--vh-text-strong);
}

.app-nav-toggle :deep(svg) {
  width: 18px;
  height: 18px;
}

.app-breadcrumb {
  gap: 9px;
  color: var(--vh-text-subtle);
  font-size: 12px;
}

.app-breadcrumb strong {
  color: var(--vh-text);
  font-weight: 500;
}

.app-breadcrumb-separator {
  color: var(--vh-border-strong);
}

.app-topbar-status {
  color: var(--vh-text-muted);
}

.app-content {
  min-height: 0;
  flex: 1;
  overflow: auto;
  padding: 32px 28px 40px;
}

.main-inner {
  width: min(100%, 1440px);
  margin: 0 auto;
}

.mobile-nav-shell {
  width: 100%;
  border-right: 0;
}

:deep(.mobile-drawer .el-drawer__body) {
  padding: 0 !important;
}

@media (max-width: 767px) {
  .app-topbar {
    padding: 0 16px;
  }

  .app-content {
    padding: 24px 16px 32px;
  }
}
</style>
