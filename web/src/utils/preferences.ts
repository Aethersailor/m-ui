export type LanguageMode = 'auto' | 'zh-CN' | 'en-US'

import { readStoredValue } from './storage'

export const languageStorageKey = 'm-ui-language-mode'

export function isLanguageMode(value: string | null): value is LanguageMode {
  return value === 'auto' || value === 'zh-CN' || value === 'en-US'
}

export function readLanguageMode(): LanguageMode {
  const saved = readStoredValue(languageStorageKey)
  return isLanguageMode(saved) ? saved : 'auto'
}

export function detectBrowserLanguage(): 'zh-CN' | 'en-US' {
  return navigator.language.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en-US'
}

export function resolveLanguage(
  mode: LanguageMode,
  serverDefault: LanguageMode = 'auto',
): 'zh-CN' | 'en-US' {
  if (mode !== 'auto') {
    return mode
  }
  if (serverDefault !== 'auto') {
    return serverDefault
  }
  return detectBrowserLanguage()
}
