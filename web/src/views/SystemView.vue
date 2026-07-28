<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NForm,
  NFormItem,
  NInput,
  NSelect,
  NSpace,
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
  getRuntimeLogs,
  listAuditEntries,
  runRuntimeAction,
  testController,
  testCore,
  updateSettings,
  type Settings,
} from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { useThemeStore, type ThemeMode } from '@/stores/theme'
import { errorTranslationKey } from '@/utils/errors'
import { formatDateTime } from '@/utils/format'

const { locale, t } = useI18n()
const router = useRouter()
const auth = useAuthStore()
const management = useManagementStore()
const theme = useThemeStore()
const dialog = useDialog()
const message = useMessage()
const loading = ref(true)
const saving = ref(false)
const activeTab = ref('settings')
const settingsForm = reactive<Settings>({
  panel_title: 'm-ui',
  ui_language: 'en-US',
  public_host: 'localhost',
})
const passwords = reactive({
  current: '',
  next: '',
  confirm: '',
})

const languageOptions = [
  { label: '简体中文', value: 'zh-CN' },
  { label: 'English', value: 'en-US' },
]
const themeOptions = computed(() => [
  { label: t('theme.auto'), value: 'auto' },
  { label: t('theme.light'), value: 'light' },
  { label: t('theme.dark'), value: 'dark' },
])

onMounted(load)

async function load() {
  loading.value = true
  try {
    await Promise.all([management.loadSystem(), management.refreshRuntime()])
    if (management.settings) {
      Object.assign(settingsForm, management.settings)
    }
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    loading.value = false
  }
}

async function saveSettings() {
  saving.value = true
  try {
    const saved = await updateSettings(auth.csrfToken, { ...settingsForm })
    management.settings = saved
    locale.value = saved.ui_language
    message.success(t('common.saved'))
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    saving.value = false
  }
}

function setTheme(value: ThemeMode) {
  theme.setMode(value)
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
                <NFormItem :label="t('theme.label')">
                  <NSelect
                    :value="theme.mode"
                    :options="themeOptions"
                    @update:value="setTheme"
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
