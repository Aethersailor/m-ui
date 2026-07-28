import { defineStore } from 'pinia'

import { getHealth, type HealthResponse } from '@/api/client'

export const useHealthStore = defineStore('health', {
  state: () => ({
    health: null as HealthResponse | null,
    loading: false,
    error: '',
  }),
  actions: {
    async refresh(signal?: AbortSignal) {
      this.loading = true
      this.error = ''
      try {
        this.health = await getHealth(signal)
      } catch (error) {
        if (error instanceof DOMException && error.name === 'AbortError') {
          return
        }
        this.health = null
        this.error = error instanceof Error ? error.message : 'Unknown error'
      } finally {
        this.loading = false
      }
    },
  },
})
