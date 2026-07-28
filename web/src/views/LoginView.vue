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
import { useThemeStore } from '@/stores/theme'

const { locale, t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const theme = useThemeStore()
const username = ref('')
const password = ref('')
const attempted = ref(false)
const languageOptions = [
  { label: '简体中文', value: 'zh-CN' },
  { label: 'English', value: 'en-US' },
]

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
        :value="locale"
        :options="languageOptions"
        size="small"
        class="language-select"
        @update:value="locale = $event"
      />
      <NButton
        quaternary
        circle
        :aria-label="t('theme.toggle')"
        @click="theme.cycle"
      >
        {{ theme.dark ? '☀' : '◐' }}
      </NButton>
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
