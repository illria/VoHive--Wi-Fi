<script setup lang="ts">
import { computed } from 'vue'
import type { DashboardDevice } from '../types/api'
import StatusLight from './StatusLight.vue'
import {
  Cellular3G24Regular,
  Cellular4G24Regular,
  Cellular5G24Regular,
  CellularData124Regular,
  Wifi124Regular, 
  Globe24Regular,
  Sim24Regular
} from '@vicons/fluent'

const props = defineProps<{ device: DashboardDevice }>()
const emit = defineEmits<{
  'open-device': [id: string]
}>()

const displayNetworkMode = computed(() => {
  const mode = String(props.device?.network_mode || '').trim()
  const duplex = String(props.device?.network_duplex || '').trim()
  if (!mode) return ''
  return duplex ? `${duplex} ${mode}` : mode
})

const networkIcon = computed(() => {
  // VoWiFi 模式显示 Wi-Fi 图标
  if (props.device?.vowifi_active) return Wifi124Regular
  const mode = displayNetworkMode.value
  if (!mode) return CellularData124Regular
  const m = String(mode).toUpperCase()
  if (m.includes('5G') || m.includes('NR')) return Cellular5G24Regular
  if (m.includes('4G') || m.includes('LTE')) return Cellular4G24Regular
  if (m.includes('3G') || m.includes('WCDMA') || m.includes('HSPA') || m.includes('UMTS')) return Cellular3G24Regular
  return CellularData124Regular
})

const networkColor = computed(() => {
  // VoWiFi 模式显示特殊颜色
  if (props.device?.vowifi_active) return 'text-emerald-500'
  const mode = displayNetworkMode.value
  if (!mode) return 'text-gray-400'
  const m = String(mode).toUpperCase()
  if (m.includes('5G') || m.includes('NR')) return 'text-purple-500'
  if (m.includes('4G') || m.includes('LTE')) return 'text-blue-500'
  if (m.includes('3G')) return 'text-orange-500'
  return 'text-gray-400'
})

const networkModeText = computed(() => {
  const mode = displayNetworkMode.value
  if (!mode) return ''
  const parts = String(mode).trim().split(/\s+/).filter(Boolean)
  if (parts.length <= 1) return parts[0] || ''
  return parts[1] || ''
})

const hideNetworkModeOnNarrow = computed(() => {
  return networkModeText.value.toUpperCase() === 'LTE'
})

function hasValidSignalDbm(dbm: number | null | undefined): dbm is number {
  return typeof dbm === 'number' && Number.isFinite(dbm) && dbm !== 0 && dbm !== -999
}

function getSignalBars(dbm: number | null | undefined) {
  if (!hasValidSignalDbm(dbm)) return 0
  if (dbm > -70) return 4
  if (dbm > -85) return 3
  if (dbm > -100) return 2
  return 1
}
</script>

<template>
  <button
    type="button"
    class="vh-device-card"
    :aria-label="`打开设备 ${device.name || device.id}`"
    @click="emit('open-device', device.id)"
  >
    <div class="vh-device-card-top">
      <div class="vh-device-card-identity">
        <div class="vh-device-mark" aria-hidden="true"><Sim24Regular /></div>
        <div class="min-w-0">
          <h3 class="vh-device-card-title">{{ device.name || device.id }}</h3>
          <div class="vh-device-card-subtitle">{{ device.id }}</div>
        </div>
      </div>
      <div class="vh-device-status" :class="{ 'is-online': device.healthy }">
        <StatusLight :tone="device.healthy ? 'success' : 'danger'" size="sm" :animated="device.healthy" />
        <span>{{ device.healthy ? '在线' : '离线' }}</span>
      </div>
    </div>

    <div class="vh-device-card-divider" />

    <div class="vh-device-card-row">
      <div class="vh-device-card-label">
        <el-icon :class="networkColor" aria-hidden="true"><component :is="networkIcon" /></el-icon>
        <span
          v-if="!device.vowifi_active && device.network_mode && networkModeText"
          :class="hideNetworkModeOnNarrow ? 'hidden xl:inline' : ''"
        >{{ networkModeText }}</span>
        <span class="vh-device-card-value">
          {{ device.vowifi_active ? 'Wi-Fi Calling' : (device.operator || '检测中...') }}
        </span>
      </div>
      <div v-if="!device.vowifi_active" class="vh-device-signal" title="信号强度">
        <div class="vh-signal-bars" aria-hidden="true">
          <span v-for="i in 4" :key="i" :class="{ 'is-active': getSignalBars(device.signal_dbm) >= i }" />
        </div>
        <span class="vh-device-card-label vh-device-signal-text">{{ device.signal_dbm || '--' }} dBm</span>
      </div>
    </div>

    <div class="vh-device-card-row vh-device-ip-row">
      <span class="vh-device-card-label"><el-icon aria-hidden="true"><Globe24Regular /></el-icon>公网 IP</span>
      <span class="vh-device-card-ip">{{ device.public_ip || '---' }}</span>
    </div>
  </button>
</template>
