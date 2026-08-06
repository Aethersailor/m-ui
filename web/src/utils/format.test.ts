import { describe, expect, it } from 'vitest'

import { formatBytes, formatCredentialSummary, maskCredential } from './format'

describe('format utilities', () => {
  it('formats binary byte values', () => {
    expect(formatBytes(0)).toBe('0 B')
    expect(formatBytes(1536)).toBe('1.5 KiB')
  })

  it('masks credentials while retaining identification edges', () => {
    expect(maskCredential('2bf189fe-ec56-497d-9069-68bf32c4425b')).toBe(
      '2bf189fe••••425b',
    )
  })

  it('shows a configured state when the API redacts a stored credential', () => {
    expect(formatCredentialSummary('', true, 'Configured')).toBe('Configured')
    expect(formatCredentialSummary('', false, 'Configured')).toBe('')
    expect(formatCredentialSummary('short-secret', true, 'Configured')).toBe('••••••••')
  })
})
