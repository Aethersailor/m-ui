import { apiRequest } from './client'

export interface Admin {
  id: string
  username: string
}

interface MeResponse {
  admin: Admin
}

interface LoginResponse {
  admin: Admin
  csrf_token: string
  expires_at: string
}

export function login(
  username: string,
  password: string,
): Promise<LoginResponse> {
  return apiRequest<LoginResponse>('/api/v1/auth/login', {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ username, password }),
  })
}

export function me(): Promise<MeResponse> {
  return apiRequest<MeResponse>('/api/v1/auth/me')
}

export function logout(csrfToken: string): Promise<void> {
  return apiRequest<void>('/api/v1/auth/logout', {
    method: 'POST',
    headers: {
      'X-CSRF-Token': csrfToken,
    },
  })
}

export function readCSRFCookie(): string {
  const prefix = 'm_ui_csrf='
  for (const value of document.cookie.split(';')) {
    const cookie = value.trim()
    if (cookie.startsWith(prefix)) {
      return decodeURIComponent(cookie.slice(prefix.length))
    }
  }
  return ''
}
