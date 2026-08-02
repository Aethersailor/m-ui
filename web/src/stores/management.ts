import { defineStore } from 'pinia'

import {
	getEndpointSettings,
	getRuntimeLogs,
  getRuntimeStatus,
  getSettings,
  listAuditEntries,
  listListeners,
  listRevisions,
  type AuditEntry,
  type Listener,
  type Revision,
  type RuntimeLog,
  type RuntimeStatus,
	type Settings,
	type EndpointSettingsState,
} from '@/api/management'
import { APIError } from '@/api/client'

export const useManagementStore = defineStore('management', {
  state: () => ({
    listeners: [] as Listener[],
    runtime: null as RuntimeStatus | null,
    revisions: [] as Revision[],
		settings: null as Settings | null,
		endpointSettings: null as EndpointSettingsState | null,
    logs: [] as RuntimeLog[],
    audit: [] as AuditEntry[],
    loading: false,
    errorCode: '',
  }),
  getters: {
    enabledUserCount: (state) =>
      state.listeners.reduce(
        (total, listener) =>
          total + listener.users.filter((user) => user.enabled).length,
        0,
      ),
    activeRevision: (state) =>
      state.revisions.find((revision) => revision.status === 'active') ?? null,
  },
  actions: {
    async loadShellSettings() {
      if (this.settings) {
        return
      }
		this.settings = await getSettings()
		this.endpointSettings = await getEndpointSettings()
    },
    async loadOverview() {
      this.loading = true
      this.errorCode = ''
      try {
        const [listeners, runtime, revisions] = await Promise.all([
          listListeners(),
          getRuntimeStatus(),
          listRevisions(),
        ])
        this.listeners = listeners
        this.runtime = runtime
        this.revisions = revisions
      } catch (error) {
        this.errorCode =
          error instanceof APIError ? error.code : 'REQUEST_FAILED'
        throw error
      } finally {
        this.loading = false
      }
    },
    async refreshRuntime() {
      try {
        this.runtime = await getRuntimeStatus()
      } catch (error) {
        this.errorCode =
          error instanceof APIError ? error.code : 'REQUEST_FAILED'
      }
    },
    async refreshListeners() {
      this.listeners = await listListeners()
    },
    async refreshRevisions() {
      this.revisions = await listRevisions()
    },
    async loadSystem() {
		const [settings, endpoints, logs, audit] = await Promise.all([
			getSettings(),
			getEndpointSettings(),
			getRuntimeLogs(),
        listAuditEntries(),
      ])
		this.settings = settings
		this.endpointSettings = endpoints
      this.logs = logs
      this.audit = audit
    },
  },
})
