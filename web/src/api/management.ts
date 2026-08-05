import { apiRequest } from './client'

export interface User {
  id: string
  listener_id: string
  name: string
  enabled: boolean
  uuid: string
  expires_at: string | null
  created_at: string
  updated_at: string
}

export interface Listener {
  id: string
  name: string
  enabled: boolean
  listen_address: string
  listen_port: number
  public_host_override: string
  public_port_override: number | null
  server_name: string
  reality_dest: string
  reality_public_key: string
  reality_private_key_set: boolean
  short_id: string
  udp_enabled: boolean
  users: User[]
  created_at: string
  updated_at: string
}

export interface ListenerInput {
  name: string
  listen_address: string
  listen_port: number
  public_host_override: string
  public_port_override: number | null
  server_name: string
  reality_dest: string
  reality_private_key?: string
  reality_public_key?: string
  short_id?: string
  udp_enabled: boolean
}

export interface UserInput {
  name: string
  uuid?: string
  expires_at: string | null
}

export interface Revision {
  id: string
  revision_number: number
  sha256: string
  status: 'pending' | 'active' | 'failed' | 'rolled_back'
  reason: string
  actor_admin_id: string
  error_message: string
  created_at: string
  activated_at: string | null
}

export interface RuntimeStatus {
  active: boolean
  degraded: boolean
  degraded_reason: string
  version: {
    meta: boolean
    version: string
  }
  traffic: {
    up: number
    down: number
    upTotal: number
    downTotal: number
  }
  memory: {
    inuse: number
    oslimit: number
  }
  connection_count: number
  download_total: number
  upload_total: number
  observed_at: string
}

export interface RuntimeLog {
  timestamp: string
  message: string
}

export interface Settings {
  panel_title: string
  ui_language: 'auto' | 'en-US' | 'zh-CN'
  public_host: string
  cookie_secure: boolean
  requires_mui_restart: boolean
}

export type SettingsInput = Omit<Settings, 'requires_mui_restart'>

export interface EndpointValue {
  host: string
  port: number
}

export interface EndpointSettings {
  panel_ui_bind: EndpointValue
  mihomo_external_controller_bind: EndpointValue
  mihomo_controller_connect: EndpointValue
  external_controller_cors_origins: string[]
  generation: number
  requires_mui_restart?: boolean
  requires_mihomo_restart?: boolean
  updated_at?: string
}

export interface EndpointSettingsState {
  active: EndpointSettings
  pending: EndpointSettings | null
}

export interface AuditEntry {
  id: string
  actor_admin_id: string
  action: string
  resource_type: string
  resource_id: string
  result: 'success' | 'failure'
  summary: string
  created_at: string
}

export interface CoreIdentity {
  channel: 'release' | 'alpha'
  repository: 'MetaCubeX/mihomo'
  release_id: number
  tag_name: string
  prerelease: boolean
  published_at: string
  target_commitish?: string
  asset_id: number
  asset_name: string
  asset_size: number
  asset_digest_sha256: string
  binary_reported_version?: string
}

export interface CoreManifest {
  schema_version: number
  source: 'downloaded' | 'bootstrap' | 'adopted'
  verified_source: boolean
  identity: CoreIdentity
  compressed_sha256: string
  binary_sha256: string
  binary_size: number
  binary_reported_version: string
  installed_at: string
}

export interface CoreSettings {
  channel: 'release' | 'alpha'
  auto_update: boolean
  check_interval: '6h0m0s' | '12h0m0s' | '24h0m0s' | '168h0m0s'
  managed: boolean
  external_path?: string
}

export interface CoreStatus {
  settings: CoreSettings
  state: {
    current?: CoreManifest
    available?: CoreIdentity
    last_check_at?: string
    last_check_result?: string
    last_update_at?: string
    last_update_result?: string
    last_error_redacted?: string
    next_check_at?: string
    update_in_progress: boolean
  }
  actual_version: string
  controller_version?: string
  process_active: boolean
  controller_reachable: boolean
  current_binary_sha256?: string
  managed: boolean
  update_available: boolean
  runtime_version_matches: boolean
}

export interface Share {
  uri: string
  qr_content: string
  client_yaml: string
}

