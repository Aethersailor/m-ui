import { defineStore } from 'pinia'

import { i18n } from '@/i18n'
import {
  languageStorageKey,
  readLanguageMode,
  resolveLanguage,
  type LanguageMode,
} from '@/utils/preferences'

export const usePreferencesStore = defineStore('preferences', {
  state: () => ({
    languageMode: 'auto' as LanguageMode,
    serverLanguageDefault: 'auto' as LanguageMode,
    initialized: false,
  }),
  getters: {
    language: (state) =>
      resolveLanguage(state.languageMode, state.serverLanguageDefault),
  },
  actions: {
    initialize(serverDefault?: LanguageMode) {
      if (serverDefault !== undefined) {
        this.serverLanguageDefault = serverDefault
      }
      if (!this.initialized) {
        this.languageMode = readLanguageMode()
        this.initialized = true
      }
      this.applyLanguage()
    },
    setLanguageMode(mode: LanguageMode) {
      this.languageMode = mode
      localStorage.setItem(languageStorageKey, mode)
      this.applyLanguage()
    },
    setServerLanguageDefault(mode: LanguageMode) {
      this.serverLanguageDefault = mode
      this.applyLanguage()
    },
    applyLanguage() {
      const language = this.language
      i18n.global.locale.value = language
      document.documentElement.lang = language
    },
  },
})
