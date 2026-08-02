export function readStoredValue(key: string): string | null {
  try {
    return window.localStorage.getItem(key)
  } catch {
    return null
  }
}

export function writeStoredValue(key: string, value: string): void {
  try {
    window.localStorage.setItem(key, value)
  } catch {
    // Preferences remain usable in private browsing and restricted WebViews.
  }
}

