import { createRouter, createWebHistory } from 'vue-router'

import { pinia } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'
import { usePreferencesStore } from '@/stores/preferences'
import { useThemeStore } from '@/stores/theme'
import LoginView from '@/views/LoginView.vue'
import SetupView from '@/views/SetupView.vue'

export const router = createRouter({
  history: createWebHistory(),
  routes: [
    {
      path: '/',
      redirect: { name: 'dashboard' },
    },
    {
      path: '/dashboard',
      name: 'dashboard',
      component: () => import('@/views/HomeView.vue'),
      meta: { section: 'dashboard' },
    },
    {
      path: '/listeners',
      name: 'listeners',
      component: () => import('@/views/ListenersView.vue'),
      meta: { section: 'listeners' },
    },
    {
      path: '/listeners/:id',
      name: 'listener-detail',
      component: () => import('@/views/ListenerDetailView.vue'),
      meta: { section: 'listeners' },
    },
    {
      path: '/config',
      name: 'config',
      component: () => import('@/views/ConfigView.vue'),
      meta: { section: 'config' },
    },
    {
      path: '/system',
      name: 'system',
      component: () => import('@/views/SystemView.vue'),
      meta: { section: 'system' },
    },
    {
      path: '/login',
      name: 'login',
      component: LoginView,
      meta: { public: true },
    },
    {
      path: '/setup',
      name: 'setup',
      component: SetupView,
      meta: { public: true },
    },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore(pinia)
  const setup = useSetupStore(pinia)
  const preferences = usePreferencesStore(pinia)
  const theme = useThemeStore(pinia)
  let setupAvailable = true
  try {
    await setup.initialize()
  } catch {
    setupAvailable = false
  }
  if (setupAvailable && setup.status) {
    preferences.initialize(setup.status.language_default)
  } else {
    preferences.initialize()
  }
  theme.initialize()
  await auth.initialize()
  if (setupAvailable && setup.required && to.name !== 'setup') {
    return { name: 'setup' }
  }
  if (setupAvailable && setup.complete && to.name === 'setup') {
    return auth.authenticated ? { name: 'dashboard' } : { name: 'login' }
  }
  if (!to.meta.public && !auth.authenticated) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && auth.authenticated) {
    return { name: 'dashboard' }
  }
  return true
})
