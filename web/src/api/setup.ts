import { apiRequest } from './client'

export interface SetupStatus {
  state: 'required' | 'complete'
  language_default: 'auto' | 'zh-CN' | 'en-US'
  password_policy: {
    minimum_characters: number
    maximum_bytes: number
  }
}

export interface SetupResponse {
  admin: {
    id: string
    username: string
  }
  csrf_token: string
  expires_at: string
}

export function getSetupStatus(): Promise<SetupStatus> {
  return apiRequest<SetupStatus>('/api/v1/setup/status')
}

export function completeSetup(
  username: string,
  password: string,
): Promise<SetupResponse> {
  return apiRequest<SetupResponse>('/api/v1/setup/complete', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ username, password }),
  })
}
