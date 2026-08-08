<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import type { EsimChipInfo, EsimEUICCProfiles, EsimNotificationItem, EsimProfileItem, EsimSpaceDelta } from '../types/api'
import { devicesService } from '../services/devices'
import { errorMessage } from '../services/http'
import { api } from '../stores/auth'
import { useSensitiveVisibility } from '../composables/useSensitiveVisibility'
import { applyOptimisticActiveState } from './deviceEsimOptimistic'
import { pickNextDownloadAid } from './deviceEsimOverviewRefresh'
import { describeDeleteResultNotice, describeDownloadTerminalNotice, describeSpaceDelta } from './deviceEsimOperationNotice'
import { parseEsimActivationCode } from '../utils/esimActivationCode'
import jsQR from 'jsqr'
import {
  formatEsimNotificationEvent,
  notificationDialogWidth,
  notificationListItemLayoutClass,
  notificationMetaContainerClass,
  notificationMetaItemClass,
  reconcileEsimNotificationDialogState,
  shouldShowEsimNotificationIcon,
  shouldShowEsimRefreshIcon
} from './deviceEsimNotifications'
import {
  Add24Regular,
  Alert24Regular,
  ArrowDownload24Regular,
  ArrowSync24Regular,
  Eye24Regular,
  EyeOff24Regular
} from '@vicons/fluent'

const props = defineProps<{
  deviceId: string
  isActive?: boolean
}>()

// 数据状态
const loading = ref(false)
const profilesRefreshing = ref(false)
const chipInfo = ref<EsimChipInfo | null>(null)
const profiles = ref<EsimEUICCProfiles[]>([])
const expiryDrafts = ref<Record<string, string>>({})
const expirySaving = ref<string | null>(null)
const expiryProfiles = computed<EsimProfileItem[]>(() => profiles.value.flatMap((group) => group.profiles || []))

// 操作状态
const switching = ref<string | null>(null)
const deleting = ref<string | null>(null)
const renaming = ref<string | null>(null)
const showSensitive = useSensitiveVisibility()
const renameValue = ref('')
const noteEditing = ref<string | null>(null)
const noteSaving = ref<string | null>(null)
const noteValue = ref('')
const notifications = ref<EsimNotificationItem[]>([])
const notificationsLoading = ref(false)
const notificationsDialogOpen = ref(false)
const retryingNotificationSequence = ref<number | null>(null)

// 下载表单
const downloadForm = ref({
  smdp: '',
  matchingId: '',
  confirmationCode: '',
  aidHex: '',
  imei: ''
})
const downloading = ref(false)
const downloadProgress = ref(0)
const downloadMsg = ref('')
const downloadError = ref('')
const downloadSessionId = ref(0)
const qrInput = ref<HTMLInputElement | null>(null)
const qrScanning = ref(false)
const qrScanError = ref('')
const qrScanNote = ref('')
const recentSpaceDelta = ref<{ aidHex: string; message: string } | null>(null)
let recentSpaceDeltaTimer: number | null = null

type BarcodeDetection = { rawValue?: string }
type BarcodeDetectorInstance = {
  detect(source: unknown): Promise<BarcodeDetection[]>
}
type BarcodeDetectorConstructor = new (options?: { formats?: string[] }) => BarcodeDetectorInstance

function applyActivationCode(input: string, notify = false) {
  const parsed = parseEsimActivationCode(input)
  downloadForm.value.smdp = parsed.smdp
  downloadForm.value.matchingId = parsed.matchingId
  qrScanNote.value = parsed.optionalFields.length
    ? '二维码含标准可选参数；确认码仍请按运营商提供的内容填写。'
    : ''
  if (notify) {
    ElMessage.success('已识别二维码，请确认目标 eUICC 后开始下载')
  }
  return parsed
}

// 智能解析完整的 LPA 激活码或移除 URL 前缀
watch(() => downloadForm.value.smdp, (newVal) => {
  const value = newVal.trim()
  if (!value) return

  if (/^(?:LPA:)?1\$/i.test(value)) {
    try {
      applyActivationCode(value, true)
    } catch {
      // 输入尚未完成时不打断用户；点击上传二维码时会给出明确错误。
    }
  } else if (/^https?:\/\//i.test(value)) {
    downloadForm.value.smdp = value.replace(/^https?:\/\//i, '')
  }
})

function barcodeDetectorConstructor(): BarcodeDetectorConstructor | null {
  const candidate = (window as Window & { BarcodeDetector?: BarcodeDetectorConstructor }).BarcodeDetector
  return typeof candidate === 'function' ? candidate : null
}

async function detectQrPayloadWithBarcodeDetector(file: File, Detector: BarcodeDetectorConstructor): Promise<string> {
  const detector = new Detector({ formats: ['qr_code'] })
  if (typeof createImageBitmap === 'function') {
    const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })
    try {
      const results = await detector.detect(bitmap)
      return results.find((item) => item.rawValue?.trim())?.rawValue?.trim() || ''
    } finally {
      bitmap.close()
    }
  }

  const objectUrl = URL.createObjectURL(file)
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image()
      element.onload = () => resolve(element)
      element.onerror = () => reject(new Error('无法读取二维码图片'))
      element.src = objectUrl
    })
    const results = await detector.detect(image)
    return results.find((item) => item.rawValue?.trim())?.rawValue?.trim() || ''
  } finally {
    URL.revokeObjectURL(objectUrl)
  }
}

type QrImageSource = HTMLImageElement | ImageBitmap
type QrImageHandle = {
  source: QrImageSource
  width: number
  height: number
  close: () => void
}

function isHeicFile(file: File): boolean {
  return /image\/hei[cf]/i.test(file.type) || /\.hei[cf]$/i.test(file.name)
}

