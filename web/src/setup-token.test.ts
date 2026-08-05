import { beforeEach, describe, expect, it, vi } from 'vitest'

async function setupModule() {
  vi.resetModules()
  return import('./setup-token')
}

describe('setup token input', () => {
  beforeEach(() => {
    window.history.replaceState(null, '', '/setup')
  })

  it('accepts a raw token and a complete setup link', async () => {
    const raw = 'a'.repeat(43)
    let module = await setupModule()
    expect(module.acceptSetupTokenInput(raw)).toBe(true)
    expect(module.getSetupToken()).toBe(raw)

    module = await setupModule()
    const linked = 'b'.repeat(43)
    expect(
      module.acceptSetupTokenInput(`https://panel.example/setup#token=${linked}`),
    ).toBe(true)
    expect(module.getSetupToken()).toBe(linked)
  })

  it('rejects malformed and query-string capabilities', async () => {
    const module = await setupModule()
    expect(module.acceptSetupTokenInput('not-a-token')).toBe(false)
    expect(
      module.acceptSetupTokenInput(
        `https://panel.example/setup?token=${'a'.repeat(43)}`,
      ),
    ).toBe(false)
    expect(module.getSetupToken()).toBe('')
  })

  it('consumes and removes a fragment before requests begin', async () => {
    const raw = 'c'.repeat(43)
    window.history.replaceState(null, '', `/setup#token=${raw}`)
    const module = await setupModule()
    module.consumeSetupTokenFragment()
    expect(module.getSetupToken()).toBe(raw)
    expect(window.location.hash).toBe('')
  })
})
