<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NText,
} from 'naive-ui'
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { completeSetup } from '@/api/setup'
import { useAuthStore } from '@/stores/auth'
import { usePreferencesStore } from '@/stores/preferences'
import { useSetupStore } from '@/stores/setup'
import { useThemeStore } from '@/stores/theme'
import type { LanguageMode } from '@/utils/preferences'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const preferences = usePreferencesStore()
const setup = useSetupStore()
const theme = useThemeStore()
const token = ref('')
const username = ref('admin')
const password = ref('')
const confirmation = ref('')
const formError = ref('')
const loading = ref(false)

const languageOptions = computed(() => [
  { label: t('language.auto'), value: 'auto' as LanguageMode },
  { label: t('language.chinese'), value: 'zh-CN' as LanguageMode },
  { label: t('language.english'), value: 'en-US' as LanguageMode },
])
const themeOptions = computed(() => [
  { label: t('theme.auto'), value: 'auto' as const },
  { label: t('theme.light'), value: 'light' as const },
  { label: t('theme.dark'), value: 'dark' as const },
])
const errorMessage = computed(() => {
  if (formError.value === 'PASSWORD_MISMATCH') {
    return t('auth.passwordMismatch')
  }
  if (formError.value === 'MISSING_TOKEN') {
    return t('setup.tokenMissing')
  }
  if (formError.value === 'SETUP_ALREADY_COMPLETED') {
    return t('setup.completed')
  }
  if (formError.value || setup.errorCode) {
    return t('setup.unavailable')
  }
  return ''
})
const canSubmit = computed(
  () => Boolean(token.value && username.value && password.value && confirmation.value),
)

onMounted(() => {
  preferences.initialize()
  theme.initialize()
  const fragment = window.location.hash
  const params = new URLSearchParams(fragment.replace(/^#/, ''))
  token.value = params.get('token') ?? ''
  if (fragment) {
    window.history.replaceState(
      null,
      document.title,
      `${window.location.pathname}${window.location.search}`,
    )
  }
  if (!token.value) {
    formError.value = 'MISSING_TOKEN'
  }
})

async function submit() {
  formError.value = ''
  if (!token.value) {
    formError.value = 'MISSING_TOKEN'
    return
  }
  if (password.value !== confirmation.value) {
    formError.value = 'PASSWORD_MISMATCH'
    return
  }
  loading.value = true
  try {
    const response = await completeSetup(token.value, username.value, password.value)
    auth.acceptCredentials(response)
    password.value = ''
    confirmation.value = ''
    await router.replace({ name: 'dashboard' })
  } catch (error) {
    const code = error instanceof Error && 'code' in error
      ? String((error as { code?: string }).code ?? '')
      : ''
    formError.value = code || 'REQUEST_FAILED'
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <main class="login-page setup-page">
    <div class="login-orb login-orb-one" />
    <div class="login-orb login-orb-two" />
    <div class="login-toolbar">
      <NSelect
        :value="preferences.languageMode"
        :options="languageOptions"
        size="small"
        class="language-select"
        :aria-label="t('language.label')"
        @update:value="preferences.setLanguageMode"
      />
      <NSelect
        :value="theme.mode"
        :options="themeOptions"
        size="small"
        class="theme-select"
        :aria-label="t('theme.label')"
        @update:value="theme.setMode"
      />
    </div>

    <section class="login-shell">
      <header class="login-brand">
        <span class="brand-mark login-mark">m</span>
        <NText tag="h1" class="login-title">{{ t('product.name') }}</NText>
        <NText depth="3" class="login-description">
          {{ t('product.description') }}
        </NText>
      </header>

      <NCard :bordered="false" class="login-card setup-card">
        <NText tag="h2" class="login-card-title">
          {{ t('setup.title') }}
        </NText>
        <NText depth="3" class="setup-description">
          {{ t('setup.description') }}
        </NText>
        <NAlert
          v-if="errorMessage"
          type="error"
          :title="errorMessage"
          class="login-alert"
          role="alert"
        />
        <NForm size="large" @submit.prevent="submit">
          <NFormItem :label="t('auth.username')">
            <NInput
              v-model:value="username"
              autocomplete="username"
              autofocus
              :disabled="loading"
            />
          </NFormItem>
          <NFormItem :label="t('auth.password')" :show-feedback="false">
            <NInput
              v-model:value="password"
              type="password"
              autocomplete="new-password"
              show-password-on="click"
              :disabled="loading"
            />
          </NFormItem>
          <NText depth="3" class="setup-hint">{{ t('setup.passwordHint') }}</NText>
          <NFormItem :label="t('setup.confirmPassword')">
            <NInput
              v-model:value="confirmation"
              type="password"
              autocomplete="new-password"
              show-password-on="click"
              :disabled="loading"
            />
          </NFormItem>
          <NButton
            type="primary"
            attr-type="submit"
            block
            size="large"
            :loading="loading"
            :disabled="!canSubmit"
          >
            {{ t('setup.submit') }}
          </NButton>
        </NForm>
        <NText depth="3" class="setup-hint setup-token-hint">
          {{ t('setup.tokenHint') }}
        </NText>
      </NCard>
    </section>
  </main>
</template>