async function loadQrImage(file: File): Promise<QrImageHandle> {
  if (typeof createImageBitmap === 'function') {
    try {
      const bitmap = await createImageBitmap(file, { imageOrientation: 'from-image' })
      if (bitmap.width > 0 && bitmap.height > 0) {
        return {
          source: bitmap,
          width: bitmap.width,
          height: bitmap.height,
          close: () => bitmap.close()
        }
      }
      bitmap.close()
    } catch {
      // Safari and some Android browsers expose createImageBitmap but cannot
      // decode every image format. Fall back to an ordinary HTML image.
    }
  }

  const objectUrl = URL.createObjectURL(file)
  try {
    const image = await new Promise<HTMLImageElement>((resolve, reject) => {
      const element = new Image()
      element.decoding = 'async'
      element.onload = () => resolve(element)
      element.onerror = () => reject(new Error('无法读取二维码图片'))
      element.src = objectUrl
    })
    const width = image.naturalWidth || image.width
    const height = image.naturalHeight || image.height
    if (!width || !height) {
      throw new Error('二维码图片尺寸无效')
    }
    return {
      source: image,
      width,
      height,
      close: () => URL.revokeObjectURL(objectUrl)
    }
  } catch (error) {
    URL.revokeObjectURL(objectUrl)
    throw error
  }
}

function decodeQrImageData(imageData: ImageData, width: number, height: number): string {
  const result = jsQR(imageData.data, width, height, { inversionAttempts: 'attemptBoth' })
  if (result?.data?.trim()) return result.data.trim()

  // A phone photo may have a very bright background or low contrast. A single
  // high-contrast pass costs little and helps jsQR when the original pixels
  // contain shadows, glare, or light grey QR modules.
  const thresholded = new Uint8ClampedArray(imageData.data.length)
  for (let index = 0; index < imageData.data.length; index += 4) {
    const luminance = Math.round(
      imageData.data[index] * 0.299 +
      imageData.data[index + 1] * 0.587 +
      imageData.data[index + 2] * 0.114
    )
    const value = luminance < 160 ? 0 : 255
    thresholded[index] = value
    thresholded[index + 1] = value
    thresholded[index + 2] = value
    thresholded[index + 3] = 255
  }
  const thresholdedResult = jsQR(thresholded, width, height, { inversionAttempts: 'attemptBoth' })
  return thresholdedResult?.data?.trim() || ''
}

type QrCrop = {
  left: number
  top: number
  width: number
  height: number
}

function decodeQrAtSize(
  source: QrImageSource,
  sourceWidth: number,
  sourceHeight: number,
  maxDimension: number,
  crop?: QrCrop
): string {
  const region = crop || { left: 0, top: 0, width: sourceWidth, height: sourceHeight }
  const scale = Math.min(1, maxDimension / Math.max(region.width, region.height))
  const width = Math.max(1, Math.round(region.width * scale))
  const height = Math.max(1, Math.round(region.height * scale))
  const canvas = document.createElement('canvas')
  canvas.width = width
  canvas.height = height
  const context = canvas.getContext('2d', { willReadFrequently: true })
  if (!context) {
    throw new Error('当前浏览器无法读取图片像素')
  }
  context.fillStyle = '#ffffff'
  context.fillRect(0, 0, width, height)
  context.drawImage(source, region.left, region.top, region.width, region.height, 0, 0, width, height)
  return decodeQrImageData(context.getImageData(0, 0, width, height), width, height)
}

function yieldToBrowser(): Promise<void> {
  return new Promise((resolve) => {
    if (typeof requestAnimationFrame === 'function') {
      requestAnimationFrame(() => resolve())
    } else {
      window.setTimeout(resolve, 0)
    }
  })
}

async function detectQrPayloadWithJsQr(file: File): Promise<string> {
  const image = await loadQrImage(file)
  try {
    const { source, width, height } = image
    const attempts: Array<{ maxDimension: number; crop?: QrCrop }> = [
      { maxDimension: 3200 },
      { maxDimension: 2400 },
      {
        maxDimension: 2400,
        crop: {
          left: width * 0.1,
          top: height * 0.1,
          width: width * 0.8,
          height: height * 0.8
        }
      },
      { maxDimension: 1600 }
    ]

    for (const attempt of attempts) {
      const payload = decodeQrAtSize(source, width, height, attempt.maxDimension, attempt.crop)
      if (payload) return payload
      // Keep the page responsive while trying additional resolutions on a phone.
      await yieldToBrowser()
    }
    return ''
  } finally {
    image.close()
  }
}

type QrDetectionResult = {
  payload: string
  usedFallback: boolean
}

async function detectQrPayload(file: File): Promise<QrDetectionResult> {
  const Detector = barcodeDetectorConstructor()
  if (Detector) {
    try {
      const payload = await detectQrPayloadWithBarcodeDetector(file, Detector)
      if (payload) return { payload, usedFallback: false }
    } catch {
      // Some browsers expose BarcodeDetector but fail for a particular image;
      // continue with the bundled decoder below.
    }
  }

  let fallbackError: unknown = null
  try {
    const payload = await detectQrPayloadWithJsQr(file)
    if (payload) return { payload, usedFallback: true }
  } catch (error: unknown) {
    fallbackError = error
    // The user-facing error below also covers browsers with restricted canvas
    // access or images that cannot be decoded by the fallback.
  }

  if (isHeicFile(file)) {
    throw new Error('手机选择的是 HEIC/HEIF 图片，当前浏览器无法稳定读取该格式；请将二维码导出或截图为 JPG/PNG 后再上传，也可以直接粘贴 LPA 激活码')
  }
  if (fallbackError instanceof Error && fallbackError.message === '当前浏览器无法读取图片像素') {
    throw new Error('当前浏览器无法读取图片像素；请改用 JPG/PNG 图片，或直接粘贴 LPA 激活码')
  }
  throw new Error('兼容二维码识别未读取到二维码；请上传完整、清晰的 JPG/PNG 图片，确保二维码四周留有白边，或直接粘贴 LPA 激活码')
}

function openQrPicker() {
  if (!qrScanning.value) qrInput.value?.click()
}

async function handleQrFileChange(event: Event) {
  const input = event.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = ''
  if (!file) return

  qrScanning.value = true
  qrScanError.value = ''
  qrScanNote.value = ''
  try {
    const detection = await detectQrPayload(file)
    applyActivationCode(detection.payload)
    if (detection.usedFallback) {
      qrScanNote.value = '浏览器原生识别不可用，已使用兼容识别模式。'
    }
    ElMessage.success('已识别二维码，请确认目标 eUICC 后开始下载')
  } catch (e: unknown) {
    qrScanError.value = errorMessage(e, '二维码识别失败')
    ElMessage.error(qrScanError.value)
  } finally {
    qrScanning.value = false
  }
}

