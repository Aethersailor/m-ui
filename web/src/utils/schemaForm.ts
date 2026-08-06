import type { FieldCapability } from '@/api/management'

export function pathValue(model: unknown, path: string): unknown {
  let current = model
  for (const segment of path.split('.')) {
    if (typeof current !== 'object' || current === null) return undefined
    current = (current as Record<string, unknown>)[segment]
  }
  return current
}

export function withPathValue<T>(model: T, path: string, value: unknown): T {
  const segments = path.split('.')
  return setPathValue(model, segments, value) as T
}

function setPathValue(current: unknown, segments: string[], value: unknown): unknown {
  if (!segments.length) return value
  const [head, ...tail] = segments
  const source = isRecord(current) ? current : {}
  return { ...source, [head]: setPathValue(source[head], tail, value) }
}

export function displayFieldValue(model: unknown, field: FieldCapability): string {
  const value = pathValue(model, field.path)
  if (field.type === 'string-list') return Array.isArray(value) ? value.join(', ') : ''
  return typeof value === 'string' ? value : ''
}

export function parseFieldText(field: FieldCapability, value: string): unknown {
  if (field.type === 'string-list') {
    return value.split(/[\n,]/).map((item) => item.trim()).filter(Boolean)
  }
  return value
}

export function fieldVisible(model: unknown, field: FieldCapability): boolean {
  if (!field.visible_when) return true
  return Object.is(pathValue(model, field.visible_when.path), field.visible_when.equals)
}

export function emptyFieldValue(field: FieldCapability): unknown {
  if (field.type === 'boolean') return false
  if (field.type === 'integer') return field.minimum ?? 0
  if (field.type === 'string-list' || field.type === 'object-list') return []
  if (field.type === 'record') return {}
  return field.options?.[0] ?? ''
}

export function emptyObjectForFields(fields: FieldCapability[]): Record<string, unknown> {
  return fields.reduce<Record<string, unknown>>(
    (result, field) => withPathValue(result, field.path, emptyFieldValue(field)),
    {},
  )
}

export function objectListValue(value: unknown): Array<Record<string, unknown>> {
  if (!Array.isArray(value)) return []
  return value.map((item) => isRecord(item) ? item : {})
}

export interface RecordEntry {
  key: string
  value: Record<string, unknown>
}

export function recordEntries(value: unknown): RecordEntry[] {
  if (!isRecord(value)) return []
  return Object.entries(value).map(([key, item]) => ({ key, value: isRecord(item) ? item : {} }))
}

export function recordFromEntries(entries: RecordEntry[]): Record<string, Record<string, unknown>> {
  return Object.fromEntries(
    entries.filter((entry) => entry.key.trim()).map((entry) => [entry.key.trim(), entry.value]),
  )
}

export function cloneJSONValue<T>(value: T): T {
  return JSON.parse(JSON.stringify(value)) as T
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}
