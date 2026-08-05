<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NDatePicker,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NSpace,
  NText,
  useMessage,
} from 'naive-ui'
import QrcodeVue from 'qrcode.vue'
import { computed, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { completeOnboarding, type OnboardingResult } from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import CopyButton from '@/components/CopyButton.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { errorTranslationKey } from '@/utils/errors'

const { t } = useI18n()
const router = useRouter()
const message = useMessage()
const auth = useAuthStore()
const management = useManagementStore()
const saving = ref(false)
const result = ref<OnboardingResult | null>(null)

const form = reactive({
  publicHost: window.location.hostname,
  listenerName: 'default',
  listenPort: 443,
  serverName: '',
  realityDest: '',
  userName: 'user',
  expiresAt: null as number | null,
})

const canSubmit = computed(
  () =>
    Boolean(
      form.publicHost.trim() &&
      form.listenerName.trim() &&
      form.listenPort &&
      form.serverName.trim() &&
      form.realityDest.trim() &&
      form.userName.trim(),
    ),
)

function fillRealityDestination() {
  if (form.serverName.trim() && !form.realityDest.trim()) {
    form.realityDest = `${form.serverName.trim()}:443`
  }
}

async function submit() {
  saving.value = true
  try {
    result.value = await completeOnboarding(auth.csrfToken, {
      public_host: form.publicHost.trim(),
      listener: {
        name: form.listenerName.trim(),
        listen_port: form.listenPort,
        server_name: form.serverName.trim(),
        reality_dest: form.realityDest.trim(),
        udp_enabled: true,
      },
      user: {
        name: form.userName.trim(),
        expires_at: form.expiresAt
          ? new Date(form.expiresAt).toISOString()
          : null,
      },
    })
    await management.refreshListeners()
    message.success(t('onboarding.completed'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AppShell>
    <main class="page-container onboarding-page">
      <PageHeader
        :title="t('onboarding.title')"
        :description="t('onboarding.description')"
      />

      <NCard v-if="!result" :bordered="false" class="surface-card">
        <NAlert type="info" :bordered="false" class="section-gap">
          {{ t('onboarding.automaticHint') }}
        </NAlert>
        <NForm size="large" @submit.prevent="submit">
          <div class="form-grid">
            <NFormItem :label="t('onboarding.publicHost')" required>
              <NInput v-model:value="form.publicHost" />
            </NFormItem>
            <NFormItem :label="t('onboarding.listenerName')" required>
              <NInput v-model:value="form.listenerName" maxlength="64" />
            </NFormItem>
            <NFormItem :label="t('onboarding.listenPort')" required>
              <NInputNumber
                v-model:value="form.listenPort"
                :min="1"
                :max="65535"
                class="full-width"
              />
            </NFormItem>
            <NFormItem :label="t('onboarding.serverName')" required>
              <NInput
                v-model:value="form.serverName"
                placeholder="www.example.com"
                @blur="fillRealityDestination"
              />
            </NFormItem>
            <NFormItem :label="t('onboarding.realityDest')" required>
              <NInput
                v-model:value="form.realityDest"
                placeholder="www.example.com:443"
              />
            </NFormItem>
            <NFormItem :label="t('onboarding.userName')" required>
              <NInput v-model:value="form.userName" maxlength="64" />
            </NFormItem>
            <NFormItem :label="t('onboarding.expiresAt')">
              <NDatePicker
                v-model:value="form.expiresAt"
                type="datetime"
                clearable
                class="full-width"
              />
            </NFormItem>
          </div>
          <NButton
            type="primary"
            attr-type="submit"
            size="large"
            block
            :loading="saving"
            :disabled="!canSubmit"
          >
            {{ t('onboarding.create') }}
          </NButton>
        </NForm>
      </NCard>

      <NCard v-else :bordered="false" class="surface-card onboarding-result">
        <NAlert type="success" :title="t('onboarding.ready')" :bordered="false">
          {{ t('onboarding.readyHint') }}
        </NAlert>
        <div class="qr-wrap section-gap">
          <QrcodeVue :value="result.share.qr_content" :size="220" level="M" />
        </div>
        <NText strong>{{ t('share.uri') }}</NText>
        <div class="share-block section-gap">
          <code class="break-all">{{ result.share.uri }}</code>
          <CopyButton :value="result.share.uri" />
        </div>
        <NText strong>{{ t('share.yaml') }}</NText>
        <pre class="code-panel section-gap">{{ result.share.client_yaml }}</pre>
        <NSpace justify="end" class="section-gap">
          <NButton
            @click="
              router.push({
                name: 'listener-detail',
                params: { id: result.listener.id },
              })
            "
          >
            {{ t('onboarding.manage') }}
          </NButton>
          <NButton type="primary" @click="router.push({ name: 'dashboard' })">
            {{ t('onboarding.dashboard') }}
          </NButton>
        </NSpace>
      </NCard>
    </main>
  </AppShell>
</template>
