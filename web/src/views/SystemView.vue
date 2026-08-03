<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NDescriptions,
  NDescriptionsItem,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NInputNumber,
  NSelect,
  NSpace,
  NSwitch,
  NTabPane,
  NTabs,
  NTag,
  NText,
  useDialog,
  useMessage,
} from 'naive-ui'
import { computed, onMounted, reactive, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { changePassword } from '@/api/auth'
import {
  checkCore,
  getCoreStatus,
  updateEndpointSettings,
  getRuntimeLogs,
  listAuditEntries,
  runRuntimeAction,
  rollbackCore,
  testController,
  testCore,
  updateCore,
  updateCoreSettings,
  updateSettings,
  type Settings,
  type EndpointSettings,
  type CoreStatus,
} from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import AppearancePreferences from '@/components/AppearancePreferences.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { usePreferencesStore } from '@/stores/preferences'
import type { LanguageMode } from '@/utils/preferences'
import { errorTranslationKey } from '@/utils/errors'
import { formatDateTime } from '@/utils/format'

const { locale, t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const management = useManagementStore()
const preferences = usePreferencesStore()
const dialog = useDialog()
const message = useMessage()
const loading = ref(true)
const saving = ref(false)
const endpointSaving = ref(false)
const coreBusy = ref(false)
const coreStatus = ref<CoreStatus | null>(null)
const activeTab = ref('settings')
const settingsForm = reactive<Settings>({
  panel_title: 'm-ui',
  ui_language: 'auto',
  public_host: 'localhost',
})
const endpointForm = reactive<EndpointSettings>({
  panel_ui_bind: { host: '127.0.0.1', port: 2095 },
  mihomo_external_controller_bind: { host: '127.0.0.1', port: 9090 },
  mihomo_controller_connect: { host: '127.0.0.1', port: 9090 },
  external_controller_cors_origins: [],
  generation: 1,
})
const corsOriginsText = ref('')
const coreForm = reactive({
  channel: 'release' as 'release' | 'alpha',
  auto_update: false,
  check_interval: '24h0m0s' as CoreStatus['settings']['check_interval'],
})
const passwords = reactive({
  current: '',
  next: '',
  confirm: '',
})

const languageOptions = computed(() => [
  { label: t('language.auto'), value: 'auto' as LanguageMode },
  { label: t('language.chinese'), value: 'zh-CN' as LanguageMode },
  { label: t('language.english'), value: 'en-US' as LanguageMode },
])
const coreChannelOptions = computed(() => [
  { label: t('system.coreRelease'), value: 'release' },
  { label: t('system.coreAlpha'), value: 'alpha' },
])
const coreIntervalOptions = [
  { label: '6h', value: '6h0m0s' },
  { label: '12h', value: '12h0m0s' },
  { label: '24h', value: '24h0m0s' },
  { label: '7d', value: '168h0m0s' },
]

onMounted(load)

async function load() {
  loading.value = true
  try {
    const [, , loadedCore] = await Promise.all([
      management.loadSystem(),
      management.refreshRuntime(),
      getCoreStatus(),
    ])
    coreStatus.value = loadedCore
    Object.assign(coreForm, {
      channel: loadedCore.settings.channel,
      auto_update: loadedCore.settings.auto_update,
      check_interval: loadedCore.settings.check_interval,
    })
    if (management.settings) {
      Object.assign(settingsForm, management.settings)
    }
    if (management.endpointSettings) {
      Object.assign(endpointForm, {
        ...management.endpointSettings.active,
        panel_ui_bind: { ...management.endpointSettings.active.panel_ui_bind },
        mihomo_external_controller_bind: {
          ...management.endpointSettings.active.mihomo_external_controller_bind,
        },
        mihomo_controller_connect: {
          ...management.endpointSettings.active.mihomo_controller_connect,
        },
      })
      corsOriginsText.value = endpointForm.external_controller_cors_origins.join(
        '\n',
      )
    }
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    loading.value = false
  }
}

async function saveCoreSettings() {
  coreBusy.value = true
  try {
    coreStatus.value = await updateCoreSettings(auth.csrfToken, {
      ...coreForm,
    })
    message.success(t('common.saved'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    coreBusy.value = false
  }
}

async function runCoreCheck() {
  coreBusy.value = true
  try {
    await checkCore(auth.csrfToken)
    coreStatus.value = await getCoreStatus()
    message.success(t('system.coreCheckDone'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    coreBusy.value = false
  }
}

function confirmCoreAction(action: 'update' | 'rollback') {
  dialog.warning({
    title: t(
      action === 'update'
        ? 'system.coreUpdateConfirm'
        : 'system.coreRollbackConfirm',
    ),
    content: t('system.coreTransactionHint'),
    positiveText: t(
      action === 'update' ? 'system.coreUpdate' : 'system.coreRollback',
    ),
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      coreBusy.value = true
      try {
        if (action === 'update') {
          await updateCore(auth.csrfToken)
        } else {
          await rollbackCore(auth.csrfToken)
        }
        coreStatus.value = await getCoreStatus()
        await management.refreshRuntime()
        message.success(t('system.coreActionDone'))
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      } finally {
        coreBusy.value = false
      }
    },
  })
}

async function saveSettings() {
  saving.value = true
  try {
    const saved = await updateSettings(auth.csrfToken, { ...settingsForm })
    management.settings = saved
    preferences.setServerLanguageDefault(saved.ui_language)
    message.success(t('common.saved'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

async function saveEndpointSettings() {
  endpointSaving.value = true
  try {
    const saved = await updateEndpointSettings(auth.csrfToken, {
      panel_ui_bind: { ...endpointForm.panel_ui_bind },
      mihomo_external_controller_bind: {
        ...endpointForm.mihomo_external_controller_bind,
      },
      mihomo_controller_connect: { ...endpointForm.mihomo_controller_connect },
      external_controller_cors_origins: corsOriginsText.value
        .split(/\r?\n/)
        .map((origin) => origin.trim())
        .filter(Boolean),
      generation: endpointForm.generation,
    })
    management.endpointSettings = saved
    Object.assign(endpointForm, {
      ...saved.active,
      panel_ui_bind: { ...saved.active.panel_ui_bind },
      mihomo_external_controller_bind: {
        ...saved.active.mihomo_external_controller_bind,
      },
      mihomo_controller_connect: { ...saved.active.mihomo_controller_connect },
    })
    corsOriginsText.value = endpointForm.external_controller_cors_origins.join(
      '\n',
    )
    message.success(t('system.endpointSaved'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    endpointSaving.value = false
  }
}

function confirmRuntimeAction(
  action: 'start' | 'stop' | 'restart' | 'reload',
) {
  const actionLabel = t(`system.${action}`)
  dialog.warning({
    title: t('system.actionTitle', { action: actionLabel }),
    content: t('system.actionBody'),
    positiveText: actionLabel,
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await runRuntimeAction(auth.csrfToken, action)
        message.success(t('system.actionDone'))
        window.setTimeout(() => void management.refreshRuntime(), 750)
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

async function runCoreTest() {
  try {
    const result = await testCore(auth.csrfToken)
    message.success(t('system.testPassed', { version: result.version }))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

async function runControllerTest() {
  try {
    const result = await testController(auth.csrfToken)
    message.success(
      t('system.testPassed', {
        version: result.version.version,
      }),
    )
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

async function refreshLogs() {
  try {
    management.logs = await getRuntimeLogs()
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

async function refreshAudit() {
  try {
    management.audit = await listAuditEntries()
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  }
}

async function submitPassword() {
  if (passwords.next !== passwords.confirm) {
    message.error(t('auth.passwordMismatch'))
    return
  }
  saving.value = true
  try {
    await changePassword(
      auth.csrfToken,
      passwords.current,
      passwords.next,
    )
    Object.assign(passwords, { current: '', next: '', confirm: '' })
    message.success(t('auth.passwordChanged'))
    await auth.logout()
    await router.replace({ name: 'login' })
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <AppShell>
    <main class="page-container">
      <PageHeader
        :title="t('system.title')"
        :description="t('system.description')"
      />

      <NCard :bordered="false" class="surface-card">
        <NTabs v-model:value="activeTab" type="line" animated>
          <NTabPane name="settings" :tab="t('system.settings')">
            <NForm class="settings-form" @submit.prevent="saveSettings">
              <div class="form-grid">
                <NFormItem :label="t('system.panelTitle')" required>
                  <NInput
                    v-model:value="settingsForm.panel_title"
                    maxlength="80"
                  />
                </NFormItem>
                <NFormItem :label="t('system.language')" required>
                  <NSelect
                    v-model:value="settingsForm.ui_language"
                    :options="languageOptions"
                  />
                </NFormItem>
                <NFormItem :label="t('system.publicHost')" required>
                  <NInput v-model:value="settingsForm.public_host" />
                </NFormItem>
              </div>
              <NAlert type="info" :bordered="false">
                {{ t('system.publicHostHint') }}
              </NAlert>
              <NSpace justify="end" class="modal-actions">
                <NButton
                  type="primary"
                  attr-type="submit"
                  :loading="saving"
                  :disabled="
                    !settingsForm.panel_title || !settingsForm.public_host
                  "
                >
                  {{ t('common.save') }}
                </NButton>
              </NSpace>
            </NForm>

            <NCard
              :title="t('appearance.title')"
              size="small"
              class="section-gap"
            >
              <NText depth="3">{{ t('appearance.description') }}</NText>
              <AppearancePreferences class="section-gap" />
            </NCard>

            <NCard
              :title="t('system.endpointSettings')"
              size="small"
              class="section-gap"
            >
              <NAlert type="warning" :bordered="false" class="section-gap">
                {{ t('system.endpointSecurityHint') }}
              </NAlert>
              <NForm
                class="settings-form"
                @submit.prevent="saveEndpointSettings"
              >
                <div class="form-grid">
                  <NFormItem :label="t('system.panelUIBind')" required>
                    <NSpace>
                      <NInput
                        v-model:value="endpointForm.panel_ui_bind.host"
                        :placeholder="t('system.ipAddressPlaceholder')"
                      />
                      <NInputNumber
                        v-model:value="endpointForm.panel_ui_bind.port"
                        :min="1"
                        :max="65535"
                        :show-button="false"
                      />
                    </NSpace>
                  </NFormItem>
                  <NFormItem
                    :label="t('system.mihomoExternalControllerBind')"
                    required
                  >
                    <NSpace>
                      <NInput
                        v-model:value="endpointForm.mihomo_external_controller_bind.host"
                        :placeholder="t('system.ipAddressPlaceholder')"
                      />
                      <NInputNumber
                        v-model:value="endpointForm.mihomo_external_controller_bind.port"
                        :min="1"
                        :max="65535"
                        :show-button="false"
                      />
                    </NSpace>
                  </NFormItem>
                  <NFormItem
                    :label="t('system.mihomoControllerConnect')"
                    required
                  >
                    <NSpace>
                      <NInput
                        v-model:value="endpointForm.mihomo_controller_connect.host"
                        :placeholder="t('system.loopbackPlaceholder')"
                      />
                      <NInputNumber
                        v-model:value="endpointForm.mihomo_controller_connect.port"
                        :min="1"
                        :max="65535"
                        :show-button="false"
                      />
                    </NSpace>
                  </NFormItem>
                  <NFormItem :label="t('system.controllerCorsOrigins')">
                    <NInput
                      v-model:value="corsOriginsText"
                      type="textarea"
                      :placeholder="t('system.controllerCorsPlaceholder')"
                    />
                  </NFormItem>
                </div>
                <NAlert
                  v-if="management.endpointSettings?.pending"
                  type="info"
                  :bordered="false"
                  class="section-gap"
                >
                  {{
                    t('system.endpointPending', {
                      mui: management.endpointSettings.pending
                        .requires_mui_restart
                        ? t('system.muiRestartRequired')
                        : '',
                      mihomo: management.endpointSettings.pending
                        .requires_mihomo_restart
                        ? t('system.mihomoRestartRequired')
                        : '',
                    })
                  }}
                </NAlert>
                <NAlert type="info" :bordered="false" class="section-gap">
                  {{ t('system.endpointRestartOrder') }}
                </NAlert>
                <NSpace justify="end" class="modal-actions">
                  <NButton
                    type="primary"
                    attr-type="submit"
                    :loading="endpointSaving"
                    :disabled="
                      !endpointForm.panel_ui_bind.host ||
                      !endpointForm.mihomo_external_controller_bind.host ||
                      !endpointForm.mihomo_controller_connect.host
                    "
                  >
                    {{ t('common.save') }}
                  </NButton>
                </NSpace>
              </NForm>
            </NCard>
          </NTabPane>

          <NTabPane name="runtime" :tab="t('system.runtime')">
            <div class="runtime-control">
              <div class="status-strip inline-status">
                <div>
                  <span
                    class="status-dot"
                    :class="{ online: management.runtime?.active }"
                  />
                  <NText strong>
                    {{
                      management.runtime?.active
                        ? t('dashboard.online')
                        : t('dashboard.offline')
                    }}
                  </NText>
                </div>
                <NTag
                  :type="management.runtime?.active ? 'success' : 'error'"
                  :bordered="false"
                >
                  {{
                    management.runtime?.version.version || t('common.unknown')
                  }}
                </NTag>
              </div>
              <NSpace class="section-gap">
                <NButton
                  type="primary"
                  @click="confirmRuntimeAction('start')"
                >
                  {{ t('system.start') }}
                </NButton>
                <NButton @click="confirmRuntimeAction('reload')">
                  {{ t('system.reload') }}
                </NButton>
                <NButton @click="confirmRuntimeAction('restart')">
                  {{ t('system.restart') }}
                </NButton>
                <NButton
                  type="error"
                  secondary
                  @click="confirmRuntimeAction('stop')"
                >
                  {{ t('system.stop') }}
                </NButton>
              </NSpace>
              <NSpace class="section-gap">
                <NButton secondary @click="runCoreTest">
                  {{ t('system.coreTest') }}
                </NButton>
                <NButton secondary @click="runControllerTest">
                  {{ t('system.controllerTest') }}
                </NButton>
              </NSpace>
            </div>
            <NCard
              :title="t('system.coreVersion')"
              size="small"
              class="section-gap core-card"
            >
              <NAlert
                v-if="coreStatus?.settings.channel === 'alpha'"
                type="warning"
                :bordered="false"
                class="section-gap"
              >
                {{ t('system.coreAlphaWarning') }}
              </NAlert>
              <NAlert
                v-if="coreStatus && !coreStatus.managed"
                type="info"
                :bordered="false"
                class="section-gap"
              >
                {{ t('system.coreExternal') }}
              </NAlert>
              <NAlert
                v-if="management.runtime?.degraded"
                type="error"
                :bordered="false"
                class="section-gap"
              >
                {{ t('system.coreDegraded') }}
              </NAlert>
              <NDescriptions
                v-if="coreStatus"
                label-placement="top"
                :column="2"
                class="section-gap"
              >
                <NDescriptionsItem :label="t('system.coreActualVersion')">
                  {{ coreStatus.actual_version || t('common.unknown') }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreManaged')">
                  {{
                    coreStatus.managed
                      ? t('common.enabled')
                      : t('common.disabled')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreProcessActive')">
                  {{
                    coreStatus.process_active
                      ? t('common.enabled')
                      : t('common.disabled')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreControllerReachable')">
                  {{
                    coreStatus.controller_reachable
                      ? t('common.enabled')
                      : t('common.disabled')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreCurrentTag')">
                  {{
                    coreStatus.state.current?.identity.tag_name ||
                    t('common.unknown')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreSHA')">
                  <code>{{
                    coreStatus.current_binary_sha256?.slice(0, 12) ||
                    t('common.unknown')
                  }}</code>
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreAvailable')">
                  {{
                    coreStatus.state.available?.tag_name ||
                    t('common.unknown')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreLastCheck')">
                  {{
                    coreStatus.state.last_check_at
                      ? formatDateTime(coreStatus.state.last_check_at, locale)
                      : t('common.never')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreLastUpdate')">
                  {{
                    coreStatus.state.last_update_at
                      ? formatDateTime(coreStatus.state.last_update_at, locale)
                      : t('common.never')
                  }}
                </NDescriptionsItem>
                <NDescriptionsItem :label="t('system.coreNextCheck')">
                  {{
                    coreStatus.state.next_check_at
                      ? formatDateTime(coreStatus.state.next_check_at, locale)
                      : t('common.never')
                  }}
                </NDescriptionsItem>
              </NDescriptions>
              <NForm
                class="settings-form section-gap"
                @submit.prevent="saveCoreSettings"
              >
                <div class="form-grid">
                  <NFormItem :label="t('system.coreChannel')">
                    <NSelect
                      v-model:value="coreForm.channel"
                      :options="coreChannelOptions"
                      :disabled="
                        !coreStatus?.managed ||
                        management.runtime?.degraded ||
                        coreStatus?.state.update_in_progress
                      "
                    />
                  </NFormItem>
                  <NFormItem :label="t('system.coreInterval')">
                    <NSelect
                      v-model:value="coreForm.check_interval"
                      :options="coreIntervalOptions"
                      :disabled="
                        !coreStatus?.managed ||
                        management.runtime?.degraded ||
                        coreStatus?.state.update_in_progress
                      "
                    />
                  </NFormItem>
                  <NFormItem :label="t('system.coreAutoUpdate')">
                    <NSwitch
                      v-model:value="coreForm.auto_update"
                      :disabled="
                        !coreStatus?.managed ||
                        management.runtime?.degraded ||
                        coreStatus?.state.update_in_progress
                      "
                    />
                  </NFormItem>
                </div>
                <NSpace>
                  <NButton
                    attr-type="submit"
                    :loading="coreBusy"
                    :disabled="
                      !coreStatus?.managed ||
                      management.runtime?.degraded ||
                      coreStatus?.state.update_in_progress
                    "
                  >
                    {{ t('common.save') }}
                  </NButton>
                  <NButton
                    secondary
                    :loading="coreBusy"
                    :disabled="
                      !coreStatus?.managed ||
                      management.runtime?.degraded ||
                      coreStatus?.state.update_in_progress
                    "
                    @click="runCoreCheck"
                  >
                    {{ t('system.coreCheck') }}
                  </NButton>
                  <NButton
                    type="primary"
                    :loading="coreBusy"
                    :disabled="
                      !coreStatus?.managed ||
                      management.runtime?.degraded ||
                      coreStatus?.state.update_in_progress
                    "
                    @click="confirmCoreAction('update')"
                  >
                    {{ t('system.coreUpdate') }}
                  </NButton>
                  <NButton
                    type="warning"
                    secondary
                    :loading="coreBusy"
                    :disabled="
                      !coreStatus?.managed ||
                      management.runtime?.degraded ||
                      coreStatus?.state.update_in_progress
                    "
                    @click="confirmCoreAction('rollback')"
                  >
                    {{ t('system.coreRollback') }}
                  </NButton>
                </NSpace>
              </NForm>
            </NCard>
          </NTabPane>

          <NTabPane name="logs" :tab="t('system.logs')">
            <div class="card-heading">
              <NText depth="3">{{ t('system.logTimezone') }}</NText>
              <NButton size="small" @click="refreshLogs">
                {{ t('common.refresh') }}
              </NButton>
            </div>
            <NEmpty
              v-if="!management.logs.length"
              :description="t('system.logsEmpty')"
            />
            <div v-else class="log-panel">
              <div
                v-for="(entry, index) in management.logs"
                :key="`${entry.timestamp}-${index}`"
                class="log-line"
              >
                <time v-if="entry.timestamp && !entry.timestamp.startsWith('0001')">
                  {{ formatDateTime(entry.timestamp, locale) }}
                </time>
                <code>{{ entry.message }}</code>
              </div>
            </div>
          </NTabPane>

          <NTabPane name="security" :tab="t('system.security')">
            <NForm
              class="password-form"
              @submit.prevent="submitPassword"
            >
              <NFormItem :label="t('auth.currentPassword')" required>
                <NInput
                  v-model:value="passwords.current"
                  type="password"
                  autocomplete="current-password"
                  show-password-on="click"
                />
              </NFormItem>
              <NFormItem :label="t('auth.newPassword')" required>
                <NInput
                  v-model:value="passwords.next"
                  type="password"
                  autocomplete="new-password"
                  show-password-on="click"
                />
              </NFormItem>
              <NFormItem :label="t('auth.confirmPassword')" required>
                <NInput
                  v-model:value="passwords.confirm"
                  type="password"
                  autocomplete="new-password"
                  show-password-on="click"
                />
              </NFormItem>
              <NButton
                type="primary"
                attr-type="submit"
                :loading="saving"
                :disabled="
                  !passwords.current ||
                  !passwords.next ||
                  !passwords.confirm
                "
              >
                {{ t('auth.changePassword') }}
              </NButton>
            </NForm>
          </NTabPane>

          <NTabPane name="audit" :tab="t('system.audit')">
            <div class="card-heading">
              <NText depth="3">{{ management.audit.length }}</NText>
              <NButton size="small" @click="refreshAudit">
                {{ t('common.refresh') }}
              </NButton>
            </div>
            <NEmpty
              v-if="!management.audit.length"
              :description="t('common.noData')"
            />
            <div v-else class="table-scroll">
              <table class="data-table">
                <thead>
                  <tr>
                    <th>{{ t('config.time') }}</th>
                    <th>{{ t('system.auditAction') }}</th>
                    <th>{{ t('system.auditResource') }}</th>
                    <th>{{ t('system.auditResult') }}</th>
                    <th>{{ t('system.auditSummary') }}</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="entry in management.audit" :key="entry.id">
                    <td>{{ formatDateTime(entry.created_at, locale) }}</td>
                    <td><code>{{ entry.action }}</code></td>
                    <td>
                      {{ entry.resource_type }}
                      <NText
                        v-if="entry.resource_id"
                        tag="small"
                        depth="3"
                        class="block-text"
                      >
                        {{ entry.resource_id.slice(0, 12) }}
                      </NText>
                    </td>
                    <td>
                      <NTag
                        :type="
                          entry.result === 'success' ? 'success' : 'error'
                        "
                        :bordered="false"
                        size="small"
                      >
                        {{ entry.result }}
                      </NTag>
                    </td>
                    <td>{{ entry.summary }}</td>
                  </tr>
                </tbody>
              </table>
            </div>
          </NTabPane>
        </NTabs>
      </NCard>
    </main>
  </AppShell>
</template>
