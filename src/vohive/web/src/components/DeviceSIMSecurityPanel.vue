<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { DeviceLifecyclePhase, SIMSecurityState, SIMSecurityStatus } from '../types/api'
import { errorMessage } from '../services/http'
import { devicesService } from '../services/devices'
import { isRecoveryPhase } from '../utils/deviceLifecycle'

const props = defineProps<{
  deviceId: string
  visible: boolean
  controlOnline: boolean
  lifecyclePhase?: DeviceLifecyclePhase
  backendMode?: string
}>()

const emit = defineEmits<{
  refresh: []
}>()

const security = ref<SIMSecurityState | null>(null)
const loading = ref(false)
const submitting = ref(false)
const pin = ref('')
const showPin = ref(false)
const errorText = ref('')
const statusAbort = ref<AbortController | null>(null)
let pollTimer: number | null = null

const isRecovering = computed(() => !props.controlOnline || isRecoveryPhase(props.lifecyclePhase))
const isQMIBackend = computed(() => !props.backendMode || props.backendMode === 'qmi')
const pinValid = computed(() => validPin(pin.value))
const canSubmit = computed(() => {
  const current = security.value
  return props.visible &&
    !isRecovering.value &&
    !loading.value &&
    !submitting.value &&
    current?.status === 'pin_required' &&
    current.can_verify_pin === true &&
    pinValid.value
})

const statusLabels: Record<SIMSecurityStatus, string> = {
  ready: '已就绪',
  pin_required: '需要 PIN',
  puk_required: '需要 PUK',
  blocked: '已阻塞',
  absent: '未检测到 SIM',
  network_locked: '网络锁定',
  initializing: '初始化中',
  unavailable: '暂不可用',
  unsupported: '不支持'
}

type StatusTagType = 'success' | 'warning' | 'danger' | 'info'

const statusTone: Record<SIMSecurityStatus, StatusTagType> = {
  ready: 'success',
  pin_required: 'warning',
  puk_required: 'danger',
  blocked: 'danger',
  absent: 'info',
  network_locked: 'danger',
  initializing: 'warning',
  unavailable: 'info',
  unsupported: 'info'
}

function validPin(value: string) {
  return /^[0-9]{4,8}$/.test(value)
}

function clearPoll() {
  if (pollTimer !== null) {
    window.clearTimeout(pollTimer)
    pollTimer = null
  }
}

function isCancelled(code?: string) {
  return code === 'ERR_CANCELED' || code === 'ECONNABORTED'
}

function schedulePoll() {
  clearPoll()
  const current = security.value
  if (!props.visible || isRecovering.value || current?.status !== 'pin_required') return
  pollTimer = window.setTimeout(() => {
    pollTimer = null
    void fetchStatus()
  }, 12000)
}

async function fetchStatus() {
  clearPoll()
  statusAbort.value?.abort()
  statusAbort.value = null
  if (!props.visible || !props.deviceId) return
  if (!isQMIBackend.value) {
    security.value = null
    errorText.value = '当前后端不支持 SIM PIN 操作'
    return
  }
  if (isRecovering.value) {
    errorText.value = '设备正在恢复中，暂不读取 SIM 安全状态'
    return
  }

  const controller = new AbortController()
  statusAbort.value = controller
  loading.value = true
  errorText.value = ''
  const result = await devicesService.getSIMSecurity(props.deviceId, controller.signal)
  if (statusAbort.value !== controller) return
  statusAbort.value = null
  if (result.ok) {
    security.value = result.data
  } else if (!isCancelled(result.error.code)) {
    errorText.value = errorMessage(result.error, 'SIM 安全状态暂不可用')
  }
  loading.value = false
  schedulePoll()
}

function clearPin() {
  pin.value = ''
  showPin.value = false
}

async function verifyPin() {
  const current = security.value
  if (!current?.pin_required || !current.can_verify_pin) {
    await fetchStatus()
    return
  }
  if (!pinValid.value) {
    errorText.value = 'PIN 必须是 4–8 位 ASCII 数字'
    return
  }
  if (current.pin_retries === 1) {
    try {
      await ElMessageBox.confirm(
        '这是当前显示的最后一次 PIN 尝试。继续后若失败，SIM 可能要求 PUK 解锁。',
        '确认验证 SIM PIN',
        { confirmButtonText: '继续验证', cancelButtonText: '取消', type: 'warning' }
      )
    } catch {
      return
    }
  }

  const submittedPin = pin.value
  submitting.value = true
  errorText.value = ''
  clearPoll()
  let result: Awaited<ReturnType<typeof devicesService.verifySIMPIN>>
  try {
    result = await devicesService.verifySIMPIN(props.deviceId, submittedPin)
  } finally {
    // Clear both successful and failed submissions. The PIN is never kept in
    // component state after the request completes.
    clearPin()
    submitting.value = false
  }

  if (result.ok) {
    security.value = result.data.security
    ElMessage.success(result.data.message || 'SIM PIN 验证成功')
    emit('refresh')
    await fetchStatus()
    return
  }

  if (isCancelled(result.error.code)) {
    errorText.value = '请求结果未知，已刷新 SIM 状态，请勿立即重复提交'
  } else {
    errorText.value = errorMessage(result.error, 'SIM PIN 验证失败')
  }
  // A failed or timed-out POST is followed by one status GET only; never
  // repeat the PIN automatically.
  await fetchStatus()
}

