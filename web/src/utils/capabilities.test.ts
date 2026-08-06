import { describe, expect, it } from 'vitest'
import { reactive } from 'vue'

import type { CapabilityManifest, VLESSSpec } from '@/api/management'

import {
  cloneComponentDefaults,
  cloneProtocolDefaults,
  cloneUserDefaults,
  componentCapabilities,
  componentConfig,
  componentIdentifier,
  componentOptions,
  componentSelection,
  defaultComponentSelection,
  evaluateComponentSelection,
  protocolCapability,
  replaceProtocolDefaults,
  selectComponentConfig,
  setComponentEnabled,
  updateComponentConfig,
} from './capabilities'

const manifest: CapabilityManifest = {
  schema_version: 4,
  node_schema_version: 1,
  source: { repository: 'MetaCubeX/mihomo', branch: 'Meta', commit: 'abc123' },
  node_fields: [],
  access_profile_fields: [],
  protocols: [{
    kind: 'vless',
    label: 'VLESS',
    default_node: {
      vless: { handler: { type: 'raw' }, security: { type: 'reality', reality: { destination: '', private_key: '', public_key: '', short_ids: [], server_names: [] } } },
    },
    default_user: { vless: { uuid: '', flow: 'xtls-rprx-vision' } },
    layers: [
      { group: 'transport', required: true, multiple: false, default_component: 'raw' },
      { group: 'security', required: true, multiple: false, default_component: 'reality' },
    ],
    components: [
      { group: 'transport', kind: 'raw', label: 'Raw TCP', config_path: 'vless.handler', selection_path: 'vless.handler.type', default_config: { type: 'raw' } },
      { group: 'transport', kind: 'websocket', label: 'WebSocket', config_path: 'vless.handler', selection_path: 'vless.handler.type', default_config: { type: 'websocket', websocket: { path: '/' } } },
      { group: 'security', kind: 'reality', label: 'REALITY', config_path: 'vless.security', selection_path: 'vless.security.type', default_config: { type: 'reality', reality: { destination: '', private_key: '', public_key: '', short_ids: [], server_names: [] } } },
    ],
    user_fields: [],
  }],
}

describe('capability schema helpers', () => {
  it('selects protocol layers and component options from the manifest', () => {
    expect(protocolCapability(manifest, 'vless')?.label).toBe('VLESS')
    expect(componentCapabilities(manifest, 'vless', 'transport').map((item) => item.kind)).toEqual(['raw', 'websocket'])
  })

  it('returns isolated protocol, component, and user defaults', () => {
    const first = cloneProtocolDefaults(manifest, 'vless')
    const second = cloneProtocolDefaults(manifest, 'vless')
    expect(first?.vless?.handler.type).toBe('raw')
    first!.vless!.handler.type = 'websocket'
    expect(second?.vless?.handler.type).toBe('raw')

    const handler = cloneComponentDefaults<VLESSSpec['handler']>(manifest, 'vless', 'transport', 'websocket')
    expect(handler).toEqual({ type: 'websocket', websocket: { path: '/' } })
    expect(cloneUserDefaults(manifest, 'vless')).toEqual({ vless: { uuid: '', flow: 'xtls-rprx-vision' } })
  })

  it('clones defaults after Vue makes the capability manifest reactive', () => {
    const reactiveManifest = reactive(manifest)
    expect(cloneProtocolDefaults(reactiveManifest, 'vless')?.vless?.handler.type).toBe('raw')
    expect(cloneComponentDefaults<VLESSSpec['handler']>(reactiveManifest, 'vless', 'transport', 'websocket')).toEqual({
      type: 'websocket',
      websocket: { path: '/' },
    })
  })

  it('enforces required, conflicting, single-choice, and locked layers', () => {
    const protocol = structuredClone(manifest.protocols[0]!)
    protocol.layers.push({ group: 'extension', required: false, multiple: true })
    protocol.components.push({
      group: 'extension', kind: 'edge', label: 'Edge', config_path: 'vless.edge',
      enabled_path: 'vless.edge.enabled', default_config: {},
      requires: ['transport:websocket'], conflicts: ['security:reality'],
    })
    const selected = defaultComponentSelection(protocol)
    const extension = protocol.components.at(-1)!
    const evaluation = evaluateComponentSelection(protocol, extension, selected)
    expect(evaluation.allowed).toBe(false)
    expect(evaluation.reasons).toEqual(expect.arrayContaining([
      'extension:edge requires transport:websocket',
      'extension:edge conflicts with security:reality',
    ]))

    expect(componentOptions(protocol, 'transport', selected)).toEqual([
      { label: 'Raw TCP', value: 'raw', disabled: false },
      { label: 'WebSocket', value: 'websocket', disabled: false },
    ])
    expect(componentIdentifier(extension)).toBe('extension:edge')

    protocol.layers[0]!.locked = true
    expect(evaluateComponentSelection(protocol, protocol.components[1]!, selected).allowed).toBe(false)
  })

  it('reads and changes component configuration through manifest paths', () => {
    const protocol = structuredClone(manifest.protocols[0]!)
    const websocket = protocol.components[1]!
    const model = structuredClone(protocol.default_node) as Record<string, unknown>

    expect(componentSelection(protocol, model)).toEqual(new Set(['transport:raw', 'security:reality']))
    const switched = selectComponentConfig(model, websocket)
    expect(componentSelection(protocol, switched)).toEqual(new Set(['transport:websocket', 'security:reality']))
    expect(componentConfig(switched, websocket)).toEqual({ type: 'websocket', websocket: { path: '/' } })

    const updated = updateComponentConfig(switched, websocket, { type: 'websocket', websocket: { path: '/ws' } })
    expect(componentConfig(updated, websocket)).toEqual({ type: 'websocket', websocket: { path: '/ws' } })
  })

  it('replaces protocol-owned keys and toggles multi-value components generically', () => {
    const withSecondProtocol: CapabilityManifest = structuredClone(manifest)
    withSecondProtocol.protocols.push({
      kind: 'future', label: 'Future', default_node: { future: { mode: 'fast' } }, default_user: { future: { token: '' } },
      layers: [{ group: 'extension', required: false, multiple: true }],
      components: [{
        group: 'extension', kind: 'metrics', label: 'Metrics', config_path: 'future.metrics',
        enabled_path: 'future.metrics.enabled', default_config: { enabled: false, path: '/metrics' },
      }],
      user_fields: [],
    })
    const next = replaceProtocolDefaults({ ...manifest.protocols[0]!.default_node, protocol: 'vless', name: 'kept' }, withSecondProtocol, 'future')
    expect(next).toEqual({ protocol: 'future', name: 'kept', future: { mode: 'fast' } })

    const component = withSecondProtocol.protocols[1]!.components[0]!
    const enabled = setComponentEnabled(next, component, true)
    expect(componentSelection(withSecondProtocol.protocols[1], enabled)).toContain('extension:metrics')
    expect(componentConfig(enabled, component)).toEqual({ enabled: true, path: '/metrics' })
  })
})
