import { apiRequest } from './client'

// Protocol identifiers are registry data returned by /api/v1/capabilities.
// Keeping this open-ended is what lets a newly registered backend protocol
// appear in the shared node views without another frontend release.
export type ProtocolKind = string
export type VLESSHandlerKind = 'raw' | 'websocket' | 'grpc' | 'xhttp'
export type VLESSSecurityKind = 'none' | 'tls' | 'reality' | 'shadow-tls' | 'res-tls' | 'jls'

export interface MuxSpec {
  padding?: boolean
  brutal?: { enabled?: boolean; up?: string; down?: string }
}

export interface TLSConfig {
  certificate: string
  private_key: string
  client_auth_type?: string
  client_auth_cert?: string
  ech_key?: string
  allow_insecure?: boolean
}

export interface RealityConfig {
  destination: string
  private_key: string
  public_key: string
  short_ids: string[]
  server_names: string[]
  max_time_difference?: number
  proxy?: string
  limit_fallback_upload?: { after_bytes?: number; bytes_per_sec?: number; burst_bytes_per_sec?: number }
  limit_fallback_download?: { after_bytes?: number; bytes_per_sec?: number; burst_bytes_per_sec?: number }
}

export interface XHTTPConfig {
  path?: string
  host?: string
  mode?: string
  x_padding_bytes?: string
  x_padding_obfs_mode?: boolean
  x_padding_key?: string
  x_padding_header?: string
  x_padding_placement?: string
  x_padding_method?: string
  uplink_http_method?: string
  session_placement?: string
  session_key?: string
  seq_placement?: string
  seq_key?: string
  uplink_data_placement?: string
  uplink_data_key?: string
  uplink_chunk_size?: string
  no_sse_header?: boolean
  sc_stream_up_server_secs?: string
  sc_max_buffered_posts?: string
  sc_max_each_post_bytes?: string
}

export interface VLESSSpec {
  decryption?: string
  handler: {
    type: VLESSHandlerKind
    websocket?: { path: string }
    grpc?: { service_name: string }
    xhttp?: XHTTPConfig
  }
  security: {
    type: VLESSSecurityKind
    tls?: TLSConfig
    reality?: RealityConfig
    shadow_tls?: {
      version?: number
      password?: string
      users?: Array<{ name: string; password: string }>
      handshake: { destination: string; proxy?: string }
      handshake_for_server_name?: Record<string, { destination: string; proxy?: string }>
      strict_mode?: boolean
      wildcard_sni?: string
    }
    res_tls?: { destination: string; password: string; version_hint?: string; script?: string; min_record_length?: number; proxy?: string }
    jls?: { users: Array<{ username: string; password: string }>; server_name?: string; destination: string; alpn?: string[]; proxy?: string; rate_limit?: number }
  }
  mux?: MuxSpec
}

export interface Hysteria2Spec {
  obfs?: string
  obfs_password?: string
  obfs_min_packet_size?: number
  obfs_max_packet_size?: number
  certificate: string
  private_key: string
  client_auth_type?: string
  client_auth_cert?: string
  ech_key?: string
  max_idle_time?: number
  alpn?: string[]
  up?: string
  down?: string
  ignore_client_bandwidth?: boolean
  masquerade?: string
  cwnd?: number
  bbr_profile?: string
  udp_mtu?: number
  mux?: MuxSpec
  realm?: {
    enabled: boolean
    server_url?: string
    token?: string
    realm_id?: string
    stun_servers?: string[]
    server_name?: string
    skip_cert_verify?: boolean
    name_cert_verify?: string
    fingerprint?: string
    certificate?: string
    private_key?: string
    alpn?: string[]
    proxy?: string
  }
  initial_stream_receive_window?: number
  max_stream_receive_window?: number
  initial_connection_receive_window?: number
  max_connection_receive_window?: number
}

export interface User {
  [key: string]: unknown
  id: string
  node_id: string
  name: string
  enabled: boolean
  vless?: { uuid: string; flow?: string }
  hysteria2?: { password: string }
  expires_at: string | null
  created_at: string
  updated_at: string
}

