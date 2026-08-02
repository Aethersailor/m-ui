import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'

import { usePreferencesStore } from './preferences'

describe('browser preferences store', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    localStorage.clear()
  })

  it('keeps an explicit language choice across initialization', () => {
    localStorage.setItem('m-ui-language-mode', 'zh-CN')
    const store = usePreferencesStore()

    store.initialize('en-US')

    expect(store.languageMode).toBe('zh-CN')
    expect(store.language).toBe('zh-CN')
    expect(localStorage.getItem('m-ui-language-mode')).toBe('zh-CN')
  })

  it('uses the server default when browser language mode is automatic', () => {
    const store = usePreferencesStore()

    store.initialize('zh-CN')

    expect(store.languageMode).toBe('auto')
    expect(store.language).toBe('zh-CN')
  })
})
