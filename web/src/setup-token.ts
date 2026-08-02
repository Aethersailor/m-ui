let consumed = false
let setupToken = ''

const bootstrapTokenPattern = /^[A-Za-z0-9_-]{43}$/

// Consume the setup capability before the router is created or any request is
// issued. It is intentionally kept only in this module's memory.
export function consumeSetupTokenFragment(): void {
  if (consumed || typeof window === 'undefined') {
    return
  }
  consumed = true
  const fragment = window.location.hash
  if (!fragment) {
    return
  }
  const value = new URLSearchParams(fragment.slice(1)).get('token') ?? ''
  if (bootstrapTokenPattern.test(value)) {
    setupToken = value
  }
  window.history.replaceState(
    null,
    document.title,
    `${window.location.pathname}${window.location.search}`,
  )
}

export function getSetupToken(): string {
  return setupToken
}

