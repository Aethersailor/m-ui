import type {
  CapabilityManifest,
  ComponentCapability,
  ComponentGroup,
  NodeInput,
  ProtocolCapability,
  ProtocolKind,
  UserInput,
} from '@/api/management'
import { pathValue, withPathValue } from '@/utils/schemaForm'

export function protocolCapability(
  manifest: CapabilityManifest | null,
  kind: ProtocolKind,
): ProtocolCapability | undefined {
  return manifest?.protocols.find((item) => item.kind === kind)
}

export function componentCapabilities(
  manifest: CapabilityManifest | null,
  protocol: ProtocolKind,
  group: ComponentGroup,
): ComponentCapability[] {
  return protocolCapability(manifest, protocol)?.components.filter((item) => item.group === group) ?? []
}

export function componentCapability(
  manifest: CapabilityManifest | null,
  protocol: ProtocolKind,
  group: ComponentGroup,
  kind: string,
): ComponentCapability | undefined {
  return componentCapabilities(manifest, protocol, group).find((item) => item.kind === kind)
}

export function componentIdentifier(component: Pick<ComponentCapability, 'group' | 'kind'>): string {
  return `${component.group}:${component.kind}`
}

export function defaultComponentSelection(protocol: ProtocolCapability | undefined): Set<string> {
  const selected = new Set<string>()
  if (!protocol) return selected
  for (const layer of protocol.layers) {
    if (layer.default_component) selected.add(`${layer.group}:${layer.default_component}`)
  }
  return selected
}

export function componentSelection(
  protocol: ProtocolCapability | undefined,
  model: unknown,
): Set<string> {
  const selected = defaultComponentSelection(protocol)
  if (!protocol) return selected

  for (const layer of protocol.layers) {
    const components = protocol.components.filter((item) => item.group === layer.group)
    if (layer.multiple) {
      for (const component of components) {
        if (!component.enabled_path) continue
        const identifier = componentIdentifier(component)
        if (pathValue(model, component.enabled_path) === true) selected.add(identifier)
        else selected.delete(identifier)
      }
      continue
    }

    const selectionPath = components.find((item) => item.selection_path)?.selection_path
    const kind = selectionPath ? pathValue(model, selectionPath) : undefined
    if (typeof kind !== 'string' || !kind) continue
    replaceSelectionGroup(selected, layer.group, kind)
  }
  return selected
}

function replaceSelectionGroup(selected: Set<string>, group: ComponentGroup, kind: string) {
  for (const identifier of selected) {
    if (identifier.startsWith(`${group}:`)) selected.delete(identifier)
  }
  selected.add(`${group}:${kind}`)
}

export interface ComponentSelectionEvaluation {
  allowed: boolean
  selected: Set<string>
  reasons: string[]
}

export function evaluateComponentSelection(
  protocol: ProtocolCapability,
  candidate: ComponentCapability,
  currentSelection: ReadonlySet<string>,
): ComponentSelectionEvaluation {
  const selected = new Set(currentSelection)
  const layer = protocol.layers.find((item) => item.group === candidate.group)
  const reasons: string[] = []
  if (!layer) return { allowed: false, selected, reasons: [`undeclared layer ${candidate.group}`] }

  if (!layer.multiple) {
    for (const id of selected) {
      if (id.startsWith(`${candidate.group}:`)) selected.delete(id)
    }
  }
  selected.add(componentIdentifier(candidate))

  if (layer.locked && candidate.kind !== layer.default_component) {
    reasons.push(`layer ${layer.group} is locked to ${layer.default_component}`)
  }
  for (const currentLayer of protocol.layers) {
    const count = [...selected].filter((id) => id.startsWith(`${currentLayer.group}:`)).length
    if (currentLayer.required && count === 0) reasons.push(`layer ${currentLayer.group} is required`)
    if (!currentLayer.multiple && count > 1) reasons.push(`layer ${currentLayer.group} accepts one component`)
    if (currentLayer.locked && !selected.has(`${currentLayer.group}:${currentLayer.default_component}`)) {
      reasons.push(`layer ${currentLayer.group} is locked to ${currentLayer.default_component}`)
    }
  }
  for (const id of selected) {
    const component = protocol.components.find((item) => componentIdentifier(item) === id)
    if (!component) {
      reasons.push(`unknown component ${id}`)
      continue
    }
    for (const required of component.requires ?? []) {
      if (!selected.has(required)) reasons.push(`${id} requires ${required}`)
    }
    for (const conflict of component.conflicts ?? []) {
      if (selected.has(conflict)) reasons.push(`${id} conflicts with ${conflict}`)
    }
  }
  const uniqueReasons = [...new Set(reasons)]
  return { allowed: uniqueReasons.length === 0, selected, reasons: uniqueReasons }
}

