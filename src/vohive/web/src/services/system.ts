import { api } from '../stores/auth'
import { callService } from './http'

export type DocsLinks = {
  swagger_ui: string
  openapi_yaml: string
  openapi_json: string
}

export type UpdateInfo = {
  has_update: boolean
  current_version: string
  latest_version: string
  release_note: string
  is_docker: boolean
  migration_required: boolean
  supported: boolean
  channel: string
  proxy_id: string
  proxy_options: UpdateProxyOption[]
}

export type UpdateProxyOption = {
  id: string
  name: string
  description: string
}

export type UpdateState =
  | 'idle'
  | 'checking'
  | 'available'
  | 'downloading'
  | 'verifying'
  | 'backing_up'
  | 'applying'
  | 'restarting'
  | 'success'
  | 'failed'
  | 'rolled_back'

export type UpdateStatus = {
  state: UpdateState
  current_version: string
  target_version: string
  proxy_id?: string
  progress: number
  message: string
  error: string
  error_code: string
  backup_path?: string
  updated_at: string
}

export type SystemInfo = {
  version: string
  build_time: string
  commit: string
  config: string
  docs: DocsLinks
}

export type TelegramSettings = {
  enabled: boolean
  bot_token: string
  chat_id: number | null
  admin_id: number | null
  base_url: string
  proxy: string
}

export type FeishuSettings = {
  enabled: boolean
  app_id: string
  app_secret: string
  chat_ids: string[]
}

export type QQSettings = {
  enabled: boolean
  app_id: string
  app_secret: string
  group_ids: string
  direct_ids: string
}

export type WecomSettings = {
  enabled: boolean
  webhook_url: string
}

export type WebhookSettings = {
  enabled: boolean
  urls: string[]
  secret: string
  timeout_ms: number
  retry_max: number
  text_template: string
  headers: Record<string, string>
}

export type BarkSettings = {
  enabled: boolean
  urls: string[]
  group: string
  icon: string
  level: string
}

export type EmailSettings = {
  enabled: boolean
  use_ssl: boolean
  smtp_host: string
  smtp_port: number
  username: string
  password: string
  from_address: string
  to_addresses: string[]
}

export type PushplusSettings = {
  enabled: boolean
  token: string
  topic: string
  channel: string
}

export type NotificationsSettingsResponse = {
  telegram?: Partial<TelegramSettings>
  feishu?: Partial<FeishuSettings>
  qq?: Partial<QQSettings>
  wecom?: Partial<WecomSettings>
  email?: Partial<EmailSettings>
  pushplus?: Partial<PushplusSettings>
  webhook?: Partial<WebhookSettings>
  bark?: Partial<BarkSettings>
}

export type SaveNotificationsPayload = {
  telegram: {
    enabled: boolean
    bot_token: string
    chat_id: number
    admin_id: number
    base_url: string
    proxy: string
  }
  feishu: {
    enabled: boolean
    app_id: string
    app_secret: string
    chat_ids: string[]
  }
  qq: {
    enabled: boolean
    app_id: string
    app_secret: string
    group_ids: string
    direct_ids: string
  }
  wecom: {
    enabled: boolean
    webhook_url: string
  }
  email: {
    enabled: boolean
    use_ssl: boolean
    smtp_host: string
    smtp_port: number
    username: string
    password: string
    from_address: string
    to_addresses: string[]
  }
  pushplus: {
    enabled: boolean
    token: string
    topic: string
    channel: string
  }
  webhook: {
    enabled: boolean
    urls: string[]
    secret: string
    timeout_ms: number
    retry_max: number
    text_template: string
    headers?: Record<string, string>
  }
  bark: {
    enabled: boolean
    urls: string[]
    group: string
    icon: string
    level: string
  }
}

export type SaveNotificationsResponse = {
  applied?: boolean
  warning?: string
}

export type TestWebhookPayload = {
  enabled: boolean
  urls: string[]
  secret: string
  timeout_ms: number
  retry_max: number
  text_template: string
  headers?: Record<string, string>
}

export type TestWebhookResponse = {
  ok: boolean
  message: string
  failed_urls?: string[]
}

export type TestBarkPayload = {
  enabled: boolean
  urls: string[]
  group: string
  icon: string
  level: string
}

export type TestBarkResponse = {
  ok: boolean
  message: string
  failed_urls?: string[]
}

export type TestEmailPayload = {
  enabled: boolean
  use_ssl: boolean
  smtp_host: string
  smtp_port: number
  username: string
  password: string
  from_address: string
  to_addresses: string[]
}

export type TestEmailResponse = {
  ok: boolean
  message: string
}

export type TestWecomPayload = {
  enabled: boolean
  webhook_url: string
}

export type TestWecomResponse = {
  ok: boolean
  message: string
}

export const systemService = {
  getInfo() {
    return callService(async () => {
      const res = await api.get('/system/info')
      return res.data as SystemInfo
    })
  },
  changePassword(payload: { old_password: string; new_password: string; confirm_password: string }) {
    return callService(async () => {
      await api.post('/settings/password', payload)
      return true
    })
  },
  getNotifications() {
    return callService(async () => {
      const res = await api.get('/settings/notifications')
      return (res.data || {}) as NotificationsSettingsResponse
    })
  },
  saveNotifications(payload: SaveNotificationsPayload) {
    return callService(async () => {
      const res = await api.put<SaveNotificationsResponse>('/settings/notifications', payload)
      return {
        applied: res.data?.applied,
        warning: res.data?.warning
      }
    })
  },
  testWebhook(payload: TestWebhookPayload) {
    return callService(async () => {
      const res = await api.post<TestWebhookResponse>('/settings/notifications/webhook/test', payload)
      return res.data
    })
  },
  testBark(payload: TestBarkPayload) {
    return callService(async () => {
      const res = await api.post<TestBarkResponse>('/settings/notifications/bark/test', payload)
      return res.data
    })
  },
  testEmail(payload: TestEmailPayload) {
    return callService(async () => {
      const res = await api.post<TestEmailResponse>('/settings/notifications/email/test', payload)
      return res.data
    })
  },
  testWecom(payload: TestWecomPayload) {
    return callService(async () => {
      const res = await api.post<TestWecomResponse>('/settings/notifications/wecom/test', payload)
      return res.data
    })
  },
  getUpdateProxies() {
    return callService(async () => {
      const res = await api.get<UpdateProxyOption[]>('/system/update/proxies')
      return res.data
    })
  },
  checkUpdate(proxyID = 'auto', proxyURL = '') {
    return callService(async () => {
      const res = await api.get<UpdateInfo>('/system/update/check', {
        params: { proxy_id: proxyID, ...(proxyURL ? { proxy_url: proxyURL } : {}) }
      })
      return res.data
    })
  },
  applyUpdate(proxyID = 'auto', proxyURL = '') {
    return callService(async () => {
      const res = await api.post<UpdateStatus>('/system/update/apply', {
        proxy_id: proxyID,
        ...(proxyURL ? { proxy_url: proxyURL } : {})
      })
      return res.data
    })
  },
  getUpdateStatus() {
    return callService(async () => {
      const res = await api.get<UpdateStatus>('/system/update/status')
      return res.data
    })
  }
}
