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
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { completeSetup } from '@/api/setup'

import AppearancePreferences from '@/components/AppearancePreferences.vue'
import { acceptSetupTokenInput, getSetupToken } from '@/setup-token'
import { useAuthStore } from '@/stores/auth'
import { useSetupStore } from '@/stores/setup'

const { t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const setup = useSetupStore()
const token = ref('')
const tokenInput = ref('')
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
  if (formError.value === 'SETUP_AUTHORIZATION_FAILED') {
    return t('setup.authorizationFailed')
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
  () => Boolean(token.value && username.value && password.value && confirmation.value),
)

onMounted(() => {
  token.value = getSetupToken()
})

function acceptToken() {
  formError.value = ''
  if (!acceptSetupTokenInput(tokenInput.value)) {
    formError.value = 'SETUP_AUTHORIZATION_FAILED'
    return
  }
  token.value = getSetupToken()
  tokenInput.value = ''
}

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
        <template v-if="!token">
          <NAlert
            type="info"
            :title="t('setup.tokenMissing')"
            class="login-alert"
            role="status"
          />
          <NText depth="3" class="setup-hint">
            {{ t('setup.tokenCommandHint') }}
          </NText>
          <pre class="code-panel setup-command">m-ui admin setup-link --base-url http://SERVER_IP:2095</pre>
          <pre class="code-panel setup-command">docker compose exec m-ui m-ui admin setup-link --base-url http://SERVER_IP:2095</pre>
          <NForm size="large" @submit.prevent="acceptToken">
            <NFormItem :label="t('setup.tokenInput')">
              <NInput
                v-model:value="tokenInput"
                :placeholder="t('setup.tokenInputPlaceholder')"
                autocomplete="off"
                autofocus
              />
            </NFormItem>
            <NButton
              type="primary"
              attr-type="submit"
              block
              size="large"
              :disabled="!tokenInput.trim()"
            >
              {{ t('setup.continue') }}
            </NButton>
          </NForm>
        </template>
        <NForm v-else size="large" @submit.prevent="submit">
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
        <NText depth="3" class="setup-hint setup-token-hint">
          {{ t('setup.tokenHint') }}
        </NText>
        <AppearancePreferences compact class="login-appearance" />
      </NCard>
    </section>
  </main>
</template>
