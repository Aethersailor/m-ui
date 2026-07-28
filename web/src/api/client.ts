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

export async function getHealth(signal?: AbortSignal): Promise<HealthResponse> {
  const response = await fetch('/api/v1/health', {
    headers: {
      Accept: 'application/json',
    },
    signal,
  })
  if (!response.ok) {
    throw new Error(`Health request failed with status ${response.status}`)
  }
  return (await response.json()) as HealthResponse
}