let fetchAbortController: AbortController | null = null
let fetchOverviewRequestId = 0

function normalizeAidHex(aidHex: string | undefined | null): string {
  return (aidHex || '').trim().toUpperCase()
}

function clearRecentSpaceDelta() {
  if (recentSpaceDeltaTimer !== null) {
    window.clearTimeout(recentSpaceDeltaTimer)
    recentSpaceDeltaTimer = null
  }
  recentSpaceDelta.value = null
}

function showRecentSpaceDelta(aidHex: string, spaceDelta?: EsimSpaceDelta) {
  const normalizedAidHex = normalizeAidHex(aidHex)
  const message = describeSpaceDelta(spaceDelta)
  if (!normalizedAidHex || !message) return
  clearRecentSpaceDelta()
  recentSpaceDelta.value = { aidHex: normalizedAidHex, message }
  recentSpaceDeltaTimer = window.setTimeout(() => {
    recentSpaceDelta.value = null
    recentSpaceDeltaTimer = null
  }, 75000)
}

function syncExpiryDrafts(nextProfiles: EsimEUICCProfiles[]) {
  const next: Record<string, string> = {}
  for (const group of nextProfiles) {
    for (const profile of group.profiles || []) {
      next[profile.iccid] = profile.expiry_date || ''
    }
  }
  expiryDrafts.value = next
}

function setExpiryDraft(iccid: string, value: unknown) {
  expiryDrafts.value[iccid] = typeof value === 'string' ? value : ''
}

function profileExpiryValue(profile: EsimProfileItem): string {
  return expiryDrafts.value[profile.iccid] ?? profile.expiry_date ?? ''
}

function profileExpirySummary(profile: EsimProfileItem): string {
  const expiryDate = profileExpiryValue(profile)
  return expiryDate ? `到期 ${expiryDate} · 提前 7 天推送提醒` : '永久有效 · 不发送到期提醒'
}

async function fetchNotifications() {
  notificationsLoading.value = true
  const result = await devicesService.getEsimNotifications(props.deviceId)
  try {
    if (!result.ok) throw result.error
    notifications.value = result.data
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '获取当前通知列表失败'))
  } finally {
    notificationsLoading.value = false
  }
}

async function openNotificationsDialog() {
  notificationsDialogOpen.value = true
  await fetchNotifications()
}

async function retryNotification(item: EsimNotificationItem) {
  if (!item.can_retry || retryingNotificationSequence.value !== null) return
  retryingNotificationSequence.value = item.sequence_number
  const result = await devicesService.retryEsimNotification(props.deviceId, item.sequence_number, item.aid_hex)
  try {
    if (!result.ok) throw result.error
    retryingNotificationSequence.value = null
    ElMessage.success(result.data.message)
    const refreshed = await devicesService.getEsimNotifications(props.deviceId)
    if (!refreshed.ok) {
      ElMessage.warning(refreshed.error.message || '通知已发送，但刷新通知列表失败')
      return
    }
    const nextState = reconcileEsimNotificationDialogState({
      isOpen: notificationsDialogOpen.value,
      items: notifications.value,
      refreshedItems: refreshed.data,
      retriedSequenceNumber: item.sequence_number
    })
    notificationsDialogOpen.value = nextState.isOpen
    notifications.value = nextState.items
    retryingNotificationSequence.value = nextState.retryingSequenceNumber
  } catch (e: unknown) {
    const nextState = reconcileEsimNotificationDialogState({
      isOpen: notificationsDialogOpen.value,
      items: notifications.value,
      refreshedItems: notifications.value,
      retriedSequenceNumber: null
    })
    notificationsDialogOpen.value = nextState.isOpen
    notifications.value = nextState.items
    retryingNotificationSequence.value = nextState.retryingSequenceNumber
    ElMessage.error(errorMessage(e, '通知重试发送失败'))
  }
}

// 获取 eSIM 总览数据
async function fetchOverview(refresh = false) {
  fetchOverviewRequestId += 1
  const requestId = fetchOverviewRequestId

  if (fetchAbortController) {
    fetchAbortController.abort()
  }
  const controller = new AbortController()
  fetchAbortController = controller

  if (refresh) {
    profilesRefreshing.value = true
  } else {
    loading.value = true
  }

  const currentAidHex = downloadForm.value.aidHex
  const result = await devicesService.getEsimOverview(props.deviceId, {
    refresh,
    signal: controller.signal
  })
  let shouldResetLoading = true
  try {
    if (requestId !== fetchOverviewRequestId) {
      shouldResetLoading = false
      return
    }
    if (!result.ok) throw result.error
    chipInfo.value = result.data.chipInfo
    profiles.value = result.data.profiles
    syncExpiryDrafts(result.data.profiles)
    downloadForm.value.aidHex = pickNextDownloadAid(chipInfo.value, currentAidHex)
  } catch (e: unknown) {
    if (result.ok === false && result.error.code === 'ERR_CANCELED') {
      return
    }
    ElMessage.error(errorMessage(e, '获取 eSIM 信息失败'))
  } finally {
    if (shouldResetLoading) {
      if (refresh) {
        profilesRefreshing.value = false
      } else {
        loading.value = false
      }
    }
  }
}

async function fetchProfiles(refresh = false) {
  profilesRefreshing.value = true
  const result = await devicesService.getEsimProfiles(props.deviceId, { refresh })
  try {
    if (!result.ok) throw result.error
    profiles.value = result.data
    syncExpiryDrafts(result.data)
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '获取 eSIM Profiles 失败'))
  } finally {
    profilesRefreshing.value = false
  }
}

function applyOptimisticActive(targetICCID: string, aidHex: string) {
  profiles.value = applyOptimisticActiveState(profiles.value, targetICCID, aidHex)
}

