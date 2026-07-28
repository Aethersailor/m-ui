export interface BuildInfo {
  version: string
  commit: string
  date: string
  dirty: boolean
}

export interface HealthResponse {
  status: 'ok'
  time: string
  build: BuildInfo
}

interface ErrorEnvelope {
  error?: {
    code?: string
    message?: string
    request_id?: string
  }
}

export class APIError extends Error {
  readonly status: number
  readonly code: string
  readonly requestID: string

  constructor(
    status: number,
    code: string,
    message: string,
    requestID = '',
  ) {
    super(message)
    this.name = 'APIError'
    this.status = status
    this.code = code
    this.requestID = requestID
  }
}

export async function apiRequest<T>(
  path: string,
  init: RequestInit = {},
): Promise<T> {
  const headers = new Headers(init.headers)
  headers.set('Accept', 'application/json')
  const response = await fetch(path, {
    ...init,
    credentials: 'same-origin',
    headers,
  })
  if (response.status === 204) {
    return undefined as T
  }

  const contentType = response.headers.get('Content-Type') ?? ''
  const payload = contentType.includes('application/json')
    ? ((await response.json()) as T & ErrorEnvelope)
    : undefined
  if (!response.ok) {
    throw new APIError(
      response.status,
      payload?.error?.code ?? 'REQUEST_FAILED',
      payload?.error?.message ?? `Request failed with status ${response.status}`,
      payload?.error?.request_id,
    )
  }
  return payload as T
}

export async function getHealth(signal?: AbortSignal): Promise<HealthResponse> {
  return apiRequest<HealthResponse>('/api/v1/health', {
    signal,
  })
}
