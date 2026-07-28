import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useHealthStore } from './health'

describe('health store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    vi.restoreAllMocks()
  })

  it('loads backend health', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            status: 'ok',
            time: '2026-07-28T00:00:00Z',
            build: {
              version: 'test',
              commit: 'test-commit',
              date: '2026-07-28T00:00:00Z',
              dirty: false,
            },
          }),
          {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          },
        ),
      ),
    )

    const store = useHealthStore()
    await store.refresh()

    expect(store.error).toBe('')
    expect(store.health?.status).toBe('ok')
    expect(store.health?.build.version).toBe('test')
  })

  it('records a failed health request', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(new Response(null, { status: 503 })),
    )

    const store = useHealthStore()
    await store.refresh()

    expect(store.health).toBeNull()
    expect(store.error).toContain('503')
  })
})