// 切换 profile（启用/禁用）
async function switchProfile(iccid: string, currentState: number, aidHex: string) {
  const action = currentState === 1 ? '禁用' : '启用'
  const confirmed = await ElMessageBox.confirm(
    `确定要${action}此 Profile (${iccid}) 吗？切换后设备会短暂断网。`,
    `${action} Profile`,
    { confirmButtonText: action, cancelButtonText: '取消', type: 'warning' }
  ).then(() => true).catch(() => false)
  if (!confirmed) return

  switching.value = iccid
  try {
    const result = await devicesService.switchEsimProfile(props.deviceId, {
      iccid,
      aid_hex: aidHex
    })
    if (!result.ok) throw new Error(result.error.message || `${action}失败`)
    ElMessage.success(`Profile ${action}成功`)
    applyOptimisticActive(iccid, aidHex)
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, `${action}失败`))
  } finally {
    switching.value = null
  }
}

// 开始编辑名称
function startRename(iccid: string, currentName: string) {
  renaming.value = iccid
  renameValue.value = currentName
}

// 保存名称
async function saveRename(iccid: string, aidHex: string) {
  const name = renameValue.value.trim()
  if (!name) {
    ElMessage.warning('名称不能为空')
    return
  }
  try {
    const result = await devicesService.renameEsimProfile(props.deviceId, iccid, { name, aid_hex: aidHex })
    if (!result.ok) throw new Error(result.error.message || '修改名称失败')
    ElMessage.success('名称修改成功')
    renaming.value = null
    await fetchProfiles(true)
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '修改名称失败'))
  }
}

// 取消编辑
function cancelRename() {
  renaming.value = null
  renameValue.value = ''
}

function startNoteEdit(iccid: string, currentNote: string | undefined) {
  noteEditing.value = iccid
  noteValue.value = currentNote || ''
}

function cancelNoteEdit() {
  noteEditing.value = null
  noteValue.value = ''
}

async function saveProfileNote(iccid: string) {
  noteSaving.value = iccid
  try {
    const result = await devicesService.updateEsimProfileNote(props.deviceId, iccid, noteValue.value)
    if (!result.ok) throw new Error(result.error.message || '保存备注失败')
    ElMessage.success(noteValue.value.trim() ? '备注已保存' : '备注已清空')
    cancelNoteEdit()
    await fetchProfiles(false)
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '保存备注失败'))
  } finally {
    noteSaving.value = null
  }
}

async function saveProfileExpiry(iccid: string) {
  const expiryDate = expiryDrafts.value[iccid] || ''
  expirySaving.value = iccid
  try {
    const result = await devicesService.updateEsimProfileExpiry(props.deviceId, iccid, expiryDate)
    if (!result.ok) throw new Error(result.error.message || '保存到期日期失败')
    ElMessage.success(expiryDate ? '到期日期已保存，将在到期前 7 天推送提醒' : '已设置为永久有效')
    await fetchProfiles(false)
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '保存到期日期失败'))
  } finally {
    expirySaving.value = null
  }
}

// 删除 profile（需要输入 ICCID 后 4 位确认）
async function deleteProfile(iccid: string, name: string, aidHex: string) {
  const last4 = iccid.slice(-4)
  const { value: input } = await ElMessageBox.prompt(
    `此操作不可逆！请输入 ICCID 后 4 位「${last4}」以确认删除 Profile「${name}」`,
    '⚠️ 删除 Profile',
    {
      confirmButtonText: '确认删除',
      cancelButtonText: '取消',
      inputPattern: new RegExp(`^${last4}$`),
      inputErrorMessage: `请输入 ${last4} 以确认`,
      inputPlaceholder: `输入 ${last4}`,
      type: 'error',
      confirmButtonClass: '!bg-red-600 !border-red-600 hover:!bg-red-700'
    }
  ).catch(() => ({ value: '' }))
  if (input !== last4) return

  deleting.value = iccid
  try {
    const result = await devicesService.deleteEsimProfile(props.deviceId, iccid, aidHex)
    if (!result.ok) throw new Error(result.error.message || '删除失败')
    showRecentSpaceDelta(aidHex, result.data.space_delta)
    const notice = describeDeleteResultNotice(result.data)
    if (notice.tone === 'warning') {
      ElMessage.warning(notice.message)
    } else {
      ElMessage.success(notice.message)
    }
    await fetchOverview(true)
  } catch (e: unknown) {
    ElMessage.error(errorMessage(e, '删除失败'))
  } finally {
    deleting.value = null
  }
}

// 下载新 profile（SSE 流式进度）
async function downloadProfile() {
  const { smdp, matchingId, confirmationCode, aidHex, imei } = downloadForm.value
  const targetAidHex = aidHex || pickNextDownloadAid(chipInfo.value, '')
  if (!smdp) {
    ElMessage.warning('请输入 SM-DP+ 地址')
    return
  }

  downloadSessionId.value++
  downloading.value = true
  downloadProgress.value = 0
  downloadMsg.value = '正在连接...'
  downloadError.value = ''

  const params = new URLSearchParams({ smdp })
  if (matchingId) params.set('matching_id', matchingId)
  if (confirmationCode) params.set('confirmation_code', confirmationCode)
  if (targetAidHex) params.set('aid_hex', targetAidHex)
  if (imei.trim()) params.set('imei', imei.trim())

  const base = api.defaults.baseURL || ''
  const url = `${base}/devices/${props.deviceId}/esim/actions/download?${params}`
  const token = localStorage.getItem('token') || ''
  const controller = new AbortController()

  try {
    const res = await fetch(url, {
      method: 'GET',
      headers: { Authorization: `Bearer ${token}`, Accept: 'text/event-stream' },
      signal: controller.signal
    })
    if (!res.ok) {
      const text = await res.text()
      throw new Error(text || `HTTP ${res.status}`)
    }
    if (!res.body) throw new Error('No stream body')

    const reader = res.body.getReader()
    const decoder = new TextDecoder('utf-8')
    let buffer = ''

    outer: while (true) {
      const { value, done } = await reader.read()
      if (done) break
      buffer += decoder.decode(value, { stream: true })

      while (true) {
        const nl = buffer.indexOf('\n')
        if (nl < 0) break
        let line = buffer.slice(0, nl)
        buffer = buffer.slice(nl + 1)
        if (line.endsWith('\r')) line = line.slice(0, -1)
        if (!line.startsWith('data:')) continue

        const payload = line.slice('data:'.length).trim()
        try {
          const evt = JSON.parse(payload) as { step: string; msg: string; pct: number; code?: string; warning?: string; space_delta?: EsimSpaceDelta }
          if (evt.step === 'error') {
            downloadError.value = evt.code === 'euicc_insufficient_memory'
              ? 'eUICC 安装 profile 时空间不足，请删除未使用的 profile 后重试。'
              : evt.msg
            break outer
          }
          downloadProgress.value = evt.pct
          downloadMsg.value = evt.msg
          if (evt.step === 'done') {
            showRecentSpaceDelta(targetAidHex, evt.space_delta)
            const notice = describeDownloadTerminalNotice(evt)
            if (notice.tone === 'warning') {
              ElMessage.warning(notice.message)
            } else {
              ElMessage.success(notice.message)
            }
            downloadForm.value = { smdp: '', matchingId: '', confirmationCode: '', aidHex: targetAidHex, imei: '' }
            await fetchOverview(true)
            break outer
          }
        } catch { /* 非 JSON 行，忽略 */ }
      }
    }
  } catch (e: unknown) {
    if (!downloadError.value) {
      downloadError.value = errorMessage(e, '下载失败')
    }
  } finally {
    downloading.value = false
  }
}

