import { defineStore } from 'pinia'

import { getSetupStatus, type SetupStatus } from '@/api/setup'
import { APIError } from '@/api/client'

export const useSetupStore = defineStore('setup', {
  state: () => ({
    status: null as SetupStatus | null,
    initialized: false,
    loading: false,
    errorCode: '',
  }),
  getters: {
    required: (state) => state.status?.state === 'required',
    complete: (state) => state.status?.state === 'complete',
  },
  actions: {
    async initialize() {
      if (this.initialized || this.loading) {
        return
      }
      this.loading = true
      this.errorCode = ''
      try {
        this.status = await getSetupStatus()
      } catch (error) {
        this.status = null
        this.errorCode =
          error instanceof APIError ? error.code : 'REQUEST_FAILED'
        throw error
      } finally {
        this.initialized = true
        this.loading = false
      }
    },
  },
})
