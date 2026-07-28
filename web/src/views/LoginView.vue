<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NForm,
  NFormItem,
  NInput,
  NLayout,
  NLayoutContent,
  NText,
} from 'naive-ui'
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { useAuthStore } from '@/stores/auth'

const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const username = ref('')
const password = ref('')

const errorMessage = computed(() => {
  if (!auth.errorCode) {
    return ''
  }
  if (auth.errorCode === 'LOGIN_RATE_LIMITED') {
    return t('auth.rateLimited')
  }
  return t('auth.failed')
})

async function submit() {
  try {
    await auth.login(username.value, password.value)
    password.value = ''
    const redirect =
      typeof route.query.redirect === 'string' ? route.query.redirect : '/'
    await router.replace(redirect)
  } catch {
    // The store exposes only a non-differentiating error code to this view.
  }
}
</script>

<template>
  <NLayout class="page">
    <NLayoutContent class="login-content">
      <header class="login-brand">
        <NText tag="h1" class="title">{{ t('product.name') }}</NText>
        <NText depth="3">{{ t('product.description') }}</NText>
      </header>
      <NCard :title="t('auth.signIn')" embedded>
        <NAlert
          v-if="errorMessage"
          type="error"
          :title="errorMessage"
          class="login-alert"
        />
        <NForm @submit.prevent="submit">
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
            :loading="auth.loading"
            :disabled="!username || !password"
          >
            {{ t('auth.submit') }}
          </NButton>
        </NForm>
      </NCard>
    </NLayoutContent>
  </NLayout>
</template>
