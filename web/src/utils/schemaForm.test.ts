import { describe, expect, it } from 'vitest'
import { reactive } from 'vue'

import type { FieldCapability } from '@/api/management'

import {
  displayFieldValue,
  emptyObjectForFields,
  fieldVisible,
  objectListValue,
  parseFieldText,
  pathValue,
  recordEntries,
  recordFromEntries,
  secretPathConfigured,
  withPathValue,
} from './schemaForm'

describe('schema form helpers', () => {
  it('reads and immutably updates nested capability paths', () => {
    const original = { xhttp: { path: '/', mode: 'auto' } }
    const updated = withPathValue(original, 'xhttp.path', '/edge')
    expect(pathValue(updated, 'xhttp.path')).toBe('/edge')
    expect(original.xhttp.path).toBe('/')
  })

  it('creates missing objects and normalizes string-list values', () => {
    const updated = withPathValue({}, 'mux.brutal.enabled', true)
    expect(updated).toEqual({ mux: { brutal: { enabled: true } } })
    const field: FieldCapability = { path: 'alpn', label: 'ALPN', type: 'string-list' }
    expect(parseFieldText(field, 'h3, h2\nh3-29')).toEqual(['h3', 'h2', 'h3-29'])
    expect(displayFieldValue({ alpn: ['h3', 'h2'] }, field)).toBe('h3, h2')
  })

  it('updates Vue reactive models without cloning their proxies', () => {
    const original = reactive({ tls: { private_key: '' } })
    const updated = withPathValue(original, 'tls.private_key', 'secret')
    expect(updated).toEqual({ tls: { private_key: 'secret' } })
    expect(original.tls.private_key).toBe('')
  })

  it('evaluates schema visibility conditions', () => {
    const field: FieldCapability = {
      path: 'shadow_tls.users', label: 'Users', type: 'object-list',
      visible_when: { path: 'shadow_tls.version', equals: 3 },
    }
    expect(fieldVisible({ shadow_tls: { version: 3 } }, field)).toBe(true)
    expect(fieldVisible({ shadow_tls: { version: 2 } }, field)).toBe(false)
  })

  it('resolves stored secrets against rooted and component-prefixed paths', () => {
    const secrets = {
      'trojan.password': true,
      'security.tls.private_key': true,
    }
    expect(secretPathConfigured(secrets, 'trojan.password')).toBe(true)
    expect(secretPathConfigured(secrets, 'private_key', 'security.tls.')).toBe(true)
    expect(secretPathConfigured(secrets, 'vless.uuid')).toBe(false)
  })

  it('creates object-list rows and round-trips record rows', () => {
    const fields: FieldCapability[] = [
      { path: 'name', label: 'Name', type: 'string' },
      { path: 'password', label: 'Password', type: 'secret', secret: true },
    ]
    expect(emptyObjectForFields(fields)).toEqual({ name: '', password: '' })
    expect(objectListValue([{ name: 'alice' }, null])).toEqual([{ name: 'alice' }, {}])

    const rows = recordEntries({ 'cdn.example.com': { destination: 'origin.example.com:443' } })
    expect(rows).toEqual([{ key: 'cdn.example.com', value: { destination: 'origin.example.com:443' } }])
    expect(recordFromEntries(rows)).toEqual({ 'cdn.example.com': { destination: 'origin.example.com:443' } })
  })
})
