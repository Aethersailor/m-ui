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
    auth: {
      signIn: '管理员登录',
      username: '用户名',
      password: '密码',
      submit: '登录',
      signOut: '退出登录',
      failed: '用户名或密码错误。',
      rateLimited: '尝试次数过多，请稍后再试。',
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
    auth: {
      signIn: 'Administrator sign in',
      username: 'Username',
      password: 'Password',
      submit: 'Sign in',
      signOut: 'Sign out',
      failed: 'The username or password is incorrect.',
      rateLimited: 'Too many attempts. Try again later.',
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