export interface OnboardingInput {
  public_host: string
  listener: {
    name: string
    listen_port: number
    server_name: string
    reality_dest: string
    udp_enabled: boolean
  }
  user: {
    name: string
    expires_at: string | null
  }
}

export interface OnboardingResult {
  listener: Listener
  user: User
  revision: Revision
  share: Share
}

export interface ConfigPreview {
  yaml: string
  sha256: string
  revealed: boolean
}

export interface GeneratedKeypair {
  private_key: string
  public_key: string
  short_id: string
}

function mutation<T>(
  path: string,
  csrfToken: string,
  method = 'POST',
  body?: unknown,
): Promise<T> {
  const headers: Record<string, string> = {
    'X-CSRF-Token': csrfToken,
  }
  if (body !== undefined) {
    headers['Content-Type'] = 'application/json'
  }
  return apiRequest<T>(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
  })
}

export async function listListeners(): Promise<Listener[]> {
  return (await apiRequest<{ listeners: Listener[] }>('/api/v1/listeners'))
    .listeners
}

export function completeOnboarding(
  csrfToken: string,
  input: OnboardingInput,
): Promise<OnboardingResult> {
  return mutation<OnboardingResult>(
    '/api/v1/onboarding',
    csrfToken,
    'POST',
    input,
  )
}

export function getListener(id: string): Promise<Listener> {
  return apiRequest<Listener>(`/api/v1/listeners/${encodeURIComponent(id)}`)
}

export async function createListener(
  csrfToken: string,
  input: ListenerInput,
): Promise<Listener> {
  return (
    await mutation<{ listener: Listener }>(
      '/api/v1/listeners',
      csrfToken,
      'POST',
      input,
    )
  ).listener
}

export async function updateListener(
  csrfToken: string,
  id: string,
  input: ListenerInput,
): Promise<Listener> {
  return (
    await mutation<{ listener: Listener }>(
      `/api/v1/listeners/${encodeURIComponent(id)}`,
      csrfToken,
      'PUT',
      input,
    )
  ).listener
}

export function deleteListener(
  csrfToken: string,
  id: string,
): Promise<void> {
  return mutation<void>(
    `/api/v1/listeners/${encodeURIComponent(id)}`,
    csrfToken,
    'DELETE',
  )
}

export async function setListenerEnabled(
  csrfToken: string,
  id: string,
  enabled: boolean,
): Promise<Listener> {
  return (
    await mutation<{ listener: Listener }>(
      `/api/v1/listeners/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`,
      csrfToken,
    )
  ).listener
}

export function generateRealityKeypair(
  csrfToken: string,
): Promise<GeneratedKeypair> {
  return mutation<GeneratedKeypair>(
    '/api/v1/listeners/generate-reality-keypair',
    csrfToken,
  )
}

export async function createUser(
  csrfToken: string,
  listenerID: string,
  input: UserInput,
): Promise<User> {
  return (
    await mutation<{ user: User }>(
      `/api/v1/listeners/${encodeURIComponent(listenerID)}/users`,
      csrfToken,
      'POST',
      input,
    )
  ).user
}

export async function updateUser(
  csrfToken: string,
  listenerID: string,
  userID: string,
  input: UserInput,
): Promise<User> {
  return (
    await mutation<{ user: User }>(
      `/api/v1/listeners/${encodeURIComponent(listenerID)}/users/${encodeURIComponent(userID)}`,
      csrfToken,
      'PUT',
      input,
    )
  ).user
}

export function deleteUser(
  csrfToken: string,
  listenerID: string,
  userID: string,
): Promise<void> {
  return mutation<void>(
    `/api/v1/listeners/${encodeURIComponent(listenerID)}/users/${encodeURIComponent(userID)}`,
    csrfToken,
    'DELETE',
  )
}

export async function setUserEnabled(
  csrfToken: string,
  listenerID: string,
  userID: string,
  enabled: boolean,
): Promise<User> {
  return (
    await mutation<{ user: User }>(
      `/api/v1/listeners/${encodeURIComponent(listenerID)}/users/${encodeURIComponent(userID)}/${enabled ? 'enable' : 'disable'}`,
      csrfToken,
    )
  ).user
}

export function getShare(
  listenerID: string,
  userID: string,
): Promise<Share> {
  return apiRequest<Share>(
    `/api/v1/listeners/${encodeURIComponent(listenerID)}/users/${encodeURIComponent(userID)}/share`,
  )
}

