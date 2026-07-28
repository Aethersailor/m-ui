<script setup lang="ts">
import {
  NAlert,
  NButton,
  NCard,
  NEmpty,
  NSpace,
  NSpin,
  NTag,
  NText,
  useDialog,
  useMessage,
} from 'naive-ui'
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import {
  getConfigPreview,
  listRevisions,
  rollbackRevision,
  validateConfig,
  type ConfigPreview,
  type Revision,
} from '@/api/management'
import AppShell from '@/components/AppShell.vue'
import CopyButton from '@/components/CopyButton.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useAuthStore } from '@/stores/auth'
import { useManagementStore } from '@/stores/management'
import { errorTranslationKey } from '@/utils/errors'
import { formatDateTime } from '@/utils/format'

const { locale, t } = useI18n()
const auth = useAuthStore()
const management = useManagementStore()
const dialog = useDialog()
const message = useMessage()
const preview = ref<ConfigPreview | null>(null)
const revisions = ref<Revision[]>([])
const loading = ref(true)
const validating = ref(false)

onMounted(load)

async function load() {
  loading.value = true
  try {
    const [nextPreview, nextRevisions] = await Promise.all([
      getConfigPreview(),
      listRevisions(),
    ])
    preview.value = nextPreview
    revisions.value = nextRevisions
    management.revisions = revisions.value
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    loading.value = false
  }
}

function confirmReveal() {
  dialog.warning({
    title: t('config.revealTitle'),
    content: t('config.revealBody'),
    positiveText: t('config.reveal'),
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        preview.value = await getConfigPreview(true)
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

async function runValidation() {
  validating.value = true
  try {
    const result = await validateConfig(auth.csrfToken)
    message.success(`${t('config.valid')} ${result.sha256.slice(0, 12)}`)
  } catch (error) {
    message.error(t(errorTranslationKey(error)))
  } finally {
    validating.value = false
  }
}

function confirmRollback(revision: Revision) {
  dialog.error({
    title: t('config.rollbackTitle', {
      number: revision.revision_number,
    }),
    content: t('config.rollbackBody'),
    positiveText: t('config.rollback'),
    negativeText: t('common.cancel'),
    async onPositiveClick() {
      try {
        await rollbackRevision(auth.csrfToken, revision.id)
        message.success(t('config.rolledBack'))
        await load()
      } catch (error) {
        message.error(t(errorTranslationKey(error)))
      }
    },
  })
}

function revisionTagType(
  status: Revision['status'],
): 'success' | 'error' | 'warning' | 'default' {
  if (status === 'active') return 'success'
  if (status === 'failed') return 'error'
  if (status === 'pending') return 'warning'
  return 'default'
}
</script>

<template>
  <AppShell>
    <main class="page-container">
      <PageHeader
        :title="t('config.title')"
        :description="t('config.description')"
      >
        <template #actions>
          <NSpace>
            <NButton @click="load">{{ t('common.refresh') }}</NButton>
            <NButton
              type="primary"
              :loading="validating"
              @click="runValidation"
            >
              {{ t('config.validate') }}
            </NButton>
          </NSpace>
        </template>
      </PageHeader>

      <NSpin :show="loading">
        <NCard :bordered="false" class="surface-card">
          <div class="card-heading">
            <div>
              <NText tag="h2" class="section-title">
                {{ t('config.preview') }}
              </NText>
              <NTag
                :type="preview?.revealed ? 'error' : 'info'"
                :bordered="false"
                size="small"
              >
                {{
                  preview?.revealed
                    ? t('config.revealed')
                    : t('config.redacted')
                }}
              </NTag>
            </div>
            <NButton
              v-if="!preview?.revealed"
              type="warning"
              secondary
              @click="confirmReveal"
            >
              {{ t('config.reveal') }}
            </NButton>
          </div>
          <NAlert
            v-if="preview?.revealed"
            type="warning"
            :bordered="false"
            class="section-gap"
          >
            {{ t('config.revealBody') }}
          </NAlert>
          <pre class="code-panel config-preview">{{
            preview?.yaml || t('common.loading')
          }}</pre>
          <div v-if="preview" class="hash-row">
            <NText depth="3">{{ t('config.hash') }}</NText>
            <code class="break-all">{{ preview.sha256 }}</code>
            <CopyButton :value="preview.sha256" size="tiny" />
          </div>
        </NCard>

        <NCard :bordered="false" class="surface-card section-gap">
          <div class="card-heading">
            <NText tag="h2" class="section-title">
              {{ t('config.title') }}
            </NText>
            <NText depth="3">{{ revisions.length }}</NText>
          </div>
          <NEmpty
            v-if="!revisions.length"
            :description="t('common.noData')"
          />
          <div v-else class="table-scroll">
            <table class="data-table">
              <thead>
                <tr>
                  <th>{{ t('config.revision') }}</th>
                  <th>{{ t('config.time') }}</th>
                  <th>{{ t('config.actor') }}</th>
                  <th>{{ t('config.reason') }}</th>
                  <th>{{ t('config.hash') }}</th>
                  <th>{{ t('config.status') }}</th>
                  <th class="actions-cell">{{ t('common.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="revision in revisions" :key="revision.id">
                  <td><strong>#{{ revision.revision_number }}</strong></td>
                  <td>{{ formatDateTime(revision.created_at, locale) }}</td>
                  <td>{{ revision.actor_admin_id || 'system' }}</td>
                  <td>
                    {{ revision.reason }}
                    <NText
                      v-if="revision.error_message"
                      tag="small"
                      type="error"
                      class="block-text"
                    >
                      {{ revision.error_message }}
                    </NText>
                  </td>
                  <td>
                    <code>{{ revision.sha256.slice(0, 12) }}</code>
                  </td>
                  <td>
                    <NTag
                      :type="revisionTagType(revision.status)"
                      :bordered="false"
                      size="small"
                    >
                      {{ revision.status }}
                    </NTag>
                  </td>
                  <td class="actions-cell">
                    <NButton
                      size="small"
                      secondary
                      :disabled="
                        revision.status !== 'active' &&
                        revision.status !== 'rolled_back'
                      "
                      @click="confirmRollback(revision)"
                    >
                      {{ t('config.rollback') }}
                    </NButton>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
        </NCard>
      </NSpin>
    </main>
  </AppShell>
</template>
