/* global localStorage, navigator, window, document */
(() => {
  const languageKey = 'm-ui-language-mode'
  const themeKey = 'm-ui-theme-mode'
  const legacyThemeKey = 'm-ui-theme'
  const language = localStorage.getItem(languageKey)
  const browserLanguage = navigator.language.toLowerCase().startsWith('zh')
    ? 'zh-CN'
    : 'en-US'
  const effectiveLanguage =
    language === 'zh-CN' || language === 'en-US' ? language : browserLanguage
  const savedTheme =
    localStorage.getItem(themeKey) || localStorage.getItem(legacyThemeKey)
  const theme = savedTheme === 'light' || savedTheme === 'dark'
    ? savedTheme
    : window.matchMedia('(prefers-color-scheme: dark)').matches
      ? 'dark'
      : 'light'
  document.documentElement.lang = effectiveLanguage
  document.documentElement.dataset.theme = theme
})()
