<script setup lang="ts">
import {
  NCard,
  NCode,
  NLayout,
  NLayoutContent,
  NSpace,
  NSpin,
  NTag,
  NText,
} from 'naive-ui'
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import { useHealthStore } from '@/stores/health'

const { t } = useI18n()
const health = useHealthStore()
const controller = new AbortController()

const statusType = computed(() =>
  health.health?.status === 'ok' ? 'success' : 'error',
)

onMounted(() => {
  void health.refresh(controller.signal)
})

onBeforeUnmount(() => {
  controller.abort()
})
</script>

<template>
  <NLayout class="page">
    <NLayoutContent class="content">
      <header class="hero">
        <NText tag="h1" class="title">{{ t('product.name') }}</NText>
        <NText depth="3">{{ t('product.description') }}</NText>
      </header>

      <NSpace vertical :size="18">
        <NCard :title="t('health.title')" embedded>
          <NSpin v-if="health.loading" size="small">
            {{ t('health.loading') }}
          </NSpin>
          <NSpace v-else-if="health.health" vertical>
            <NTag :type="statusType" :bordered="false">
              {{ t('health.ready') }}
            </NTag>
            <NText>
              {{ t('health.version') }}:
              <NCode :code="health.health.build.version" word-wrap />
            </NText>
          </NSpace>
          <NTag v-else type="error" :bordered="false">
            {{ t('health.failed') }}
          </NTag>
        </NCard>

        <NCard :title="t('scope.title')" embedded>
          <NText>{{ t('scope.description') }}</NText>
        </NCard>
      </NSpace>
    </NLayoutContent>
  </NLayout>
</template>