export interface AccessProfile {
  id: string
  node_id: string
  name: string
  default: boolean
  public_host: string
  public_port: number
  server_name: string
  fingerprint: string
  packet_encoding: string
  allow_insecure: boolean
  created_at?: string
  updated_at?: string
}

export type AccessProfileInput = Omit<AccessProfile, 'id' | 'node_id' | 'created_at' | 'updated_at'> & { id?: string }

export interface Node {
  [key: string]: unknown
  id: string
  name: string
  enabled: boolean
  listen: string
  port: string
  protocol: ProtocolKind
  schema_version: number
  vless?: VLESSSpec
  hysteria2?: Hysteria2Spec
  users: User[]
  access_profiles: AccessProfile[]
  secrets_set: Record<string, boolean>
  generation: number
  created_at: string
  updated_at: string
}

export interface NodeInput {
  [key: string]: unknown
  name: string
  enabled: boolean
  listen: string
  port: string
  protocol: ProtocolKind
  vless?: VLESSSpec
  hysteria2?: Hysteria2Spec
  users?: UserInput[]
  access_profiles?: AccessProfileInput[]
  generation?: number
}

export interface UserInput {
  [key: string]: unknown
  name: string
  enabled: boolean
  vless?: { uuid: string; flow?: string }
  hysteria2?: { password: string }
  expires_at: string | null
}

export interface CapabilityManifest {
  schema_version: number
  node_schema_version: number
  source: { repository: string; branch: string; commit: string }
  node_fields: FieldCapability[]
  access_profile_fields: FieldCapability[]
  protocols: ProtocolCapability[]
}

export type FieldCapabilityType = 'string' | 'text' | 'secret' | 'boolean' | 'integer' | 'string-list' | 'object-list' | 'record'
export type ComponentGroup = 'transport' | 'security' | 'extension'

export interface FieldCondition {
  path: string
  equals: unknown
}

export interface FieldCapability {
  path: string
  source_key?: string
  label: string
  type: FieldCapabilityType
  required?: boolean
  secret?: boolean
  advanced?: boolean
  minimum?: number
  maximum?: number
  options?: string[]
  item_fields?: FieldCapability[]
  item_key_label?: string
  visible_when?: FieldCondition
  description?: string
}

export interface LayerCapability {
  group: ComponentGroup
  required: boolean
  multiple: boolean
  locked?: boolean
  default_component?: string
}

export interface ComponentCapability {
  group: ComponentGroup
  kind: string
  label: string
  default_config: unknown
  config_path: string
  selection_path?: string
  enabled_path?: string
  fields?: FieldCapability[]
  requires?: string[]
  conflicts?: string[]
}

export interface ProtocolCapability {
  kind: ProtocolKind
  label: string
  default_node: Partial<NodeInput>
  default_user: Partial<UserInput>
  layers: LayerCapability[]
  components: ComponentCapability[]
  fields?: FieldCapability[]
  user_fields: FieldCapability[]
  features?: string[]
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
  node: NodeInput
}

