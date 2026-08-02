import { defineStore } from 'pinia'

import {
  login as loginRequest,
  logout as logoutRequest,
  me,
  readCSRFCookie,
  type Admin,
  type LoginResponse,
} from '@/api/auth'
import { APIError } from '@/api/client'

export const useAuthStore = defineStore('auth', {
  state: () => ({
    admin: null as Admin | null,
    csrfToken: '',
    initialized: false,
    loading: false,
    errorCode: '',
    errorRetryAfter: 0,
  }),
  getters: {
    authenticated: (state) => state.admin !== null,
  },
  actions: {
    acceptCredentials(response: LoginResponse) {
      this.admin = response.admin
      this.csrfToken = response.csrf_token
      this.initialized = true
    },
    async initialize() {
      if (this.initialized) {
        return
      }
      try {
        const response = await me()
        this.admin = response.admin
        this.csrfToken = readCSRFCookie()
      } catch (error) {
        if (!(error instanceof APIError) || error.status !== 401) {
          this.errorCode = 'SESSION_CHECK_FAILED'
        }
        this.admin = null
        this.csrfToken = ''
      } finally {
        this.initialized = true
      }
    },
    async login(username: string, password: string) {
      this.loading = true
      this.errorCode = ''
      this.errorRetryAfter = 0
      try {
        const response = await loginRequest(username, password)
        this.acceptCredentials(response)
      } catch (error) {
        this.admin = null
        this.csrfToken = ''
        this.errorCode =
          error instanceof APIError ? error.code : 'AUTHENTICATION_FAILED'
        this.errorRetryAfter =
          error instanceof APIError ? error.retryAfter : 0
        throw error
      } finally {
        this.loading = false
      }
    },
    async logout() {
      const token = this.csrfToken || readCSRFCookie()
      try {
        await logoutRequest(token)
      } finally {
        this.admin = null
        this.csrfToken = ''
        this.initialized = true
      }
    },
  },
})
