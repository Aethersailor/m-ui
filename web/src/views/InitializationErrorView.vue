<script setup lang="ts">
import { NButton, NCard, NText } from 'naive-ui'
import { useI18n } from 'vue-i18n'

import AppearancePreferences from '@/components/AppearancePreferences.vue'

const { t } = useI18n()

function reload() {
  window.location.reload()
}
</script>

<template>
  <main class="login-page">
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

      <NCard :bordered="false" class="login-card">
        <NText tag="h2" class="login-card-title">
          {{ t('setup.initializationTitle') }}
        </NText>
        <NText depth="3" class="setup-description">
          {{ t('setup.initializationDescription') }}
        </NText>
        <NButton type="primary" block size="large" @click="reload">
          {{ t('common.refresh') }}
        </NButton>
        <AppearancePreferences compact class="login-appearance" />
      </NCard>
    </section>
  </main>
</template>
