import { defineStore } from 'pinia'

export type ThemeMode = 'auto' | 'light' | 'dark'

const storageKey = 'm-ui-theme'

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
      const saved = localStorage.getItem(storageKey)
      if (saved === 'auto' || saved === 'light' || saved === 'dark') {
        this.mode = saved
      }
      const media = window.matchMedia('(prefers-color-scheme: dark)')
      this.systemDark = media.matches
      media.addEventListener('change', (event) => {
        this.systemDark = event.matches
      })
      this.initialized = true
    },
    setMode(mode: ThemeMode) {
      this.mode = mode
      localStorage.setItem(storageKey, mode)
    },
    cycle() {
      this.setMode(this.dark ? 'light' : 'dark')
    },
  },
})
