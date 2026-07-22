export interface ApiSuccess {
  ok: true
}

export interface ApiError {
  ok: false
  error: string
  message?: string
  invalidKeys?: string[]
  startupOnlyKeys?: string[]
  unsupportedKeys?: string[]
  managedKeys?: string[]
}

export interface AdminChart {
  name: string
  id: number
}

export interface AdminRoomHost {
  id: number
  name: string
  connected: boolean
}

export interface AdminRoomState {
  type: string
  ready_users?: number[]
  ready_count?: number
  results_count?: number
  aborted_count?: number
  finished_users?: number[]
  aborted_users?: number[]
}

export interface AdminRoomUser {
  id: number
  name: string
  connected: boolean
  is_host: boolean
  game_time: number | null
  language: string
  finished?: boolean
  aborted?: boolean
  record_id?: number
}

export interface AdminRoomMonitor {
  id: number
  name: string
  connected: boolean
  language: string
}

export interface AdminContest {
  whitelist_count: number
  whitelist: number[]
  manual_start: boolean
  auto_disband: boolean
}

export interface RoomLogLine {
  message: string
  timestamp: number
}

export interface AdminRoom {
  roomid: string
  max_users: number
  current_users: number
  current_monitors: number
  replay_eligible: boolean
  live: boolean
  locked: boolean
  cycle: boolean
  host: AdminRoomHost
  state: AdminRoomState
  chart: AdminChart | null
  contest: AdminContest | null
  users: AdminRoomUser[]
  monitors: AdminRoomMonitor[]
  recent_logs: RoomLogLine[]
}

export interface AdminRoomsResponse {
  ok: true
  rooms: AdminRoom[]
}

export interface AdminUserListItem {
  id: number
  name: string
  connected: boolean
  monitor: boolean
  room: string
  banned: boolean
  language: string
}

export interface AdminUsersResponse {
  ok: true
  users: AdminUserListItem[]
}

export interface AdminUserDetail {
  id: number
  name: string
  monitor: boolean
  connected: boolean
  room: string | null
  banned: boolean
}

export interface AdminUserDetailResponse {
  ok: true
  user: AdminUserDetail
}

export interface MoveUserRequest {
  roomId: string
  monitor?: boolean
}

export interface BanUserRequest {
  userId: number
  banned: boolean
  disconnect: boolean
}

export interface BanRoomUserRequest {
  userId: number
  roomId: string
  banned: boolean
}

export interface ServerMetricsInfo {
  name: string
  version: string
}

export interface ProcessMetrics {
  pid: number
  uptime: number
  goVersion: string
  runtime: string
  platform: string
  arch: string
  goroutines: number
  numCPU: number
  pprofURL: string
}

export interface MemoryMetrics {
  rss: number
  heapUsed: number
  heapTotal: number
  systemTotal: number
  alloc: number
  totalAlloc: number
  sys: number
  heapAlloc: number
  heapSys: number
  numGC: number
}

export interface CpuMetrics {
  cores: number
  percent: number
}

export interface BusinessMetrics {
  activeSessions: number
  onlineUsers: number
  activeRooms: number
  wsConnections: number
  serverBannedUsers: number
  roomBannedUsers: number
  tempAdminTokens: number
  replayEnabled: boolean
  roomCreationEnabled: boolean
}

export interface AgentMetrics {
  enabled: boolean
  online: boolean
  endpoint?: string
  consumerId?: string
  agentVersion?: string
  lastSeen?: number
  ackedSequence?: number
  latestSequence?: number
  pendingEvents?: number
  outboxBytes?: number
  droppedNormal?: number
}

export type MetricsHistoryItem = Record<string, unknown>

export interface AdminMetricsResponse {
  ok: true
  timestamp: number
  server: ServerMetricsInfo
  process: ProcessMetrics
  memory: MemoryMetrics
  cpu: CpuMetrics
  business: BusinessMetrics
  agent: AgentMetrics
  history?: MetricsHistoryItem[]
}

export type RuntimeConfigKey =
  | "ROOM_CREATION_ENABLED"
  | "REPLAY_ENABLED"
  | "ROOM_MAX_USERS"
  | "PLAYING_RECONNECT_GRACE"
  | "MAX_ROOMS"
  | "MAX_CONNECTIONS"
  | "CONNECTION_RATE_LIMIT"
  | "COMMAND_RATE_LIMIT"
  | "HTTP_RATE_LIMIT_MAX_REQUESTS"
  | "HTTP_RATE_LIMIT_WINDOW_MS"
  | "CHAT_ENABLED"
  | "REPLAY_TTL_DAYS"
  | "ROOM_LIST_TIP"
  | "LOG_LEVEL"
  | "LOG_COMPRESS_AFTER_DAYS"
  | "LOG_MAX_TOTAL_MB"

export type RuntimeConfig = Partial<Record<RuntimeConfigKey, unknown>>

export interface RuntimeConfigResponse {
  ok: true
  managedKeys: string[]
  rollbackAvailable: boolean
  config: RuntimeConfig
}

export interface RuntimeConfigUpdateResponse {
  ok: true
  updatedKeys: string[]
  rollbackAvailable: boolean
  config: RuntimeConfig
}

export interface RuntimeConfigErrorResponse extends ApiError {
  error: "bad-runtime-config"
  invalidKeys: string[]
  startupOnlyKeys: string[]
  unsupportedKeys: string[]
  managedKeys: string[]
}

export interface ConsoleLogLine {
  level: string
  message: string
  timestamp: number
}

export interface ConsoleLogsResponse {
  ok: true
  lines: ConsoleLogLine[]
}

export interface ConsoleCommandRequest {
  command: string
}

export interface ConsoleOutputLine {
  kind: string
  text: string
}

export interface ConsoleCommandResponse {
  ok: true
  lines: ConsoleOutputLine[]
}

export interface ContestConfigRequest {
  enabled?: boolean
  whitelist?: number[]
}

export interface ContestWhitelistRequest {
  userIds: number[]
}

export interface ContestStartRequest {
  force?: boolean
}

export type FeishuRegistrationStatus =
  | "pending"
  | "waiting_qr"
  | "qr_ready"
  | "polling"
  | "slow_down"
  | "domain_switched"
  | "completed"
  | "failed"
  | "cancelled"
  | string

export interface CreateFeishuRegistrationRequest {
  target_id: string
  receive_open_id?: string
  events?: string[]
  live_update?: boolean
}

export interface FeishuRegistrationTask {
  ok: boolean
  task_id: string
  status: FeishuRegistrationStatus
  qr_url?: string
  qr_expires_at?: string
  interval?: number
  client_id?: string
  user_open_id?: string
  error?: string
}

export type OtpMode = "otp" | "cli"

export interface OtpRequestResponse {
  ok: true
  ssid: string
  expiresIn: number
  mode: OtpMode
}

export interface OtpVerifyRequest {
  mode: OtpMode
  ssid: string
  otp?: string
}

export interface OtpVerifyResponse {
  ok: true
  token: string
  expiresAt: number
  expiresIn: number
  mode?: OtpMode
}
