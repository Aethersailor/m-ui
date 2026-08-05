<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NInput,
  NText,
} from 'naive-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { completeSetup } from '@/api/setup'

import AppearancePreferences from '@/components/AppearancePreferences.vue'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const setup = useSetupStore()
const username = ref('admin')
const password = ref('')
const confirmation = ref('')
const formError = ref('')
const loading = ref(false)

const errorMessage = computed(() => {
  if (formError.value === 'PASSWORD_MISMATCH') {
    return t('auth.passwordMismatch')
  }
  if (formError.value === 'SETUP_ALREADY_COMPLETED') {
    return t('setup.completed')
  }
  if (formError.value === 'SETUP_RATE_LIMITED') {
    return t('setup.rateLimited')
  }
  if (formError.value === 'SETUP_TRANSPORT_NOT_ALLOWED') {
    return t('setup.transportNotAllowed')
  }
  if (formError.value === 'PASSWORD_POLICY_FAILED') {
    return t('setup.passwordPolicy')
  }
  if (formError.value || setup.errorCode) {
    return t('setup.unavailable')
  }
  return ''
})
const canSubmit = computed(
  () => Boolean(username.value && password.value && confirmation.value),
)

async function submit() {
  formError.value = ''
  if (password.value !== confirmation.value) {
    formError.value = 'PASSWORD_MISMATCH'
    return
  }
  loading.value = true
  try {
    const response = await completeSetup(username.value, password.value)
    auth.acceptCredentials(response)
    setup.markComplete()
    password.value = ''
    confirmation.value = ''
    await router.replace({ name: 'onboarding' })
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
          <NText depth="3" class="setup-hint">{{ t('setup.usernameHint') }}</NText>
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
        <NText depth="3" class="setup-hint setup-first-visitor-hint">
          {{ t('setup.firstVisitorHint') }}
        </NText>
        <AppearancePreferences compact class="login-appearance" />
      </NCard>
    </section>
  </main>
</template>
