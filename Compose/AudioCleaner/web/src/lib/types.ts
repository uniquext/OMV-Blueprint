export type SupportedLanguage = 'zh-CN' | 'en-US'

export interface ApiEnvelope<T> {
  code: number
  message: string
  data: T
}

export interface PageResult<T> {
  items: T[]
  page: number
  page_size: number
  total: number
}

export interface PageRequest {
  page?: number
  page_size?: number
}

export interface AudioCleanerConfig {
  media: MediaConfig
  audio: AudioConfig
  pipeline: PipelineConfig
  backup: BackupConfig
  validation: ValidationConfig
  ui: UIConfig
  scan: ScanConfig
  notifications: NotificationsConfig
}

export interface MediaConfig {
  roots: string[]
  extensions: string[]
  exclude_dirs: string[]
  exclude_patterns: string[]
}

export interface AudioConfig {
  incompatible_codecs: string[]
}

export interface PipelineConfig {
  workers: number
  max_retries: number
  retry_delay_seconds: number
  stat_quiet_seconds: number
  job_timeout_minutes: number
}

export interface BackupConfig {
  retention_days: number
}

export interface OperationStatusResponse {
  status: string
}

export type BackupPatchResponse = BackupConfig | OperationStatusResponse

export interface ValidationConfig {
  max_size_ratio: number
  max_size_increase_mb: number
  duration_tolerance_seconds: number
}

export interface UIConfig {
  language: SupportedLanguage
}

export interface ScanConfig {
  startup_scan_enabled: boolean
  watchdog_enabled: boolean
}

export interface NotificationsConfig {
  enabled: boolean
  targets: string[]
}

export type JobStatus = 'compatible' | 'processing' | 'processed' | 'failed' | 'restored' | string
export type DiscoverySource = 'scan' | 'watchdog' | 'manual' | string
export type JobKind = 'process' | 'restore'
export type JobResult = 'processing' | 'compatible' | 'succeeded' | 'failed'
export type RuntimePhase =
  | 'queued'
  | 'retry_wait'
  | 'checking'
  | 'transcoding'
  | 'verifying'
  | 'backing_up'
  | 'replacing'
export type BackupAvailability = 'available' | 'missing' | 'restored' | 'expired' | string

export interface HistoryRecord {
  id: number
  file_id: number
  path: string
  kind: JobKind
  trigger_source: DiscoverySource
  result: JobResult
  final_error: string
  started_at: string
  finished_at: string
}

export interface RuntimeLogResponse {
  content: string
  lines: number
}

export interface BackupRecord {
  id: number
  file_id: number
  original_path: string
  backup_path: string
  created_at: string
  restored_at: string
  restore_safety_path: string
  availability: BackupAvailability
}

export interface ServiceStatus {
  status: 'running' | 'restarting' | 'restart_failed' | string
  counts: Record<string, number>
  backup_usage_bytes: number
  transcode_success_rate: TranscodeSuccessRate
}

export interface RuntimeTask {
  path: string
  phase: RuntimePhase
  source?: DiscoverySource
}

export interface RuntimeTasksSnapshot {
  waiting_tasks: RuntimeTask[]
  active_tasks: RuntimeTask[]
}

export interface TranscodeSuccessRate {
  succeeded: number
  failed: number
  rate: number
}

export interface ScanResponse {
  status: 'queued' | string
}

export interface CleanupExpiredResponse {
  removed: number
}

export interface RestoreResult {
  safety_path: string
}

export type EventStreamStatus = 'connecting' | 'connected' | 'disconnected' | 'error'

export interface ServerSentEvent {
  type: string
  snapshot_id: number
  data: Record<string, unknown> | null
}
