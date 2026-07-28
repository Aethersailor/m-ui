import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'

import { useThemeStore } from './theme'

describe('theme store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
    vi.stubGlobal(
      'matchMedia',
      vi.fn().mockReturnValue({
        matches: true,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    )
  })

  it('uses the system preference in auto mode', () => {
    const store = useThemeStore()

    store.initialize()

    expect(store.mode).toBe('auto')
    expect(store.dark).toBe(true)
  })

  it('persists an explicit theme choice', () => {
    const store = useThemeStore()
    store.initialize()

    store.setMode('light')

    expect(store.dark).toBe(false)
    expect(localStorage.getItem('m-ui-theme')).toBe('light')
  })
})
