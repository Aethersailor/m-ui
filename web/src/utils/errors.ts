import { APIError } from '@/api/client'

export function errorTranslationKey(error: unknown): string {
  const code = error instanceof APIError ? error.code : 'REQUEST_FAILED'
  const supported = new Set([
    'CONFIG_VALIDATION_FAILED',
    'SYSTEM_DEGRADED',
    'RESOURCE_NOT_FOUND',
    'REQUEST_FAILED',
    'INTERNAL_ERROR',
    'CORE_OPERATION_IN_PROGRESS',
    'CORE_NOT_MANAGED',
    'CORE_BACKUP_UNAVAILABLE',
  ])
  return supported.has(code) ? `errors.${code}` : 'common.error'
}
