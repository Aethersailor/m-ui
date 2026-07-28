<script setup lang="ts">
import {
  darkTheme,
  dateEnUS,
  dateZhCN,
  enUS,
  NConfigProvider,
  NDialogProvider,
  NGlobalStyle,
  NLoadingBarProvider,
  NMessageProvider,
  type GlobalThemeOverrides,
  zhCN,
} from 'naive-ui'
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { useThemeStore } from '@/stores/theme'

const theme = useThemeStore()
const { locale } = useI18n()
theme.initialize()

const naiveLocale = computed(() => (locale.value === 'zh-CN' ? zhCN : enUS))
const naiveDateLocale = computed(() =>
  locale.value === 'zh-CN' ? dateZhCN : dateEnUS,
)
const themeOverrides: GlobalThemeOverrides = {
  common: {
    primaryColor: '#5a57d6',
    primaryColorHover: '#706de2',
    primaryColorPressed: '#4441ad',
    primaryColorSuppl: '#706de2',
    successColor: '#1b9a86',
    successColorHover: '#2baf99',
    successColorPressed: '#147968',
  },
}
</script>

<template>
  <NConfigProvider
    :theme="theme.dark ? darkTheme : null"
    :locale="naiveLocale"
    :date-locale="naiveDateLocale"
    :theme-overrides="themeOverrides"
  >
    <NGlobalStyle />
    <NLoadingBarProvider>
      <NDialogProvider>
        <NMessageProvider placement="top-right">
          <div class="theme-root" :class="{ 'theme-dark': theme.dark }">
            <RouterView />
          </div>
        </NMessageProvider>
      </NDialogProvider>
    </NLoadingBarProvider>
  </NConfigProvider>
</template>
