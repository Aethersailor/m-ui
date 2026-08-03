<script setup lang="ts">
import { NSelect, NText } from 'naive-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { usePreferencesStore } from '@/stores/preferences'
import { useThemeStore } from '@/stores/theme'
import type { LanguageMode } from '@/utils/preferences'

withDefaults(defineProps<{ compact?: boolean }>(), {
  compact: false,
})

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
</script>

<template>
  <div class="appearance-preferences" :class="{ compact }">
    <div class="appearance-field">
      <NText depth="3">{{ t('language.label') }}</NText>
      <NSelect
        :value="preferences.languageMode"
        :options="languageOptions"
        :size="compact ? 'small' : 'medium'"
        :aria-label="t('language.label')"
        @update:value="preferences.setLanguageMode"
      />
    </div>
    <div class="appearance-field">
      <NText depth="3">{{ t('theme.label') }}</NText>
      <NSelect
        :value="theme.mode"
        :options="themeOptions"
        :size="compact ? 'small' : 'medium'"
        :aria-label="t('theme.label')"
        @update:value="theme.setMode"
      />
    </div>
  </div>
</template>