export interface OnboardingResult {
  node: Node
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

export function getCapabilities(): Promise<CapabilityManifest> {
  return apiRequest<CapabilityManifest>('/api/v1/capabilities')
}

export async function listNodes(): Promise<Node[]> {
  return (await apiRequest<{ nodes: Node[] }>('/api/v1/nodes')).nodes
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

export function getNode(id: string): Promise<Node> {
  return apiRequest<Node>(`/api/v1/nodes/${encodeURIComponent(id)}`)
}

export async function createNode(
  csrfToken: string,
  input: NodeInput,
): Promise<Node> {
  return (
    await mutation<{ node: Node }>(
      '/api/v1/nodes',
      csrfToken,
      'POST',
      input,
    )
  ).node
}

export async function updateNode(
  csrfToken: string,
  id: string,
  input: NodeInput,
): Promise<Node> {
  return (
    await mutation<{ node: Node }>(
      `/api/v1/nodes/${encodeURIComponent(id)}`,
      csrfToken,
      'PUT',
      input,
    )
  ).node
}

export function deleteNode(
  csrfToken: string,
  id: string,
): Promise<void> {
  return mutation<void>(
    `/api/v1/nodes/${encodeURIComponent(id)}`,
    csrfToken,
    'DELETE',
  )
}

export async function setNodeEnabled(
  csrfToken: string,
  id: string,
  enabled: boolean,
): Promise<Node> {
  return (
    await mutation<{ node: Node }>(
      `/api/v1/nodes/${encodeURIComponent(id)}/${enabled ? 'enable' : 'disable'}`,
      csrfToken,
    )
  ).node
}

export interface CloneNodeInput {
  name: string
  port: string
  include_users: boolean
}

export function cloneNode(
  csrfToken: string,
  id: string,
  input: CloneNodeInput,
): Promise<{ node: Node; revision: Revision }> {
  return mutation(
    `/api/v1/nodes/${encodeURIComponent(id)}/clone`,
    csrfToken,
    'POST',
    input,
  )
}

export function setNodesEnabled(
  csrfToken: string,
  nodeIDs: string[],
  enabled: boolean,
): Promise<{ nodes: Node[]; revision: Revision }> {
  return mutation(
    '/api/v1/nodes/batch-enabled',
    csrfToken,
    'POST',
    { node_ids: nodeIDs, enabled },
  )
}

export function generateRealityKeypair(
  csrfToken: string,
): Promise<GeneratedKeypair> {
  return mutation<GeneratedKeypair>(
    '/api/v1/nodes/generate-reality-keypair',
    csrfToken,
  )
}

export async function createUser(
  csrfToken: string,
  nodeID: string,
  input: UserInput,
): Promise<User> {
  return (
    await mutation<{ user: User }>(
      `/api/v1/nodes/${encodeURIComponent(nodeID)}/users`,
      csrfToken,
      'POST',
      input,
    )
  ).user
}

export function createUsers(
  csrfToken: string,
  nodeID: string,
  users: UserInput[],
): Promise<{ users: User[]; revision: Revision }> {
  return mutation(
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/users/batch`,
    csrfToken,
    'POST',
    { users },
  )
}

export async function updateUser(
  csrfToken: string,
  nodeID: string,
  userID: string,
  input: UserInput,
): Promise<User> {
  return (
    await mutation<{ user: User }>(
      `/api/v1/nodes/${encodeURIComponent(nodeID)}/users/${encodeURIComponent(userID)}`,
      csrfToken,
      'PUT',
      input,
    )
  ).user
}

export function deleteUser(
  csrfToken: string,
  nodeID: string,
  userID: string,
): Promise<void> {
  return mutation<void>(
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/users/${encodeURIComponent(userID)}`,
    csrfToken,
    'DELETE',
  )
}

export async function setUserEnabled(
  csrfToken: string,
  nodeID: string,
  userID: string,
  enabled: boolean,
): Promise<User> {
  return (
    await mutation<{ user: User }>(
      `/api/v1/nodes/${encodeURIComponent(nodeID)}/users/${encodeURIComponent(userID)}/${enabled ? 'enable' : 'disable'}`,
      csrfToken,
    )
  ).user
}

export function setUsersEnabled(
  csrfToken: string,
  nodeID: string,
  userIDs: string[],
  enabled: boolean,
): Promise<{ users: User[]; revision: Revision }> {
  return mutation(
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/users/batch-enabled`,
    csrfToken,
    'POST',
    { user_ids: userIDs, enabled },
  )
}

export function getShare(
  nodeID: string,
  userID: string,
  profileID = '',
): Promise<Share> {
  return apiRequest<Share>(
    `/api/v1/nodes/${encodeURIComponent(nodeID)}/users/${encodeURIComponent(userID)}/share${profileID ? `?profile_id=${encodeURIComponent(profileID)}` : ''}`,
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
