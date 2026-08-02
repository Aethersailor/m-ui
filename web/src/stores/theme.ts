import { defineStore } from 'pinia'

export type ThemeMode = 'auto' | 'light' | 'dark'

import { readStoredValue, writeStoredValue } from '@/utils/storage'

const storageKey = 'm-ui-theme'
const preferredStorageKey = 'm-ui-theme-mode'

export const useThemeStore = defineStore('theme', {
  state: () => ({
    mode: 'auto' as ThemeMode,
    systemDark: false,
    initialized: false,
  }),
  getters: {
    dark: (state) =>
      state.mode === 'dark' || (state.mode === 'auto' && state.systemDark),
  },
  actions: {
    initialize() {
      if (this.initialized) {
        return
      }
      const saved =
        readStoredValue(preferredStorageKey) ??
        readStoredValue(storageKey)
      if (saved === 'auto' || saved === 'light' || saved === 'dark') {
        this.mode = saved
      }
      const media = window.matchMedia('(prefers-color-scheme: dark)')
      this.systemDark = media.matches
      media.addEventListener('change', (event) => {
        this.systemDark = event.matches
        this.applyDocumentTheme()
      })
      this.initialized = true
      this.applyDocumentTheme()
    },
    setMode(mode: ThemeMode) {
      this.mode = mode
      writeStoredValue(preferredStorageKey, mode)
      this.applyDocumentTheme()
    },
    cycle() {
      const modes: ThemeMode[] = ['auto', 'light', 'dark']
      const index = modes.indexOf(this.mode)
      this.setMode(modes[(index + 1) % modes.length] ?? 'auto')
    },
    applyDocumentTheme() {
      document.documentElement.dataset.theme = this.dark ? 'dark' : 'light'
    },
  },
})
