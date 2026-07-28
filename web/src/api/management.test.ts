import { describe, expect, it, vi } from 'vitest'

import { setListenerEnabled } from './management'

describe('management API', () => {
  it('sends authenticated mutations with the CSRF header', async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          listener: {
            id: 'listener-1',
            name: 'Synthetic listener',
            enabled: true,
            listen_address: '0.0.0.0',
            listen_port: 443,
            public_host_override: '',
            public_port_override: null,
            server_name: 'example.invalid',
            reality_dest: 'example.invalid:443',
            reality_public_key: 'synthetic-public-key',
            reality_private_key_set: true,
            short_id: '0123456789abcdef',
            udp_enabled: false,
            users: [],
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

    const listener = await setListenerEnabled(
      'synthetic-csrf-token',
      'listener-1',
      true,
    )

    expect(listener.enabled).toBe(true)
    expect(fetchMock).toHaveBeenCalledOnce()
    const [path, init] = fetchMock.mock.calls[0] as [string, RequestInit]
    expect(path).toBe('/api/v1/listeners/listener-1/enable')
    expect(init.method).toBe('POST')
    expect(init.credentials).toBe('same-origin')
    expect(new Headers(init.headers).get('X-CSRF-Token')).toBe(
      'synthetic-csrf-token',
    )
  })
})
