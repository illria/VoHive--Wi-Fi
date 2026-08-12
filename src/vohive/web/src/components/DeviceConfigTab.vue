<script setup lang="ts">
import { computed, watch } from 'vue'
import {
  Router24Regular,
  Delete24Regular,
  Save24Regular
} from '@vicons/fluent'
import type { DeviceConfigDTO, DeviceOverviewItem } from '../types/api'
import { isWwanQmiControlPath } from '../utils/deviceBackend'

const props = defineProps<{
  editConfig: DeviceConfigDTO | null
  deviceStatus?: DeviceOverviewItem | null
  saving: boolean
  deleting: boolean
}>()

const emit = defineEmits<{
  save: []
  delete: []
}>()

const activeControlDevice = computed(() => props.deviceStatus?.control_device || props.editConfig?.control_device)
const activeInterface = computed(() => props.deviceStatus?.interface || props.editConfig?.interface)
const activeATPort = computed(() => props.deviceStatus?.at_port || props.editConfig?.at_port)
const activeUsbPath = computed(() => props.deviceStatus?.usb_path || props.editConfig?.usb_path)

const isQMIBackendOnly = computed(() => isWwanQmiControlPath(activeControlDevice.value))
const isMBIMBackendOnly = computed(
  () => String(props.editConfig?.device_backend || '').toLowerCase() === 'mbim'
)
const isPCSCBackend = computed(
  () => String(props.editConfig?.device_backend || '').toLowerCase() === 'pcsc'
)

watch(
  isQMIBackendOnly,
  (locked) => {
    if (locked && props.editConfig) {
      props.editConfig.device_backend = 'qmi'
    }
  },
  { immediate: true }
)
</script>

<template>
  <div>
    <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-4 mb-4">
      <div class="flex items-center gap-3">
        <div class="w-10 h-10 rounded-xl bg-indigo-50 dark:bg-indigo-500/10 flex items-center justify-center text-indigo-600 dark:text-indigo-400">
          <el-icon size="22"><Router24Regular /></el-icon>
        </div>
        <div>
          <div class="text-lg font-bold text-gray-900 dark:text-white">设备配置</div>
          <div class="text-xs text-gray-500 dark:text-gray-400">配置存储在数据库中，部分字段可能需要重启生效</div>
        </div>
      </div>
      <div class="flex items-center gap-2">
        <el-button type="danger" :loading="deleting" @click="emit('delete')" class="!border-0">
          <el-icon><Delete24Regular /></el-icon>
          删除设备
        </el-button>
        <el-button type="primary" :loading="saving" @click="emit('save')" class="!border-0">
          <el-icon><Save24Regular /></el-icon>
          保存配置
        </el-button>
      </div>
    </div>

    <div v-if="editConfig" class="grid grid-cols-1 lg:grid-cols-2 gap-4">
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">ID</label>
        <el-input v-model="editConfig.id" disabled />
      </div>
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">名称</label>
        <el-input v-model="editConfig.name" placeholder="显示名称" />
      </div>
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">IMEI 绑定</label>
        <el-input v-model="editConfig.modem_imei" :disabled="!isPCSCBackend" :placeholder="isPCSCBackend ? 'VoWiFi 必填；读卡器无法自动提供' : '自动识别（添加时绑定）'" />
      </div>
      <div v-if="isPCSCBackend" class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">PC/SC 读卡器</label>
        <el-input v-model="editConfig.reader_name" disabled />
      </div>
      <div v-if="isPCSCBackend" class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">卡 ICCID</label>
        <el-input v-model="editConfig.card_iccid" disabled />
      </div>
      <div v-if="isPCSCBackend" class="ui-panel-muted p-3">
        <div class="text-sm font-bold" :class="editConfig.provisioning_state === 'draft' ? 'text-amber-700' : 'text-emerald-700'">
          {{ editConfig.provisioning_state === 'draft' ? '草稿线路 · 请补齐 IMEI' : '线路参数已就绪' }}
        </div>
        <div class="text-xs text-gray-500 mt-1">卡片移动到其他读卡器后，系统会继续按 ICCID 自动绑定。</div>
      </div>
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">设备路径</label>
        <el-input :model-value="activeUsbPath || ''" disabled placeholder="由系统自动探测" />
      </div>
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">网卡接口</label>
        <el-input :model-value="activeInterface || ''" disabled placeholder="由系统自动探测" />
      </div>
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">AT 端口</label>
        <el-input :model-value="activeATPort || ''" disabled placeholder="由系统自动探测" />
      </div>
      <div class="space-y-1">
        <label class="text-xs font-bold text-gray-500 uppercase tracking-wider">控制设备</label>
        <el-input :model-value="activeControlDevice || ''" disabled placeholder="由系统自动探测" />
      </div>
      <div class="ui-panel-muted p-3 space-y-2">
        <div class="flex items-center justify-between">
          <div>
            <div class="text-sm font-bold text-gray-800 dark:text-gray-100">设备运行模式</div>
            <div class="text-xs text-gray-500 dark:text-gray-400">
              {{ isQMIBackendOnly ? '此类设备固定 QMI，AT 口仅用于终端'
                 : (isMBIMBackendOnly ? '此类设备固定 MBIM，AT 口仅用于终端'
		         : (isPCSCBackend ? 'PC/SC 读卡器线路，按 ICCID 自动跟踪'
		         : 'AT=传统串口 / QMI=纯 QMI')) }}
            </div>
          </div>
          <el-select
            v-model="editConfig.device_backend"
            style="width: 120px"
            placeholder="AT"
            :disabled="isQMIBackendOnly || isMBIMBackendOnly || isPCSCBackend"
          >
			<el-option v-if="!isMBIMBackendOnly && !isPCSCBackend" label="AT" value="at" :disabled="isQMIBackendOnly" />
			<el-option v-if="!isMBIMBackendOnly && !isPCSCBackend" label="QMI" value="qmi" :disabled="!activeControlDevice && editConfig.device_backend !== 'qmi'" />
            <el-option v-if="isMBIMBackendOnly" label="MBIM" value="mbim" />
            <el-option v-if="isPCSCBackend" label="PC/SC" value="pcsc" />
          </el-select>
        </div>
      </div>
    </div>
  </div>
</template>
