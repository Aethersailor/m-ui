import { createI18n } from 'vue-i18n'

const messages = {
  'zh-CN': {
    product: {
      name: 'm-ui',
      description: '轻量级 Mihomo 服务端 Listener 管理面板',
    },
    health: {
      title: '服务状态',
      ready: '后端已就绪',
      loading: '正在连接后端…',
      failed: '无法连接后端',
      version: '版本',
    },
    scope: {
      title: 'v0.1 范围',
      description: '单机 VLESS + TCP + REALITY + XTLS Vision 管理',
    },
  },
  'en-US': {
    product: {
      name: 'm-ui',
      description: 'A lightweight Mihomo server Listener administration panel',
    },
    health: {
      title: 'Service status',
      ready: 'Backend ready',
      loading: 'Connecting to backend…',
      failed: 'Backend unavailable',
      version: 'Version',
    },
    scope: {
      title: 'v0.1 scope',
      description: 'Single-host VLESS + TCP + REALITY + XTLS Vision management',
    },
  },
} as const

export const i18n = createI18n({
  legacy: false,
  locale: navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US',
  fallbackLocale: 'en-US',
  messages,
})