export function getRuntimeStatus(): Promise<RuntimeStatus> {
  return apiRequest<RuntimeStatus>('/api/v1/runtime/status')
}

export async function getRuntimeLogs(limit = 200): Promise<RuntimeLog[]> {
  return (
    await apiRequest<{ logs: RuntimeLog[] }>(
      `/api/v1/runtime/logs?limit=${limit}`,
    )
  ).logs
}

export function runRuntimeAction(
  csrfToken: string,
  action: 'start' | 'stop' | 'restart' | 'reload',
): Promise<void> {
  return mutation<void>(`/api/v1/runtime/${action}`, csrfToken)
}

export function getConfigPreview(
  reveal = false,
): Promise<ConfigPreview> {
  return apiRequest<ConfigPreview>(
    `/api/v1/config/preview${reveal ? '?reveal=true' : ''}`,
    reveal
      ? { headers: { 'X-Confirm-Sensitive': 'reveal-current-config' } }
      : {},
  )
}

export function validateConfig(
  csrfToken: string,
): Promise<{ valid: boolean; sha256: string }> {
  return mutation('/api/v1/config/validate', csrfToken)
}

export async function listRevisions(): Promise<Revision[]> {
  return (
    await apiRequest<{ revisions: Revision[] }>('/api/v1/config/revisions')
  ).revisions
}

export async function rollbackRevision(
  csrfToken: string,
  revisionID: string,
): Promise<Revision> {
  return (
    await mutation<{ revision: Revision }>(
      `/api/v1/config/revisions/${encodeURIComponent(revisionID)}/rollback`,
      csrfToken,
    )
  ).revision
}

export function getSettings(): Promise<Settings> {
  return apiRequest<Settings>('/api/v1/settings')
}

export async function updateSettings(
  csrfToken: string,
  input: SettingsInput,
): Promise<Settings> {
  return (
    await mutation<{ settings: Settings }>(
      '/api/v1/settings',
      csrfToken,
      'PUT',
      input,
    )
  ).settings
}

export function restartApplication(
  csrfToken: string,
): Promise<{ restarting: boolean }> {
  return mutation('/api/v1/system/restart', csrfToken)
}

export function getEndpointSettings(): Promise<EndpointSettingsState> {
  return apiRequest<EndpointSettingsState>('/api/v1/settings/endpoints')
}

export function updateEndpointSettings(
  csrfToken: string,
  input: Omit<EndpointSettings, 'requires_mui_restart' | 'requires_mihomo_restart' | 'updated_at'>,
): Promise<EndpointSettingsState> {
  return mutation<EndpointSettingsState>(
    '/api/v1/settings/endpoints',
    csrfToken,
    'PUT',
    input,
  )
}

export function testCore(
  csrfToken: string,
): Promise<{ version: string }> {
  return mutation('/api/v1/settings/test-core', csrfToken)
}

export function testController(
  csrfToken: string,
): Promise<{ version: RuntimeStatus['version'] }> {
  return mutation('/api/v1/settings/test-controller', csrfToken)
}

export function getCoreStatus(): Promise<CoreStatus> {
  return apiRequest<CoreStatus>('/api/v1/system/core')
}

export function updateCoreSettings(
  csrfToken: string,
  input: Pick<CoreSettings, 'channel' | 'auto_update' | 'check_interval'>,
): Promise<CoreStatus> {
  return mutation(
    '/api/v1/system/core/settings',
    csrfToken,
    'PUT',
    input,
  )
}

export function checkCore(
  csrfToken: string,
): Promise<CoreIdentity> {
  return mutation('/api/v1/system/core/check', csrfToken)
}

export function updateCore(
  csrfToken: string,
): Promise<{ changed: boolean; manifest: CoreManifest }> {
  return mutation('/api/v1/system/core/update', csrfToken)
}

export function rollbackCore(
  csrfToken: string,
): Promise<CoreManifest> {
  return mutation('/api/v1/system/core/rollback', csrfToken)
}

export async function listAuditEntries(): Promise<AuditEntry[]> {
  return (
    await apiRequest<{ entries: AuditEntry[] }>('/api/v1/audit-logs')
  ).entries
}
