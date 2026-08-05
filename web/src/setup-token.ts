let consumed = false
let setupToken = ''

const bootstrapTokenPattern = /^[A-Za-z0-9_-]{43}$/

function tokenFromInput(input: string): string {
  const trimmed = input.trim()
  if (bootstrapTokenPattern.test(trimmed)) {
    return trimmed
  }
  try {
    const parsed = new URL(trimmed)
    const value = new URLSearchParams(parsed.hash.slice(1)).get('token') ?? ''
    return bootstrapTokenPattern.test(value) ? value : ''
  } catch {
    const value = new URLSearchParams(trimmed.replace(/^#/, '')).get('token') ?? ''
    return bootstrapTokenPattern.test(value) ? value : ''
  }
}

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

export function acceptSetupTokenInput(input: string): boolean {
  const value = tokenFromInput(input)
  if (!value) {
    return false
  }
  setupToken = value
  return true
}