export function componentOptions(
  protocol: ProtocolCapability | undefined,
  group: ComponentGroup,
  currentSelection: ReadonlySet<string>,
): Array<{ label: string; value: string; disabled: boolean }> {
  if (!protocol) return []
  return protocol.components.filter((item) => item.group === group).map((component) => ({
    label: component.label,
    value: component.kind,
    disabled: !evaluateComponentSelection(protocol, component, currentSelection).allowed,
  }))
}

export function cloneProtocolDefaults(
  manifest: CapabilityManifest | null,
  protocol: ProtocolKind,
): Partial<NodeInput> | undefined {
  const defaults = protocolCapability(manifest, protocol)?.default_node
  return defaults ? cloneJSON(defaults) : undefined
}

export function cloneUserDefaults(
  manifest: CapabilityManifest | null,
  protocol: ProtocolKind,
): Partial<UserInput> | undefined {
  const defaults = protocolCapability(manifest, protocol)?.default_user
  return defaults ? cloneJSON(defaults) : undefined
}

export function cloneComponentDefaults<T>(
  manifest: CapabilityManifest | null,
  protocol: ProtocolKind,
  group: ComponentGroup,
  kind: string,
): T | undefined {
  const defaults = componentCapability(manifest, protocol, group, kind)?.default_config
  return defaults === undefined ? undefined : cloneJSON(defaults as T)
}

export function componentConfig(
  model: unknown,
  component: ComponentCapability,
): Record<string, unknown> {
  const value = component.config_path ? pathValue(model, component.config_path) : model
  return isRecord(value) ? value : {}
}

export function selectComponentConfig<T extends Record<string, unknown>>(
  model: T,
  component: ComponentCapability,
): T {
  const defaults = cloneJSON(component.default_config)
  let next = component.config_path
    ? withPathValue(model, component.config_path, defaults)
    : defaults as T
  if (component.selection_path) {
    next = withPathValue(next, component.selection_path, component.kind)
  }
  return next
}

export function setComponentEnabled<T extends Record<string, unknown>>(
  model: T,
  component: ComponentCapability,
  enabled: boolean,
): T {
  let next = model
  if (enabled) next = selectComponentConfig(next, component)
  return component.enabled_path
    ? withPathValue(next, component.enabled_path, enabled)
    : next
}

export function updateComponentConfig<T extends Record<string, unknown>>(
  model: T,
  component: ComponentCapability,
  value: Record<string, unknown>,
): T {
  return component.config_path
    ? withPathValue(model, component.config_path, value)
    : value as T
}

export function componentSecretPrefix(component: ComponentCapability): string {
  return component.config_path ? `${component.config_path}.` : ''
}

export function protocolLabel(
  manifest: CapabilityManifest | null,
  protocol: ProtocolKind,
): string {
  return protocolCapability(manifest, protocol)?.label ?? protocol
}

export function replaceProtocolDefaults<T extends Record<string, unknown>>(
  model: T,
  manifest: CapabilityManifest,
  protocol: ProtocolKind,
): T {
  const next = cloneJSON(model)
  const protocolKeys = new Set(manifest.protocols.flatMap((item) => Object.keys(item.default_node)))
  for (const key of protocolKeys) delete next[key]
  Object.assign(next, cloneProtocolDefaults(manifest, protocol) ?? {}, { protocol })
  return next
}

// Capability manifests are JSON API documents. JSON cloning deliberately strips
// Vue's reactive proxies while preserving the complete wire representation.
function cloneJSON<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