// 切换设备或改换 tab 时重新获取数据
watch(
  [() => props.deviceId, () => props.isActive],
  ([newId, newActive]) => {
    if (fetchAbortController) {
      fetchAbortController.abort()
    }
    if (!newId || !newActive) return

    clearRecentSpaceDelta()
    chipInfo.value = null
    profiles.value = []
    expiryDrafts.value = {}
    downloadForm.value.aidHex = ''
    downloadForm.value.imei = ''
    fetchOverview()
  },
  { immediate: true }
)

onBeforeUnmount(() => {
  clearRecentSpaceDelta()
  if (fetchAbortController) {
    fetchAbortController.abort()
  }
})
</script>

<template>
  <div class="space-y-5">
    <div v-if="loading" class="space-y-4">
      <div class="ui-panel-muted p-4 relative overflow-hidden esim-loading-hero">
        <div class="flex items-center gap-3">
          <div class="w-10 h-10 rounded-xl esim-orbit flex items-center justify-center text-white text-xs font-bold">
            ESIM
          </div>
          <div class="space-y-2 flex-1">
            <div class="h-4 w-44 rounded-md esim-skeleton-line" />
            <div class="h-3 w-64 rounded-md esim-skeleton-line esim-skeleton-line-soft" />
          </div>
          <div class="flex items-center gap-1.5">
            <span class="esim-dot" />
            <span class="esim-dot" />
            <span class="esim-dot" />
          </div>
        </div>
        <div class="esim-skeleton-shimmer" />
      </div>

      <div class="ui-panel-muted p-4 space-y-3">
        <div class="h-3 w-28 rounded-md esim-skeleton-line" />
        <div class="space-y-2">
          <div class="h-10 rounded-xl esim-skeleton-line" />
          <div class="h-10 rounded-xl esim-skeleton-line esim-skeleton-line-soft" />
          <div class="h-10 rounded-xl esim-skeleton-line" />
        </div>
      </div>
    </div>

    <template v-else>
      <!-- 芯片信息 -->
      <div v-if="chipInfo" class="ui-panel-muted p-4 relative">
      <div class="flex items-center justify-between gap-3 mb-3">
        <div class="flex items-center gap-3 min-w-0">
          <div class="w-9 h-9 rounded-xl bg-gradient-to-br from-emerald-500 to-teal-600 text-white text-xs font-bold flex items-center justify-center shadow-lg shadow-emerald-500/25">
            ESIM
          </div>
          <div>
            <div class="text-base font-bold text-gray-900 dark:text-white">
              {{ chipInfo.sku_name || 'eUICC' }}
            </div>
            <div class="text-xs text-gray-500 dark:text-gray-400 font-mono">
              <template v-if="chipInfo.firmware">固件 {{ chipInfo.firmware }}</template>
              <template v-if="chipInfo.serial_number">
                · SN: <span class="transition-all" :class="{ 'blur-sm select-none': !showSensitive }">{{ chipInfo.serial_number }}</span>
              </template>
            </div>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <el-tooltip content="手动刷新" placement="top">
            <el-button circle text :loading="profilesRefreshing" @click="fetchOverview(true)">
              <el-icon v-if="shouldShowEsimRefreshIcon(profilesRefreshing)" size="18"><ArrowSync24Regular /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip content="当前通知" placement="top">
            <el-button circle text :loading="notificationsLoading" @click="openNotificationsDialog">
              <el-icon v-if="shouldShowEsimNotificationIcon(notificationsLoading)" size="18"><Alert24Regular /></el-icon>
            </el-button>
          </el-tooltip>
          <el-tooltip :content="showSensitive ? '隐藏敏感信息' : '显示敏感信息'" placement="top">
            <el-button circle text @click="showSensitive = !showSensitive">
              <el-icon size="18">
                <Eye24Regular v-if="showSensitive" />
                <EyeOff24Regular v-else />
              </el-icon>
            </el-button>
          </el-tooltip>
        </div>
      </div>
    </div>

      <!-- 按 eUICC 分组的 Profiles -->
      <div v-for="(group, gi) in profiles" :key="group.aid_hex || group.eid || ('group-' + gi)" class="ui-panel-muted overflow-hidden">
      <!-- eUICC 头部 -->
      <div class="px-4 py-3 border-b border-gray-100 dark:border-white/10">
        <div class="flex items-center justify-between">
          <div>
            <span class="text-sm font-bold text-gray-900 dark:text-white">eUICC #{{ gi + 1 }}</span>
            <span class="text-xs text-gray-400 font-mono ml-2 transition-all" :class="{ 'blur-sm select-none': !showSensitive }">
              {{ group.eid }}
            </span>
          </div>
          <div v-if="chipInfo?.eids" class="text-xs text-gray-500">
            <template v-for="eid in chipInfo.eids" :key="eid.eid">
              <span v-if="eid.eid === group.eid" class="inline-flex flex-col items-end gap-1">
                <span class="inline-flex items-center gap-1">
                  <span class="w-2 h-2 rounded-full" :class="eid.free_nvram_bytes > 100000 ? 'bg-green-500' : 'bg-yellow-500'" />
                  可用 {{ eid.free_nvram }}
                </span>
                <span v-if="recentSpaceDelta && normalizeAidHex(group.aid_hex) === recentSpaceDelta.aidHex" class="text-[11px] text-emerald-600 dark:text-emerald-400">
                  {{ recentSpaceDelta.message }}
                </span>
              </span>
            </template>
          </div>
        </div>
        <!-- PKI 信息行 -->
        <template v-if="chipInfo?.eids">
          <template v-for="eid in chipInfo.eids" :key="'pki-' + eid.eid">
            <div v-if="eid.eid === group.eid && (eid.manufacturer || eid.certificates?.length || eid.default_smdp_address || eid.root_ds_address || eid.sas_accreditation_number || eid.info_source)" class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-gray-400 dark:text-gray-500">
              <span v-if="eid.manufacturer" class="inline-flex items-center gap-1">
                <span class="text-[10px]">生产商:</span> {{ eid.manufacturer }}
              </span>
              <span v-if="eid.certificates?.length" class="inline-flex items-center gap-1">
                <span class="text-[10px]">证书:</span> {{ eid.certificates.join(' · ') }}
              </span>
              <span v-if="eid.default_smdp_address" class="inline-flex items-center gap-1">
                <span class="text-[10px]">Default SM-DP+:</span> {{ eid.default_smdp_address }}
              </span>
              <span v-if="eid.root_ds_address" class="inline-flex items-center gap-1">
                <span class="text-[10px]">Root SM-DS:</span> {{ eid.root_ds_address }}
              </span>
              <span v-if="eid.sas_accreditation_number" class="inline-flex items-center gap-1">
                <span class="text-[10px]">SAS:</span> {{ eid.sas_accreditation_number }}
              </span>
              <span v-if="eid.info_source" class="inline-flex items-center gap-1">
                <span class="text-[10px]">来源:</span> {{ eid.info_source }}
              </span>
            </div>
          </template>
        </template>
      </div>

      <!-- Profile 列表 -->
      <div v-if="group.profiles?.length === 0" class="p-4 text-sm text-gray-400">
        暂无 Profile
      </div>
      <div v-else class="divide-y divide-gray-100 dark:divide-white/10">
        <div
          v-for="p in group.profiles"
          :key="p.iccid"
          class="px-4 py-3 flex items-center justify-between gap-3 hover:bg-gray-50/50 dark:hover:bg-white/5 transition-colors"
        >
          <div class="min-w-0 flex-1">
            <!-- 正常显示模式 -->
            <template v-if="renaming !== p.iccid">
              <div class="flex items-center gap-2">
                <span class="w-2 h-2 rounded-full flex-shrink-0" :class="p.state === 1 ? 'bg-green-500' : 'bg-gray-300 dark:bg-gray-600'" />
                <span class="font-medium text-sm text-gray-900 dark:text-white truncate">{{ p.name || p.iccid }}</span>
                <el-tag size="small" :type="p.state === 1 ? 'success' : 'info'" class="flex-shrink-0">
                  {{ p.state_text }}
                </el-tag>
              </div>
              <div class="text-xs text-gray-500 dark:text-gray-400 mt-0.5 ml-4 flex flex-wrap items-center gap-x-2 gap-y-1 transition-all">
                <span>{{ p.service_provider_name }}</span>
                <span :class="{ 'blur-sm select-none': !showSensitive }">{{ p.iccid }}</span>
              </div>
              <div v-if="p.note" class="text-xs text-blue-600 dark:text-blue-300 mt-1 ml-4 whitespace-pre-wrap break-words">
                备注：{{ p.note }}
              </div>
            </template>
            <!-- 编辑名称模式 -->
            <template v-else>
              <div class="flex items-center gap-2">
                <el-input
                  v-model="renameValue"
                  size="small"
                  placeholder="输入新名称"
                  @keyup.enter="saveRename(p.iccid, group.aid_hex)"
                  @keyup.escape="cancelRename"
                  autofocus
                  class="!w-52"
                />
                <el-button size="small" type="primary" @click="saveRename(p.iccid, group.aid_hex)" class="!border-0">保存</el-button>
                <el-button size="small" @click="cancelRename" class="!border-0">取消</el-button>
              </div>
            </template>
            <div v-if="noteEditing === p.iccid" class="mt-2 ml-4 flex items-center gap-2 max-w-xl">
              <el-input
                v-model="noteValue"
                size="small"
                maxlength="500"
                show-word-limit
                placeholder="例如：荷兰 Vodafone 主卡"
                @keyup.enter="saveProfileNote(p.iccid)"
                @keyup.escape="cancelNoteEdit"
              />
              <el-button size="small" type="primary" :loading="noteSaving === p.iccid" @click="saveProfileNote(p.iccid)" class="!border-0">保存</el-button>
              <el-button size="small" @click="cancelNoteEdit" class="!border-0">取消</el-button>
            </div>
          </div>

          <!-- 操作按钮 -->
          <div v-if="renaming !== p.iccid" class="flex items-center gap-2 flex-shrink-0 flex-wrap justify-end">
            <el-button
              v-if="p.state !== 1"
              size="small"
              type="success"
              :loading="switching === p.iccid"
              @click="switchProfile(p.iccid, p.state, group.aid_hex)"
              plain
            >
              切换
            </el-button>
            <el-button
              size="small"
              type="primary"
              @click="startRename(p.iccid, p.name)"
              plain
            >
              改名
            </el-button>
            <el-button
              size="small"
              type="info"
              @click="startNoteEdit(p.iccid, p.note)"
              plain
            >
              备注
            </el-button>
            <el-button
              size="small"
              type="danger"
              :loading="deleting === p.iccid"
              @click="deleteProfile(p.iccid, p.name, group.aid_hex)"
              plain
            >
              删除
            </el-button>
          </div>
        </div>
      </div>
    </div>

      <el-dialog
        v-model="notificationsDialogOpen"
        title="当前通知列表"
        :width="notificationDialogWidth()"
        class="glass-modal"
      >
        <div v-if="notificationsLoading" class="py-10 text-sm text-center text-gray-400">正在加载通知...</div>
        <div v-else-if="notifications.length === 0" class="py-10 text-sm text-center text-gray-400">当前没有可展示的通知</div>
        <div v-else class="space-y-2 max-h-[420px] overflow-auto pr-1">
          <div
            v-for="item in notifications"
            :key="item.sequence_number"
            :class="notificationListItemLayoutClass()"
          >
            <div class="min-w-0 flex-1 space-y-1">
              <div class="flex items-center gap-2 text-sm font-medium text-gray-900 dark:text-white">
                <span>#{{ item.sequence_number }}</span>
                <el-tag size="small" type="info">{{ formatEsimNotificationEvent(item.event) }}</el-tag>
              </div>
              <div :class="notificationMetaContainerClass()">
                <div v-if="item.iccid" :class="notificationMetaItemClass()">
                  <span class="mr-1 text-gray-400 dark:text-gray-500">ICCID</span>
                  <span class="break-all">{{ item.iccid }}</span>
                </div>
                <div v-if="item.address" :class="notificationMetaItemClass()">
                  <span class="mr-1 text-gray-400 dark:text-gray-500">地址</span>
                  <span class="break-all">{{ item.address }}</span>
                </div>
                <div v-if="item.aid_hex" :class="notificationMetaItemClass()">
                  <span class="mr-1 text-gray-400 dark:text-gray-500">AID</span>
                  <span class="break-all">{{ item.aid_hex }}</span>
                </div>
              </div>
            </div>
            <el-button
              size="small"
              type="primary"
              class="self-start sm:self-auto"
              :disabled="!item.can_retry"
              :loading="retryingNotificationSequence === item.sequence_number"
              @click="retryNotification(item)"
            >
              重发
            </el-button>
          </div>
        </div>
      </el-dialog>

      <!-- 到期通知中心 -->
      <div v-if="chipInfo" class="ui-panel-muted p-4">
        <div class="flex flex-col sm:flex-row sm:items-start sm:justify-between gap-3 mb-4">
          <div class="flex items-start gap-2">
            <div class="w-7 h-7 rounded-lg bg-amber-50 dark:bg-amber-500/10 flex items-center justify-center text-amber-600 dark:text-amber-400 flex-shrink-0">
              <el-icon size="16"><Alert24Regular /></el-icon>
            </div>
            <div>
              <div class="text-sm font-bold text-gray-900 dark:text-white">到期通知中心</div>
              <div class="text-[11px] text-gray-400 dark:text-gray-500">设置每个号码的到期日期，系统会在到期前 7 天通过已启用的推送渠道提醒</div>
            </div>
          </div>
          <el-tag type="info" effect="plain" size="small">未设置日期 = 永久有效</el-tag>
        </div>

        <div v-if="expiryProfiles.length === 0" class="rounded-xl border border-dashed border-gray-200 dark:border-white/10 px-4 py-6 text-center text-xs text-gray-400">
          当前没有可设置到期日期的 eSIM Profile
        </div>
        <div v-else class="space-y-2">
          <div
            v-for="profile in expiryProfiles"
            :key="`expiry-${profile.iccid}`"
            class="rounded-xl border border-gray-100 dark:border-white/10 bg-white/60 dark:bg-white/[0.03] px-3 py-3 flex flex-col lg:flex-row lg:items-center gap-3"
          >
            <div class="min-w-0 flex-1">
              <div class="flex items-center gap-2 min-w-0">
                <span class="w-2 h-2 rounded-full flex-shrink-0" :class="profile.state === 1 ? 'bg-green-500' : 'bg-gray-300 dark:bg-gray-600'" />
                <span class="text-sm font-medium text-gray-900 dark:text-white truncate">{{ profile.name || profile.iccid }}</span>
                <el-tag size="small" :type="profileExpiryValue(profile) ? 'warning' : 'info'" effect="plain" class="flex-shrink-0">
                  {{ profileExpiryValue(profile) ? '已设置' : '永久有效' }}
                </el-tag>
              </div>
              <div class="mt-1 ml-4 text-xs text-gray-500 dark:text-gray-400 flex flex-wrap gap-x-3 gap-y-1">
                <span>号码：{{ profile.phone_number || '未读取到号码' }}</span>
                <span class="font-mono transition-all" :class="{ 'blur-sm select-none': !showSensitive }">ICCID：{{ profile.iccid }}</span>
              </div>
              <div v-if="profile.note" class="mt-1 ml-4 text-xs text-blue-600 dark:text-blue-300 truncate" :title="profile.note">
                备注：{{ profile.note }}
              </div>
              <div class="mt-1 ml-4 text-[11px] text-gray-400 dark:text-gray-500">
                {{ profileExpirySummary(profile) }}
              </div>
            </div>
            <div class="flex flex-col sm:flex-row sm:items-center gap-2 lg:w-auto">
              <el-date-picker
                :model-value="profileExpiryValue(profile)"
                type="date"
                value-format="YYYY-MM-DD"
                format="YYYY-MM-DD"
                clearable
                placeholder="永久有效"
                class="!w-full sm:!w-44"
                @update:model-value="setExpiryDraft(profile.iccid, $event)"
              />
              <el-button
                type="primary"
                plain
                :loading="expirySaving === profile.iccid"
                @click="saveProfileExpiry(profile.iccid)"
              >
                保存
              </el-button>
            </div>
          </div>
        </div>
      </div>

      <!-- 下载新 Profile -->
      <div v-if="chipInfo" class="ui-panel-muted p-4">
      <div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 mb-3">
        <div class="flex items-center gap-2">
          <div class="w-7 h-7 rounded-lg bg-indigo-50 dark:bg-indigo-500/10 flex items-center justify-center text-indigo-600 dark:text-indigo-400">
            <el-icon size="16"><Add24Regular /></el-icon>
          </div>
          <div>
            <div class="text-sm font-bold text-gray-900 dark:text-white">下载新 Profile</div>
            <div class="text-[11px] text-gray-400 dark:text-gray-500">上传 eSIM 二维码，自动读取 LPA 激活信息（手机端自动使用兼容识别；建议 JPG/PNG）</div>
          </div>
        </div>
        <div class="flex items-center gap-2">
          <input
            ref="qrInput"
            type="file"
            accept="image/jpeg,image/png,image/webp,image/heic,image/heif"
            class="hidden"
            @change="handleQrFileChange"
          />
          <el-button type="primary" plain :loading="qrScanning" :disabled="downloading" @click="openQrPicker">
            <el-icon><Add24Regular /></el-icon>
            上传二维码
          </el-button>
        </div>
      </div>
      <div v-if="qrScanError || qrScanNote" class="mb-3 rounded-lg border px-3 py-2 text-xs" :class="qrScanError ? 'border-red-200 bg-red-50 text-red-600 dark:border-red-500/30 dark:bg-red-500/10 dark:text-red-300' : 'border-amber-200 bg-amber-50 text-amber-700 dark:border-amber-500/30 dark:bg-amber-500/10 dark:text-amber-300'">
        {{ qrScanError || qrScanNote }}
      </div>
      <div class="grid grid-cols-1 lg:grid-cols-2 gap-3">
        <div class="space-y-1">
          <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">SM-DP+ 地址 *</div>
          <el-input v-model="downloadForm.smdp" placeholder="例如 rsp.truphone.com" />
        </div>
        <div class="space-y-1">
          <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">Matching ID</div>
          <el-input v-model="downloadForm.matchingId" placeholder="可选" />
        </div>
        <div class="space-y-1">
          <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">确认码</div>
          <el-input v-model="downloadForm.confirmationCode" placeholder="可选" />
        </div>
        <div class="space-y-1">
          <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">IMEI</div>
          <el-input v-model="downloadForm.imei" maxlength="15" placeholder="可选；留空由设备自动获取" />
        </div>
        <div class="space-y-1">
          <div class="text-[11px] font-bold text-gray-500 uppercase tracking-wider">目标 eUICC</div>
          <el-select v-model="downloadForm.aidHex" placeholder="选择目标 eUICC">
            <el-option
              v-for="(eid, ei) in (chipInfo?.eids || [])"
              :key="eid.aid"
              :label="`eUICC #${Number(ei) + 1} (...${eid.eid.slice(-4)}) — ${eid.free_nvram} 可用`"
              :value="eid.aid"
            />
          </el-select>
        </div>
      </div>
      <!-- 下载进度条 -->
      <div v-if="downloading || downloadError" class="mt-4 space-y-1.5">
        <el-progress
          :key="downloadSessionId"
          :percentage="downloadProgress"
          :status="downloadError ? 'exception' : downloadProgress >= 100 ? 'success' : undefined"
          :striped="downloading && downloadProgress < 100"
          :striped-flow="downloading && downloadProgress < 100"
          :duration="8"
          :stroke-width="10"
        />
        <div class="text-xs" :class="downloadError ? 'text-red-500' : 'text-gray-500 dark:text-gray-400'">
          {{ downloadError || downloadMsg }}
        </div>
      </div>

      <div class="flex justify-end mt-4">
        <el-button type="primary" :loading="downloading" :disabled="downloading" @click="downloadProfile" class="ui-esim-download-button !border-0">
          <el-icon><ArrowDownload24Regular /></el-icon>
          开始下载
        </el-button>
      </div>
    </div>

      <!-- 空状态 -->
      <EmptyState v-if="profiles.length === 0 && !chipInfo" title="未检测到 eUICC" subtitle="此SIM卡可能不支持 eUICC 功能" />
    </template>
  </div>
