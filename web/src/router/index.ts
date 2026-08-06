import { createRouter, createWebHistory } from 'vue-router'

import { pinia } from '@/stores'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'
import { usePreferencesStore } from '@/stores/preferences'
import { useThemeStore } from '@/stores/theme'
import LoginView from '@/views/LoginView.vue'
import InitializationErrorView from '@/views/InitializationErrorView.vue'
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
      path: '/nodes',
      name: 'nodes',
      component: () => import('@/views/ListenersView.vue'),
      meta: { section: 'nodes' },
    },
    {
      path: '/nodes/new',
      name: 'node-create',
      component: () => import('@/views/ListenerDetailView.vue'),
      meta: { section: 'nodes' },
    },
    {
      path: '/nodes/:id',
      name: 'node-detail',
      component: () => import('@/views/ListenerDetailView.vue'),
      meta: { section: 'nodes' },
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
    {
      path: '/onboarding',
      name: 'onboarding',
      component: () => import('@/views/OnboardingView.vue'),
      meta: { section: 'nodes' },
    },
    {
      path: '/initialization-error',
      name: 'initialization-error',
      component: InitializationErrorView,
      meta: { public: true },
    },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore(pinia)
  const setup = useSetupStore(pinia)
  const preferences = usePreferencesStore(pinia)
  const theme = useThemeStore(pinia)
  let setupRequestFailed = false
  try {
    await setup.initialize()
  } catch {
    setupRequestFailed = true
  }
  const setupAvailable = !setupRequestFailed && !setup.errorCode
  if (!setupAvailable) {
    preferences.initialize()
    theme.initialize()
    if (to.name !== 'initialization-error') {
      return { name: 'initialization-error' }
    }
    return true
  }
  if (setupAvailable && setup.status) {
    preferences.initialize(setup.status.language_default)
  } else {
    preferences.initialize()
  }
  theme.initialize()
  if (setupAvailable && setup.required) {
    if (to.name !== 'setup') {
      return { name: 'setup' }
    }
    // A fresh instance cannot have an authenticated session yet. Avoid an
    // expected 401 from /auth/me so the one-time setup page remains a clean,
    // self-contained public flow.
    return true
  }
  await auth.initialize()
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
