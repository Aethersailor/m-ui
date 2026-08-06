<script setup lang="ts">
import { NAlert, NButton, NCard, NSpin, NTag, NText } from 'naive-ui'
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import AppShell from '@/components/AppShell.vue'
import PageHeader from '@/components/PageHeader.vue'
import { useManagementStore } from '@/stores/management'
import { formatBytes, formatDateTime } from '@/utils/format'

const management = useManagementStore()
const router = useRouter()
const { locale, t, te } = useI18n()
let refreshTimer = 0

const runtime = computed(() => management.runtime)
const errorMessage = computed(() => {
  const key = `errors.${management.errorCode}`
  return management.errorCode && te(key) ? t(key) : t('common.error')
})

onMounted(async () => {
  try {
    await management.loadOverview()
  } catch {
    // The store retains a safe error code for display.
  }
  refreshTimer = window.setInterval(() => {
    void management.refreshRuntime()
  }, 2000)
})

onBeforeUnmount(() => {
  window.clearInterval(refreshTimer)
})
</script>

<template>
  <AppShell>
    <main class="page-container">
      <PageHeader
        :title="t('dashboard.title')"
        :description="t('dashboard.description')"
      />

      <NAlert
        v-if="runtime?.degraded"
        type="error"
        :title="t('dashboard.degradedTitle')"
        class="section-gap"
      >
        {{ runtime.degraded_reason || t('dashboard.degradedBody') }}
      </NAlert>
      <NAlert
        v-else-if="management.errorCode"
        type="error"
        :title="errorMessage"
        class="section-gap"
      />

      <NCard
        v-if="!management.loading && !management.nodes.length"
        :bordered="false"
        class="surface-card section-gap"
      >
        <NText tag="h2" class="section-title">{{ t('onboarding.title') }}</NText>
        <NText depth="3">{{ t('onboarding.description') }}</NText>
        <div class="section-gap">
          <NButton type="primary" @click="router.push({ name: 'onboarding' })">
            {{ t('onboarding.create') }}
          </NButton>
        </div>
      </NCard>

      <NSpin :show="management.loading && !runtime">
        <section class="status-strip">
          <div>
            <span
              class="status-dot"
              :class="{ online: runtime?.active }"
              aria-hidden="true"
            />
            <NText strong class="status-name">
              {{
                runtime?.active
                  ? t('dashboard.online')
                  : t('dashboard.offline')
              }}
            </NText>
          </div>
          <NTag :type="runtime?.active ? 'success' : 'error'" :bordered="false">
            {{ runtime?.version.version || t('common.unknown') }}
          </NTag>
        </section>

        <NAlert type="info" :bordered="false" class="metric-note">
          {{ t('dashboard.instanceMetrics') }}
        </NAlert>

        <section class="metric-grid">
          <NCard class="metric-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.currentUpload') }}</NText>
            <strong>{{ formatBytes(runtime?.traffic.up ?? 0) }}/s</strong>
          </NCard>
          <NCard class="metric-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.currentDownload') }}</NText>
            <strong>{{ formatBytes(runtime?.traffic.down ?? 0) }}/s</strong>
          </NCard>
          <NCard class="metric-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.totalUpload') }}</NText>
            <strong>
              {{
                formatBytes(
                  runtime?.traffic.upTotal ?? runtime?.upload_total ?? 0,
                )
              }}
            </strong>
          </NCard>
          <NCard class="metric-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.totalDownload') }}</NText>
            <strong>
              {{
                formatBytes(
                  runtime?.traffic.downTotal ?? runtime?.download_total ?? 0,
                )
              }}
            </strong>
          </NCard>
          <NCard class="metric-card accent-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.memory') }}</NText>
            <strong>{{ formatBytes(runtime?.memory.inuse ?? 0) }}</strong>
          </NCard>
          <NCard class="metric-card accent-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.connections') }}</NText>
            <strong>{{ runtime?.connection_count ?? 0 }}</strong>
          </NCard>
          <NCard class="metric-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.listenerCount') }}</NText>
            <strong>{{ management.nodes.length }}</strong>
          </NCard>
          <NCard class="metric-card" :bordered="false">
            <NText depth="3">{{ t('dashboard.enabledUsers') }}</NText>
            <strong>{{ management.enabledUserCount }}</strong>
          </NCard>
        </section>

        <NCard class="revision-card" :bordered="false">
          <dl class="detail-grid compact-details">
            <div>
              <dt>{{ t('dashboard.lastPublished') }}</dt>
              <dd>
                {{
                  formatDateTime(
                    management.activeRevision?.activated_at,
                    locale,
                  )
                }}
              </dd>
            </div>
            <div>
              <dt>{{ t('dashboard.observedAt') }}</dt>
              <dd>{{ formatDateTime(runtime?.observed_at, locale) }}</dd>
            </div>
          </dl>
        </NCard>
      </NSpin>
    </main>
  </AppShell>
</template>
