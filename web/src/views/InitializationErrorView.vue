<script setup lang="ts">
import { NButton, NCard, NSelect, NText } from 'naive-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { usePreferencesStore } from '@/stores/preferences'
import { useThemeStore } from '@/stores/theme'
import type { LanguageMode } from '@/utils/preferences'

const { t } = useI18n()
const preferences = usePreferencesStore()
const theme = useThemeStore()
const languageOptions = computed(() => [
  { label: t('language.auto'), value: 'auto' as LanguageMode },
  { label: t('language.chinese'), value: 'zh-CN' as LanguageMode },
  { label: t('language.english'), value: 'en-US' as LanguageMode },
])
const themeOptions = computed(() => [
  { label: t('theme.auto'), value: 'auto' as const },
  { label: t('theme.light'), value: 'light' as const },
  { label: t('theme.dark'), value: 'dark' as const },
])

preferences.initialize()
theme.initialize()

function reload() {
  window.location.reload()
}
</script>

<template>
  <main class="login-page">
    <div class="login-orb login-orb-one" />
    <div class="login-orb login-orb-two" />
    <div class="login-toolbar">
      <NSelect
        :value="preferences.languageMode"
        :options="languageOptions"
        size="small"
        class="language-select"
        :aria-label="t('language.label')"
        @update:value="preferences.setLanguageMode"
      />
      <NSelect
        :value="theme.mode"
        :options="themeOptions"
        size="small"
        class="theme-select"
        :aria-label="t('theme.label')"
        @update:value="theme.setMode"
      />
    </div>

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
      </NCard>
    </section>
  </main>
</template>
