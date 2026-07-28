import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useAuthStore } from './auth'

describe('authentication store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('keeps credentials in memory after login', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            admin: { id: 'admin-id', username: 'admin' },
            csrf_token: 'synthetic-csrf-token',
            expires_at: '2026-07-28T12:00:00Z',
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      ),
    )

    const store = useAuthStore()
    await store.login('admin', 'synthetic-test-password')

    expect(store.authenticated).toBe(true)
    expect(store.admin?.username).toBe('admin')
    expect(store.csrfToken).toBe('synthetic-csrf-token')
  })

  it('treats an unauthorized session check as signed out', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            error: {
              code: 'AUTHENTICATION_REQUIRED',
              message: 'Authentication is required.',
            },
          }),
          {
            status: 401,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      ),
    )

    const store = useAuthStore()
    await store.initialize()

    expect(store.initialized).toBe(true)
    expect(store.authenticated).toBe(false)
    expect(store.errorCode).toBe('')
  })
})
