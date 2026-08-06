import { describe, expect, it, vi } from 'vitest'

import {
  cloneNode,
  createUsers,
  restartApplication,
  setNodeEnabled,
  setNodesEnabled,
  setUsersEnabled,
  updateCore,
} from './management'

describe('management API', () => {
  it('sends authenticated mutations with the CSRF header', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          node: {
            id: 'node-1',
            name: 'Synthetic node',
            enabled: true,
            listen: '0.0.0.0',
            port: '443',
            protocol: 'vless',
            schema_version: 1,
            vless: { handler: { type: 'raw' }, security: { type: 'none' } },
            users: [],
            access_profiles: [],
            secrets_set: {},
            generation: 1,
            created_at: '2026-07-28T00:00:00Z',
            updated_at: '2026-07-28T00:00:00Z',
          },
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    const node = await setNodeEnabled(
      'synthetic-csrf-token',
      'node-1',
      true,
    )

    expect(node.enabled).toBe(true)
    expect(fetchMock).toHaveBeenCalledOnce()
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/nodes/node-1/enable')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('same-origin')
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe(
      'synthetic-csrf-token',
    )
  })

  it('routes core updates through the authenticated CSRF mutation path', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          changed: false,
          manifest: {
            schema_version: 1,
            source: 'bootstrap',
            verified_source: true,
            compressed_sha256: 'a'.repeat(64),
            binary_sha256: 'b'.repeat(64),
            binary_size: 123,
            binary_reported_version: 'Mihomo Meta v1.0.0',
            installed_at: '2026-07-30T00:00:00Z',
          },
        }),
        {
          status: 200,
          headers: { 'Content-Type': 'application/json' },
        },
      ),
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await updateCore('synthetic-core-csrf')

    expect(result.changed).toBe(false)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/system/core/update')
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe(
      'synthetic-core-csrf',
    )
  })

  it('requests an application restart through the authenticated Web API', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ restarting: true }), {
        status: 202,
        headers: { 'Content-Type': 'application/json' },
      }),
    )
    vi.stubGlobal('fetch', fetchMock)

    const result = await restartApplication('synthetic-restart-csrf')

    expect(result.restarting).toBe(true)
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/system/restart')
    expect(init.method).toBe('POST')
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe(
      'synthetic-restart-csrf',
    )
  })

  it('uses the stable batch and clone mutation contracts', async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ node: {}, nodes: [], users: [], revision: {} }), {
        status: 200,
        headers: { 'Content-Type': 'application/json' },
      }),
    ))
    vi.stubGlobal('fetch', fetchMock)

    await cloneNode('csrf', 'source/node', { name: 'Clone', port: '8443', include_users: true })
    await setNodesEnabled('csrf', ['node-1', 'node-2'], false)
    await createUsers('csrf', 'node-1', [{ name: 'alice', enabled: true, expires_at: null, future: { token: '' } }])
    await setUsersEnabled('csrf', 'node-1', ['user-1'], true)

    const requests = fetchMock.mock.calls.map(([path, init]) => ({
      path,
      method: (init as RequestInit).method,
      body: JSON.parse(String((init as RequestInit).body)),
    }))
    expect(requests).toEqual([
      { path: '/api/v1/nodes/source%2Fnode/clone', method: 'POST', body: { name: 'Clone', port: '8443', include_users: true } },
      { path: '/api/v1/nodes/batch-enabled', method: 'POST', body: { node_ids: ['node-1', 'node-2'], enabled: false } },
      { path: '/api/v1/nodes/node-1/users/batch', method: 'POST', body: { users: [{ name: 'alice', enabled: true, expires_at: null, future: { token: '' } }] } },
      { path: '/api/v1/nodes/node-1/users/batch-enabled', method: 'POST', body: { user_ids: ['user-1'], enabled: true } },
    ])
  })
})