</template>

<style scoped>
.ui-esim-download-button {
  min-width: 132px;
}

.esim-loading-hero {
  min-height: 88px;
}

.esim-orbit {
  background: linear-gradient(135deg, #10b981, #0ea5a4);
  animation: esim-orbit 2.2s ease-in-out infinite;
}

.esim-skeleton-line {
  background: linear-gradient(90deg, rgba(148, 163, 184, 0.18), rgba(148, 163, 184, 0.34), rgba(148, 163, 184, 0.18));
  background-size: 200% 100%;
  animation: esim-shimmer 1.4s linear infinite;
}

.esim-skeleton-line-soft {
  opacity: 0.8;
  animation-duration: 1.9s;
}

.esim-skeleton-shimmer {
  position: absolute;
  inset: 0;
  pointer-events: none;
  background: linear-gradient(120deg, transparent 0%, rgba(255, 255, 255, 0.24) 45%, transparent 75%);
  transform: translateX(-130%);
  animation: esim-sweep 2.1s ease-in-out infinite;
}

.esim-dot {
  width: 7px;
  height: 7px;
  border-radius: 9999px;
  background: #14b8a6;
  opacity: 0.3;
  animation: esim-dot-bounce 1.1s ease-in-out infinite;
}

.esim-dot:nth-child(2) {
  animation-delay: 0.16s;
}

.esim-dot:nth-child(3) {
  animation-delay: 0.32s;
}

@keyframes esim-shimmer {
  0% { background-position: 200% 0; }
  100% { background-position: -200% 0; }
}

@keyframes esim-sweep {
  0% { transform: translateX(-130%); }
  100% { transform: translateX(130%); }
}

@keyframes esim-dot-bounce {
  0%, 80%, 100% { opacity: 0.3; transform: translateY(0); }
  40% { opacity: 1; transform: translateY(-2px); }
}

@keyframes esim-orbit {
  0%, 100% { transform: scale(1); box-shadow: 0 8px 18px rgba(16, 185, 129, 0.25); }
  50% { transform: scale(1.04); box-shadow: 0 10px 22px rgba(20, 184, 166, 0.35); }
}
</style>