function statusLabel(status?: SIMSecurityStatus) {
  return status ? statusLabels[status] : '等待读取'
}

function pinKindLabel(kind?: 'pin1' | 'upin') {
  return kind === 'upin' ? 'UPIN' : kind === 'pin1' ? 'PIN1' : '—'
}

function statusTagType(status?: SIMSecurityStatus): StatusTagType {
  return status ? statusTone[status] : 'info'
}

watch(
  () => [props.visible, props.deviceId, props.controlOnline, props.lifecyclePhase, props.backendMode],
  () => {
    clearPoll()
    if (!props.visible) {
      statusAbort.value?.abort()
      statusAbort.value = null
      return
    }
    void fetchStatus()
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  clearPoll()
  statusAbort.value?.abort()
  statusAbort.value = null
  clearPin()
})
</script>

<template>
  <section class="ui-panel-muted p-4 space-y-4" aria-labelledby="sim-security-title">
    <div class="flex items-start justify-between gap-3">
      <div>
        <div id="sim-security-title" class="text-xs font-bold text-gray-500 uppercase tracking-wider">SIM 安全</div>
        <div class="mt-1 text-xs text-gray-500 dark:text-gray-400">仅处理当前物理 SIM 或 eUICC 活跃 profile 的 PIN 状态</div>
      </div>
      <el-button text size="small" :loading="loading" :disabled="!visible || isRecovering" @click="fetchStatus">刷新</el-button>
    </div>

    <div class="flex items-center gap-2">
      <el-tag size="small" :type="statusTagType(security?.status)" effect="plain">{{ statusLabel(security?.status) }}</el-tag>
      <span v-if="loading" class="text-xs text-gray-400">读取中…</span>
      <span v-else-if="security?.updated_at" class="text-xs text-gray-400">状态已更新</span>
    </div>

    <div v-if="errorText" class="rounded-lg border border-amber-200 bg-amber-50 px-3 py-2 text-xs text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300">
      {{ errorText }}
    </div>

    <div v-if="security" class="grid grid-cols-2 gap-3 text-sm">
      <div>
        <div class="text-xs text-gray-400">PIN 类型</div>
        <div class="mt-1 text-gray-700 dark:text-gray-200">{{ pinKindLabel(security.pin_kind) }}</div>
      </div>
      <div>
        <div class="text-xs text-gray-400">PIN 剩余次数</div>
        <div class="mt-1 text-gray-700 dark:text-gray-200">{{ security.pin_retries ?? '—' }}</div>
      </div>
      <div>
        <div class="text-xs text-gray-400">PUK 剩余次数</div>
        <div class="mt-1 text-gray-700 dark:text-gray-200">{{ security.puk_retries ?? '—' }}</div>
      </div>
      <div>
        <div class="text-xs text-gray-400">后端</div>
        <div class="mt-1 text-gray-700 dark:text-gray-200">{{ security.backend || '—' }}</div>
      </div>
    </div>

    <div v-if="security?.status === 'pin_required' && security.pin_retries === 1" class="rounded-lg border border-red-200 bg-red-50 px-3 py-2 text-xs text-red-700 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300">
      只剩 1 次 PIN 尝试，失败后可能需要 PUK。当前页面不提供 PUK、改 PIN 或启停 PIN 操作。
    </div>

    <div v-if="security?.status === 'pin_required' && security.can_verify_pin && !isRecovering" class="space-y-2">
      <label class="text-xs font-medium text-gray-600 dark:text-gray-300" for="sim-security-pin">输入 SIM PIN</label>
      <div class="flex flex-col sm:flex-row gap-2">
        <el-input
          id="sim-security-pin"
          v-model="pin"
          :disabled="submitting"
          type="password"
          show-password
          inputmode="numeric"
          pattern="[0-9]*"
          minlength="4"
          maxlength="8"
          autocomplete="off"
          spellcheck="false"
          placeholder="4–8 位数字"
          class="flex-1"
        />
        <el-button type="primary" :loading="submitting" :disabled="!canSubmit" @click="verifyPin">验证并解锁</el-button>
      </div>
      <div class="text-xs text-gray-400">不会保存 PIN，也不会自动重复提交。</div>
    </div>

    <div v-else-if="security?.status === 'puk_required'" class="text-xs text-gray-500 dark:text-gray-400">
      SIM 已进入 PUK 状态。本页面不执行 PUK 解锁；请使用运营商或设备管理工具完成后再刷新。
    </div>
    <div v-else-if="isRecovering" class="text-xs text-gray-500 dark:text-gray-400">
      设备控制面正在恢复，SIM 安全操作暂时停用。
    </div>
  </section>
</template>
