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
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'
import { usePreferencesStore } from '@/stores/preferences'
import { useThemeStore } from '@/stores/theme'
import type { LanguageMode } from '@/utils/preferences'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const preferences = usePreferencesStore()
const theme = useThemeStore()
const username = ref('')
const password = ref('')
const attempted = ref(false)
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

preferences.initialize()
theme.initialize()

const errorMessage = computed(() => {
  if (!attempted.value || !auth.errorCode) {
    return ''
  }
  if (auth.errorCode === 'LOGIN_RATE_LIMITED') {
    return t('auth.rateLimited', {
      seconds: auth.errorRetryAfter || 1,
    })
  }
  return t('auth.failed')
})

async function submit() {
  attempted.value = true
  try {
    await auth.login(username.value, password.value)
    password.value = ''
    const redirect =
      typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    await router.replace(redirect)
  } catch {
    // The store exposes only a non-differentiating safe message.
  }
}
</script>

<template>
  <main class="login-page">
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

      <NCard :bordered="false" class="login-card">
        <NText tag="h2" class="login-card-title">
          {{ t('auth.signIn') }}
        </NText>
        <NAlert
          v-if="errorMessage"
          type="error"
          :title="errorMessage"
          class="login-alert"
        />
        <NForm size="large" @submit.prevent="submit">
          <NFormItem :label="t('auth.username')">
            <NInput
              v-model:value="username"
              autocomplete="username"
              autofocus
              :disabled="auth.loading"
            />
          </NFormItem>
          <NFormItem :label="t('auth.password')">
            <NInput
              v-model:value="password"
              type="password"
              autocomplete="current-password"
              show-password-on="click"
              :disabled="auth.loading"
            />
          </NFormItem>
          <NButton
            type="primary"
            attr-type="submit"
            block
            size="large"
            :loading="auth.loading"
            :disabled="!username || !password"
          >
            {{ t('auth.submit') }}
          </NButton>
        </NForm>
        <NText depth="3" class="secure-note">
          <span aria-hidden="true">⌁</span>
          {{ t('auth.secureNote') }}
        </NText>
      </NCard>
    </section>
  </main>
</template>
