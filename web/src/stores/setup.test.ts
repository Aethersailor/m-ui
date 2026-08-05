import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import { useSetupStore } from './setup'

describe('setup store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
  })

  it('marks setup complete before navigating away from the setup page', () => {
    const store = useSetupStore()
    store.status = {
      state: 'required',
      language_default: 'auto',
      password_policy: {
        minimum_characters: 12,
        maximum_bytes: 1024,
      },
    }
    store.initialized = true
    store.errorCode = 'STALE_ERROR'

    store.markComplete()

    expect(store.required).toBe(false)
    expect(store.complete).toBe(true)
    expect(store.status?.language_default).toBe('auto')
    expect(store.status?.password_policy.minimum_characters).toBe(12)
    expect(store.initialized).toBe(true)
    expect(store.errorCode).toBe('')
  })
})
